package gitops_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/doublurehq/doublure/internal/gitops"
)

const teardownPath = "/repos/acme/gitops/contents/previews/feature-x/values.yaml"

// fileState is one canned GET response: a values file at a blob SHA, or a 404
// when body is empty.
type fileState struct {
	sha  string
	body string
}

// teardownRequest is one recorded write (PUT or DELETE).
type teardownRequest struct {
	method  string
	message string
	sha     string
	content string
}

// teardownGitHub fakes the contents API for previews/feature-x/values.yaml.
// GETs are answered from gets in order (the last one repeats); PUT and DELETE
// get the next status from writes (200 once the list is exhausted).
type teardownGitHub struct {
	t      *testing.T
	mu     sync.Mutex
	gets   []fileState
	writes []int
	nGet   int
	seen   []teardownRequest
}

func (g *teardownGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.URL.Path != teardownPath {
		g.t.Errorf("unexpected path %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)
		return
	}
	switch r.Method {
	case http.MethodGet:
		st := g.gets[min(g.nGet, len(g.gets)-1)]
		g.nGet++
		if st.body == "" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
			return
		}
		_, _ = io.WriteString(w, encodeContent("previews/feature-x/values.yaml", st.sha, st.body))
	case http.MethodPut, http.MethodDelete:
		g.record(r)
		status := http.StatusOK
		if len(g.writes) > 0 {
			status, g.writes = g.writes[0], g.writes[1:]
		}
		w.WriteHeader(status)
		switch status {
		case http.StatusOK:
			_, _ = io.WriteString(w, `{"commit":{"html_url":"https://github.com/acme/gitops/commit/c0ffee"}}`)
		default:
			_, _ = io.WriteString(w, `{"message":"nope"}`)
		}
	default:
		g.t.Errorf("unexpected method %s", r.Method)
	}
}

func (g *teardownGitHub) record(r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var req struct {
		Message string `json:"message"`
		SHA     string `json:"sha"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		g.t.Errorf("decode %s body: %v", r.Method, err)
	}
	content, _ := base64.StdEncoding.DecodeString(req.Content)
	g.seen = append(g.seen, teardownRequest{
		method: r.Method, message: req.Message, sha: req.SHA, content: string(content),
	})
}

func (g *teardownGitHub) requests() []teardownRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.seen)
}

func runTeardown(t *testing.T, g *teardownGitHub) (*gitops.TeardownResult, error) {
	t.Helper()
	g.t = t
	srv := httptest.NewServer(g)
	t.Cleanup(srv.Close)
	return newClient(t, srv).TeardownPreviewWithRetry(context.Background(), "feature-x", "acme", "backend-1", 42)
}

const pr7URL = "https://github.com/acme/backend-2/pull/7"

// onlyPR42 is a preview pinned by acme/backend-1#42 alone.
const onlyPR42 = `services:
  - name: backend-1
    commitSha: aaa
    metadata:
      pr-url: https://github.com/acme/backend-1/pull/42
  - name: front
`

// pr42AndPR7 is a preview pinned by acme/backend-1#42 and acme/backend-2#7.
const pr42AndPR7 = `services:
  - name: backend-1
    commitSha: aaa
    metadata:
      pr-url: https://github.com/acme/backend-1/pull/42
  - name: backend-2
    commitSha: bbb
    metadata:
      pr-url: https://github.com/acme/backend-2/pull/7
  - name: front
`

func TestTeardown_UpdateWhenPinsRemain(t *testing.T) {
	g := &teardownGitHub{gets: []fileState{{sha: "blob-1", body: pr42AndPR7}}}
	result, err := runTeardown(t, g)
	if err != nil {
		t.Fatalf("TeardownPreviewWithRetry: %v", err)
	}
	reqs := g.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodPut {
		t.Fatalf("requests = %+v, want one PUT", reqs)
	}
	const wantMsg = "chore(preview): remove backend-1 from feature-x"
	if reqs[0].message != wantMsg || reqs[0].sha != "blob-1" {
		t.Errorf("PUT message/sha = %q/%q, want %q/blob-1", reqs[0].message, reqs[0].sha, wantMsg)
	}
	wantBody := "services:\n" +
		"  - name: backend-1\n" +
		"  - name: backend-2\n" +
		"    commitSha: bbb\n" +
		"    metadata:\n" +
		"      pr-author: \"\"\n" +
		"      pr-url: " + pr7URL + "\n" +
		"      pr-number: \"\"\n" +
		"      created-at: \"\"\n" +
		"      updated-at: \"\"\n" +
		"      vcs.ref.name: \"\"\n" +
		"      cicd.pipeline.run.url: \"\"\n" +
		"  - name: front\n"
	if reqs[0].content != wantBody {
		t.Errorf("PUT content mismatch\n got:\n%s\nwant:\n%s", reqs[0].content, wantBody)
	}
	if result.Deleted || !slices.Equal(result.Removed, []string{"backend-1"}) {
		t.Errorf("result = %+v, want Removed [backend-1], Deleted false", result)
	}
	if result.CommitMessage != wantMsg || !strings.HasSuffix(result.CommitURL, "/c0ffee") {
		t.Errorf("result message/url = %q/%q", result.CommitMessage, result.CommitURL)
	}
	if len(result.Values.Services) != 3 || result.Values.Services[1].CommitSha != "bbb" {
		t.Errorf("result.Values = %+v, want backend-2 still pinned", result.Values)
	}
}

func TestTeardown_DeleteWhenNoPinRemains(t *testing.T) {
	g := &teardownGitHub{gets: []fileState{{sha: "blob-1", body: onlyPR42}}}
	result, err := runTeardown(t, g)
	if err != nil {
		t.Fatalf("TeardownPreviewWithRetry: %v", err)
	}
	reqs := g.requests()
	if len(reqs) != 1 || reqs[0].method != http.MethodDelete {
		t.Fatalf("requests = %+v, want one DELETE", reqs)
	}
	const wantMsg = "chore(preview): delete feature-x preview"
	if reqs[0].message != wantMsg || reqs[0].sha != "blob-1" {
		t.Errorf("DELETE message/sha = %q/%q, want %q/blob-1", reqs[0].message, reqs[0].sha, wantMsg)
	}
	if !result.Deleted || !slices.Equal(result.Removed, []string{"backend-1"}) {
		t.Errorf("result = %+v, want Removed [backend-1], Deleted true", result)
	}
	if result.CommitMessage != wantMsg || !strings.HasSuffix(result.CommitURL, "/c0ffee") {
		t.Errorf("result message/url = %q/%q", result.CommitMessage, result.CommitURL)
	}
	if result.Values.Services != nil {
		t.Errorf("result.Values = %+v, want empty on delete", result.Values)
	}
}

func TestTeardown_NoOps(t *testing.T) {
	cases := []struct {
		name   string
		gets   []fileState
		writes []int
		want   int // expected number of writes
	}{
		{name: "file missing", gets: []fileState{{}}},
		{
			name: "no match means no write",
			gets: []fileState{{sha: "blob-1", body: strings.Replace(pr42AndPR7, "pull/42", "pull/43", 1)}},
		},
		{
			name:   "404 on re-fetch",
			gets:   []fileState{{sha: "blob-1", body: pr42AndPR7}, {}},
			writes: []int{http.StatusConflict},
			want:   1,
		},
		{
			name:   "404 on DELETE",
			gets:   []fileState{{sha: "blob-1", body: onlyPR42}},
			writes: []int{http.StatusNotFound},
			want:   1,
		},
		{
			name: "409 then re-fetch matches nothing",
			gets: []fileState{
				{sha: "blob-1", body: onlyPR42},
				{sha: "blob-2", body: "services:\n  - name: backend-1\n  - name: front\n"},
			},
			writes: []int{http.StatusConflict},
			want:   1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &teardownGitHub{gets: tc.gets, writes: tc.writes}
			result, err := runTeardown(t, g)
			if err != nil {
				t.Fatalf("TeardownPreviewWithRetry: %v", err)
			}
			if result == nil || len(result.Removed) != 0 || result.Deleted || result.CommitURL != "" {
				t.Errorf("result = %+v, want empty no-op result", result)
			}
			if got := len(g.requests()); got != tc.want {
				t.Errorf("writes = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTeardown_DeleteConflictBecomesUpdate(t *testing.T) {
	// The first DELETE races a deploy from PR #7: the re-fetch shows its new
	// pin, so the retry must update the file instead of deleting it.
	g := &teardownGitHub{
		gets:   []fileState{{sha: "blob-1", body: onlyPR42}, {sha: "blob-2", body: pr42AndPR7}},
		writes: []int{http.StatusConflict},
	}
	result, err := runTeardown(t, g)
	if err != nil {
		t.Fatalf("TeardownPreviewWithRetry: %v", err)
	}
	reqs := g.requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %+v, want DELETE then PUT", reqs)
	}
	if reqs[0].method != http.MethodDelete || reqs[0].sha != "blob-1" {
		t.Errorf("first write = %s at %s, want DELETE at blob-1", reqs[0].method, reqs[0].sha)
	}
	if reqs[1].method != http.MethodPut || reqs[1].sha != "blob-2" {
		t.Errorf("retry = %s at %s, want PUT at blob-2", reqs[1].method, reqs[1].sha)
	}
	if !strings.Contains(reqs[1].content, "commitSha: bbb") {
		t.Errorf("retry dropped PR #7's pin:\n%s", reqs[1].content)
	}
	if result.Deleted || result.CommitMessage != "chore(preview): remove backend-1 from feature-x" {
		t.Errorf("result = %+v, want an update", result)
	}
}

func TestTeardown_ConflictsExhausted(t *testing.T) {
	g := &teardownGitHub{
		gets:   []fileState{{sha: "blob-1", body: onlyPR42}},
		writes: slices.Repeat([]int{http.StatusConflict}, gitops.MaxWriteRetries),
	}
	_, err := runTeardown(t, g)
	const want = "teardown previews/feature-x/values.yaml: 3 attempts exhausted"
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v, want %q", err, want)
	}
	if got := len(g.requests()); got != gitops.MaxWriteRetries {
		t.Errorf("writes = %d, want %d", got, gitops.MaxWriteRetries)
	}
}

func TestUpsertPRComment_CreatesWithMarker(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/repos/acme/backend-1/issues/42/comments":
			_, _ = io.WriteString(w, `[]`)
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/backend-1/issues/42/comments":
			var req struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			body = req.Body
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":1}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	if err := newClient(t, srv).UpsertPRComment(context.Background(), "acme", "backend-1", 42, "## gone"); err != nil {
		t.Fatalf("UpsertPRComment: %v", err)
	}
	if want := gitops.PRCommentMarker + "\n## gone"; body != want {
		t.Errorf("comment body = %q, want %q", body, want)
	}
}
