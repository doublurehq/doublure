package gitops_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

func TestListPreviews(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/gitops/contents/previews" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_, _ = io.WriteString(w, `[
			{"type":"dir","name":"feature-b","path":"previews/feature-b"},
			{"type":"file","name":"README.md","path":"previews/README.md"},
			{"type":"dir","name":"feature-a","path":"previews/feature-a"}
		]`)
	}))
	defer srv.Close()

	got, err := newClient(t, srv).ListPreviews(context.Background())
	if err != nil {
		t.Fatalf("ListPreviews: %v", err)
	}
	if want := []string{"feature-a", "feature-b"}; !slices.Equal(got, want) {
		t.Errorf("ListPreviews = %v, want %v", got, want)
	}
}

func TestListPreviews_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}))
	defer srv.Close()

	got, err := newClient(t, srv).ListPreviews(context.Background())
	if err != nil {
		t.Fatalf("ListPreviews: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("ListPreviews = %#v, want an empty slice", got)
	}
}

func TestGetPullRequest(t *testing.T) {
	const (
		head = `"head":{"ref":"feature/pricing","sha":"abc123","repo":{"full_name":"%s"}}`
		base = `"base":{"repo":{"full_name":"acme/backend-1"}}`
		rest = `"user":{"login":"octocat"},"html_url":"https://github.com/acme/backend-1/pull/42"`
	)
	pr := func(open bool, headFullName string, labels ...string) *gitops.PullRequest {
		if labels == nil {
			labels = []string{}
		}
		return &gitops.PullRequest{
			Open:         open,
			HeadRef:      "feature/pricing",
			HeadSHA:      "abc123",
			Author:       "octocat",
			URL:          "https://github.com/acme/backend-1/pull/42",
			Labels:       labels,
			FullName:     "acme/backend-1",
			HeadFullName: headFullName,
		}
	}
	cases := []struct {
		name       string
		status     int
		body       string
		want       *gitops.PullRequest
		wantErr    error
		wantAnyErr bool
	}{
		{
			name:   "open with labels",
			status: http.StatusOK,
			body: `{"state":"open",` + fmt.Sprintf(head, "acme/backend-1") + `,` + base + `,` + rest +
				`,"labels":[{"name":"preview"},{"name":"bug"}]}`,
			want: pr(true, "acme/backend-1", "preview", "bug"),
		},
		{
			name:   "closed",
			status: http.StatusOK,
			body:   `{"state":"closed",` + fmt.Sprintf(head, "acme/backend-1") + `,` + base + `,` + rest + `}`,
			want:   pr(false, "acme/backend-1"),
		},
		{
			name:   "fork head",
			status: http.StatusOK,
			body:   `{"state":"open",` + fmt.Sprintf(head, "octocat/backend-1") + `,` + base + `,` + rest + `}`,
			want:   pr(true, "octocat/backend-1"),
		},
		{name: "not found", status: http.StatusNotFound, body: `{"message":"Not Found"}`, wantErr: gitops.ErrNotFound},
		{name: "server error", status: http.StatusInternalServerError, body: `{"message":"boom"}`, wantAnyErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := getPullRequest(t, tc.status, tc.body)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
			case tc.wantAnyErr:
				if err == nil {
					t.Fatalf("GetPullRequest = %+v, nil; want an error", got)
				}
			case err != nil:
				t.Fatalf("GetPullRequest: %v", err)
			case !reflect.DeepEqual(got, tc.want):
				t.Errorf("GetPullRequest = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// getPullRequest calls GetPullRequest on acme/backend-1#42 against a server
// answering status and body.
func getPullRequest(t *testing.T, status int, body string) (*gitops.PullRequest, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/repos/acme/backend-1/pulls/42" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	return newClient(t, srv).GetPullRequest(context.Background(), "acme", "backend-1", 42)
}

// singlePin reports whether exactly one entry of vals is pinned; it turns
// false once a second PR deploys to the preview.
func singlePin(vals preview.PreviewValues) bool {
	n := 0
	for _, svc := range vals.Services {
		if svc.CommitSha != "" {
			n++
		}
	}
	return n == 1
}

func runDelete(
	t *testing.T,
	g *teardownGitHub,
	stillDelete func(preview.PreviewValues) bool,
) (*gitops.DeleteResult, error) {
	t.Helper()
	g.t = t
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return newClient(t, srv).DeletePreviewWithRetry(
		context.Background(), "feature-x", "chore(preview): expire feature-x preview", stillDelete,
	)
}

func TestDeletePreviewWithRetry_Deletes(t *testing.T) {
	g := &teardownGitHub{gets: []fileState{{sha: "blob-1", body: onlyPR42}}}
	res, err := runDelete(t, g, singlePin)
	if err != nil {
		t.Fatalf("DeletePreviewWithRetry: %v", err)
	}
	if !res.Deleted || res.CommitURL != "https://github.com/acme/gitops/commit/c0ffee" {
		t.Errorf("result = %+v, want deleted with the commit url", res)
	}
	if len(res.Values.Services) != 2 || res.Values.Services[0].CommitSha != "aaa" {
		t.Errorf("Values = %+v, want the deleted file", res.Values)
	}
	reqs := g.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodDelete || reqs[0].sha != "blob-1" ||
		reqs[0].message != "chore(preview): expire feature-x preview" {
		t.Errorf("requests = %+v, want one DELETE at blob-1", reqs)
	}
}

func TestDeletePreviewWithRetry_StillDeleteFalse(t *testing.T) {
	g := &teardownGitHub{gets: []fileState{{sha: "blob-1", body: pr42AndPR7}}}
	res, err := runDelete(t, g, singlePin)
	if err != nil {
		t.Fatalf("DeletePreviewWithRetry: %v", err)
	}
	if res.Deleted {
		t.Errorf("result = %+v, want nothing deleted", res)
	}
	if reqs := g.requests(); len(reqs) != 0 {
		t.Errorf("requests = %+v, want no DELETE", reqs)
	}
}

func TestDeletePreviewWithRetry_ConflictThenRecheckFalse(t *testing.T) {
	g := &teardownGitHub{
		gets:   []fileState{{sha: "blob-1", body: onlyPR42}, {sha: "blob-2", body: pr42AndPR7}},
		writes: []int{http.StatusConflict},
	}
	res, err := runDelete(t, g, singlePin)
	if err != nil {
		t.Fatalf("DeletePreviewWithRetry: %v", err)
	}
	if res.Deleted {
		t.Errorf("result = %+v, want nothing deleted", res)
	}
	if reqs := g.requests(); len(reqs) != 1 {
		t.Errorf("requests = %+v, want a single DELETE", reqs)
	}
}

func TestDeletePreviewWithRetry_LastConflictThenRecheckFalse(t *testing.T) {
	gets := slices.Repeat([]fileState{{sha: "blob-1", body: onlyPR42}}, gitops.MaxWriteRetries)
	g := &teardownGitHub{
		gets:   append(gets, fileState{sha: "blob-2", body: pr42AndPR7}),
		writes: slices.Repeat([]int{http.StatusConflict}, gitops.MaxWriteRetries),
	}
	res, err := runDelete(t, g, singlePin)
	if err != nil {
		t.Fatalf("DeletePreviewWithRetry: %v", err)
	}
	if res.Deleted {
		t.Errorf("result = %+v, want nothing deleted", res)
	}
	if got := len(g.requests()); got != gitops.MaxWriteRetries {
		t.Errorf("deletes = %d, want %d", got, gitops.MaxWriteRetries)
	}
}

func TestDeletePreviewWithRetry_NotFound(t *testing.T) {
	cases := []struct {
		name    string
		gets    []fileState
		writes  []int
		deletes int
	}{
		{name: "on read", gets: []fileState{{}}},
		{
			name:    "on re-fetch",
			gets:    []fileState{{sha: "blob-1", body: onlyPR42}, {}},
			writes:  []int{http.StatusConflict},
			deletes: 1,
		},
		{
			name:    "on delete",
			gets:    []fileState{{sha: "blob-1", body: onlyPR42}},
			writes:  []int{http.StatusNotFound},
			deletes: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &teardownGitHub{gets: tc.gets, writes: tc.writes}
			res, err := runDelete(t, g, singlePin)
			if err != nil {
				t.Fatalf("DeletePreviewWithRetry: %v", err)
			}
			if res.Deleted {
				t.Errorf("result = %+v, want nothing deleted", res)
			}
			if got := len(g.requests()); got != tc.deletes {
				t.Errorf("deletes = %d, want %d", got, tc.deletes)
			}
		})
	}
}

func TestDeletePreviewWithRetry_ConflictsExhausted(t *testing.T) {
	g := &teardownGitHub{
		gets:   []fileState{{sha: "blob-1", body: onlyPR42}},
		writes: slices.Repeat([]int{http.StatusConflict}, gitops.MaxWriteRetries),
	}
	_, err := runDelete(t, g, singlePin)
	const want = "delete previews/feature-x/values.yaml: 3 attempts exhausted"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got := len(g.requests()); got != gitops.MaxWriteRetries {
		t.Errorf("deletes = %d, want %d", got, gitops.MaxWriteRetries)
	}
}

// fanOutGitHub answers PR state reads from states, keyed by "<repo>/pulls/<n>"
// (a missing key answers 500), lists no existing comment and records every
// comment write.
type fanOutGitHub struct {
	t      *testing.T
	states map[string]string
	mu     sync.Mutex
	writes []string
}

func (g *fanOutGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/repos/acme/")
	switch {
	case r.Method == http.MethodGet && strings.Contains(path, "/pulls/"):
		state, ok := g.states[path]
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"boom"}`)
			return
		}
		_, _ = io.WriteString(w, `{"state":"`+state+`"}`)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/comments"):
		_, _ = io.WriteString(w, `[]`)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/comments"):
		g.mu.Lock()
		g.writes = append(g.writes, path)
		g.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":1}`)
	default:
		g.t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}
}

func pinnedBy(name, prURL string) preview.ServiceEntry {
	return preview.ServiceEntry{Name: name, CommitSha: "abc", Metadata: &preview.Metadata{PRURL: prURL}}
}

func TestFanOutPRComments_SkipsClosedPRs(t *testing.T) {
	g := &fanOutGitHub{t: t, states: map[string]string{
		"backend-1/pulls/42": "open",
		"backend-2/pulls/7":  "closed",
	}}
	srv := httptest.NewServer(g)
	defer srv.Close()

	vals := preview.PreviewValues{Services: []preview.ServiceEntry{
		pinnedBy("backend-1", "https://github.com/acme/backend-1/pull/42"),
		pinnedBy("backend-2", "https://github.com/acme/backend-2/pull/7"),
	}}
	if err := newClient(t, srv).FanOutPRComments(context.Background(), vals, "## body"); err != nil {
		t.Fatalf("FanOutPRComments: %v", err)
	}
	if want := []string{"backend-1/issues/42/comments"}; !slices.Equal(g.writes, want) {
		t.Errorf("comment writes = %v, want %v", g.writes, want)
	}
}

func TestFanOutPRComments_StateError(t *testing.T) {
	g := &fanOutGitHub{t: t, states: map[string]string{
		"backend-1/pulls/42": "open",
		// front/pulls/9 answers 500
	}}
	srv := httptest.NewServer(g)
	defer srv.Close()

	vals := preview.PreviewValues{Services: []preview.ServiceEntry{
		pinnedBy("backend-1", "https://github.com/acme/backend-1/pull/42"),
		pinnedBy("front", "https://github.com/acme/front/pull/9"),
	}}
	err := newClient(t, srv).FanOutPRComments(context.Background(), vals, "## body")
	if err == nil || !strings.Contains(err.Error(), "service front (acme/front#9): pr state: ") {
		t.Fatalf("err = %v, want a pr state error for front", err)
	}
	if want := []string{"backend-1/issues/42/comments"}; !slices.Equal(g.writes, want) {
		t.Errorf("comment writes = %v, want %v", g.writes, want)
	}
}
