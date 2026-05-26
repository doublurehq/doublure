package gitops_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	gh "github.com/google/go-github/v71/github"

	"github.com/pixelfactoryio/previo/internal/gitops"
	"github.com/pixelfactoryio/previo/internal/preview"
)

// newClient wires a go-github client to a httptest server. The trailing slash
// on BaseURL is required by go-github's URL resolution.
func newClient(t *testing.T, srv *httptest.Server) *gitops.Client {
	t.Helper()
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	c := gh.NewClient(nil)
	c.BaseURL = u
	c.UploadURL = u
	return gitops.NewClient(c, "amine7536", "akpe-gitops")
}

// encodeContent returns a Repository contents response body for a single file.
func encodeContent(path, sha, content string) string {
	payload := map[string]any{
		"name":     path[strings.LastIndex(path, "/")+1:],
		"path":     path,
		"sha":      sha,
		"size":     len(content),
		"type":     "file",
		"content":  base64.StdEncoding.EncodeToString([]byte(content)),
		"encoding": "base64",
	}
	b, _ := json.Marshal(payload)
	return string(b)
}

func sampleInputs() preview.DeployInputs {
	return preview.DeployInputs{
		ServiceName:    "backend-1",
		HeadRef:        "feature/add-pricing",
		CommitSha:      "abc1234567890",
		PRAuthor:       "amine",
		PRURL:          "https://github.com/amine7536/backend-1/pull/42",
		PRNumber:       "42",
		Timestamp:      "2026-05-26T10:00:00Z",
		WorkflowRunURL: "https://github.com/amine7536/backend-1/actions/runs/1",
	}
}

func TestFetchServiceCatalog(t *testing.T) {
	const servicesYAML = `serviceRepos:
  backend-1:
    repo: amine7536/backend-1
  backend-2:
    repo: amine7536/backend-2
  front:
    repo: amine7536/front
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/amine7536/akpe-gitops/contents/services.yaml" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, encodeContent("services.yaml", "sha-svc", servicesYAML))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	catalog, err := c.FetchServiceCatalog(context.Background())
	if err != nil {
		t.Fatalf("FetchServiceCatalog: %v", err)
	}
	want := []string{"backend-1", "backend-2", "front"}
	if len(catalog) != len(want) {
		t.Fatalf("catalog = %v, want %v", catalog, want)
	}
	for i, name := range want {
		if catalog[i] != name {
			t.Errorf("catalog[%d] = %q, want %q", i, catalog[i], name)
		}
	}
}

func TestGetPreview_Success(t *testing.T) {
	const body = `services:
  - name: backend-1
    commitSha: abc1234567890
    metadata:
      pr-author: amine
      pr-url: https://github.com/amine7536/backend-1/pull/42
      pr-number: '42'
      created-at: '2026-05-26T10:00:00Z'
      updated-at: '2026-05-26T10:00:00Z'
      vcs.ref.name: feature/add-pricing
      cicd.pipeline.run.url: https://github.com/amine7536/backend-1/actions/runs/1
  - name: backend-2
  - name: front
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, encodeContent("previews/feature-add-pricing/values.yaml", "blob-sha-123", body))
	}))
	defer srv.Close()

	c := newClient(t, srv)
	vals, sha, err := c.GetPreview(context.Background(), "feature-add-pricing")
	if err != nil {
		t.Fatalf("GetPreview: %v", err)
	}
	if sha != "blob-sha-123" {
		t.Errorf("sha = %q, want blob-sha-123", sha)
	}
	if got := len(vals.Services); got != 3 {
		t.Fatalf("len(Services) = %d, want 3", got)
	}
	if got := vals.Services[0].Metadata.Get(preview.MetaPRNumber); got != "42" {
		t.Errorf("metadata pr-number = %q, want 42", got)
	}
}

func TestGetPreview_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"Not Found"}`)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	_, _, err := c.GetPreview(context.Background(), "missing")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, gitops.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestWritePreview_Create(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if _, hasSHA := req["sha"]; hasSHA {
			t.Errorf("create request must not include sha; got body: %s", body)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"content":{"sha":"new-blob"},"commit":{"html_url":"https://github.com/amine7536/akpe-gitops/commit/abc"}}`)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	vals := preview.BuildPreviewValues("backend-1", "abc1234567890",
		[]string{"backend-1", "backend-2"}, sampleInputs())

	commitURL, err := c.WritePreview(context.Background(), "slug", vals, "", "chore(preview): create slug preview")
	if err != nil {
		t.Fatalf("WritePreview: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Errorf("method = %s, want PUT", gotMethod)
	}
	if commitURL != "https://github.com/amine7536/akpe-gitops/commit/abc" {
		t.Errorf("commitURL = %q", commitURL)
	}
}

func TestWritePreview_Update(t *testing.T) {
	var sawSHA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)
		if s, ok := req["sha"].(string); ok {
			sawSHA = s
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"commit":{"html_url":"https://github.com/x/y/commit/upd"}}`)
	}))
	defer srv.Close()

	c := newClient(t, srv)
	vals := preview.BuildPreviewValues("backend-1", "newsha", []string{"backend-1"}, sampleInputs())
	commitURL, err := c.WritePreview(context.Background(), "slug", vals, "old-blob", "chore(preview): update backend-1 in slug")
	if err != nil {
		t.Fatalf("WritePreview: %v", err)
	}
	if sawSHA != "old-blob" {
		t.Errorf("server saw sha = %q, want old-blob", sawSHA)
	}
	if !strings.HasSuffix(commitURL, "/upd") {
		t.Errorf("commitURL = %q", commitURL)
	}
}

func TestWritePreview_Unauthorized(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"message":"forbidden"}`)
			}))
			defer srv.Close()

			c := newClient(t, srv)
			vals := preview.BuildPreviewValues("backend-1", "sha", []string{"backend-1"}, sampleInputs())
			_, err := c.WritePreview(context.Background(), "slug", vals, "", "msg")
			if !errors.Is(err, gitops.ErrUnauthorized) {
				t.Errorf("expected ErrUnauthorized, got %v", err)
			}
		})
	}
}

func TestWritePreviewWithRetry_ConflictThenSuccess(t *testing.T) {
	var (
		writeAttempts atomic.Int32
		getAttempts   atomic.Int32
	)
	const initialFile = `services:
  - name: backend-1
    commitSha: oldsha
    metadata:
      pr-author: amine
      pr-url: https://github.com/amine7536/backend-1/pull/42
      pr-number: '42'
      created-at: '2026-05-26T09:00:00Z'
      updated-at: '2026-05-26T09:00:00Z'
      vcs.ref.name: feature/add-pricing
      cicd.pipeline.run.url: https://github.com/x/y/actions/runs/0
  - name: backend-2
`
	const refreshedFile = `services:
  - name: backend-1
    commitSha: oldsha
    metadata:
      pr-author: amine
      pr-url: https://github.com/amine7536/backend-1/pull/42
      pr-number: '42'
      created-at: '2026-05-26T09:00:00Z'
      updated-at: '2026-05-26T09:30:00Z'
      vcs.ref.name: feature/add-pricing
      cicd.pipeline.run.url: https://github.com/x/y/actions/runs/0
  - name: backend-2
`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			attempt := getAttempts.Add(1)
			body := initialFile
			sha := "blob-1"
			if attempt > 1 {
				body = refreshedFile
				sha = "blob-2"
			}
			_, _ = io.WriteString(w, encodeContent("previews/slug/values.yaml", sha, body))
		case http.MethodPut:
			attempt := writeAttempts.Add(1)
			if attempt == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"message":"sha does not match"}`)
				return
			}
			body, _ := io.ReadAll(r.Body)
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if got := req["sha"]; got != "blob-2" {
				t.Errorf("retry sha = %v, want blob-2", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"commit":{"html_url":"https://github.com/x/y/commit/winner"}}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv)
	in := sampleInputs()
	in.CommitSha = "newsha"
	in.Timestamp = "2026-05-26T10:00:00Z"

	commitURL, err := c.WritePreviewWithRetry(
		context.Background(), "slug",
		"backend-1", "newsha", in,
		"chore(preview): create slug preview",
		"chore(preview): update backend-1 in slug",
	)
	if err != nil {
		t.Fatalf("WritePreviewWithRetry: %v", err)
	}
	if writeAttempts.Load() != 2 {
		t.Errorf("write attempts = %d, want 2", writeAttempts.Load())
	}
	if getAttempts.Load() != 2 {
		t.Errorf("get attempts = %d, want 2 (initial + post-conflict)", getAttempts.Load())
	}
	if !strings.HasSuffix(commitURL, "/winner") {
		t.Errorf("commitURL = %q", commitURL)
	}
}

func TestFanOutPRComments_UpdateAndCreate(t *testing.T) {
	// Two services with PR URLs:
	//   backend-1: PR #42 → existing marker comment → expect PATCH
	//   backend-2: PR #7  → no existing comment    → expect POST
	var sawEdit, sawCreate atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/issues/42/comments"):
			_, _ = io.WriteString(w, `[{"id": 999, "body": "<!-- akpe-preview -->\n old body"}]`)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/issues/7/comments"):
			_, _ = io.WriteString(w, `[{"id": 111, "body": "unrelated"}]`)
		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/issues/comments/999"):
			sawEdit.Store(true)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "<!-- akpe-preview -->") {
				t.Errorf("PATCH body missing marker: %s", body)
			}
			_, _ = io.WriteString(w, `{"id":999}`)
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/issues/7/comments"):
			sawCreate.Store(true)
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "<!-- akpe-preview -->") {
				t.Errorf("POST body missing marker: %s", body)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":222}`)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	c := newClient(t, srv)

	in1 := sampleInputs()
	in2 := sampleInputs()
	in2.ServiceName = "backend-2"
	in2.PRURL = "https://github.com/amine7536/backend-2/pull/7"
	in2.PRNumber = "7"

	vals := preview.PreviewValues{Services: []preview.ServiceEntry{
		{Name: "backend-1", CommitSha: "abc", Metadata: preview.BuildServiceMetadata(in1)},
		{Name: "backend-2", CommitSha: "def", Metadata: preview.BuildServiceMetadata(in2)},
	}}

	if err := c.FanOutPRComments(context.Background(), vals, "## body"); err != nil {
		t.Fatalf("FanOutPRComments: %v", err)
	}
	if !sawEdit.Load() {
		t.Error("expected PATCH on existing comment for PR #42")
	}
	if !sawCreate.Load() {
		t.Error("expected POST for PR #7")
	}
}
