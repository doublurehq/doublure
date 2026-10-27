package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

// fakeGitops captures calls and returns canned responses. Each field controls
// one of the three methods on gitopsAPI. When servicesYAML is set, the catalog
// is parsed from it instead of being taken from catalog.
type fakeGitops struct {
	catalog      []string
	servicesYAML string
	catalogErr   error
	writeResult  *gitops.WriteResult
	writeErr     error
	fanOutErr    error

	capturedInputs   preview.DeployInputs
	capturedCatalog  []string
	capturedService  string
	capturedSlug     string
	fanOutBodyCalled bool
	capturedSummary  string
}

func (f *fakeGitops) FetchServiceCatalog(_ context.Context) (*gitops.ServiceCatalog, error) {
	if f.catalogErr != nil {
		return nil, f.catalogErr
	}
	if f.servicesYAML != "" {
		return gitops.ParseServices([]byte(f.servicesYAML))
	}
	return &gitops.ServiceCatalog{Services: f.catalog}, nil
}

func (f *fakeGitops) WritePreviewWithRetry(
	_ context.Context,
	slug string,
	catalog []string,
	serviceName, _ string,
	inputs preview.DeployInputs,
	_, _ string,
) (*gitops.WriteResult, error) {
	f.capturedSlug = slug
	f.capturedCatalog = catalog
	f.capturedService = serviceName
	f.capturedInputs = inputs
	return f.writeResult, f.writeErr
}

func (f *fakeGitops) FanOutPRComments(_ context.Context, _ preview.PreviewValues, body string) error {
	f.fanOutBodyCalled = true
	f.capturedSummary = body
	return f.fanOutErr
}

func validBody() string {
	return `{
		"service": "backend-1",
		"head_ref": "feature/add-pricing",
		"commit_sha": "abc1234567890",
		"pr_author": "octocat",
		"pr_url": "https://github.com/acme/backend-1/pull/42",
		"pr_number": "42",
		"workflow_run_url": "https://github.com/acme/backend-1/actions/runs/1"
	}`
}

func newTestServer(t *testing.T, fg *fakeGitops) *Server {
	t.Helper()
	return NewServer(nil, fg, "test-token", 0)
}

func postDeploy(t *testing.T, srv *Server, body, bearer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/deploy", strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDeploy_HappyPath(t *testing.T) {
	fg := &fakeGitops{
		catalog: []string{"backend-1", "backend-2", "front"},
		writeResult: &gitops.WriteResult{
			Values: preview.PreviewValues{Services: []preview.ServiceEntry{
				{Name: "backend-1", CommitSha: "abc1234567890"},
				{Name: "backend-2"},
				{Name: "front"},
			}},
			CommitURL:     "https://github.com/acme/gitops/commit/deadbeef",
			CommitMessage: "chore(preview): update backend-1 in feature-add-pricing",
		},
	}
	rec := postDeploy(t, newTestServer(t, fg), validBody(), "test-token")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var resp DeployResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PreviewSlug != "feature-add-pricing" {
		t.Errorf("PreviewSlug = %q, want feature-add-pricing", resp.PreviewSlug)
	}
	if !strings.HasSuffix(resp.GitOpsCommitURL, "/deadbeef") {
		t.Errorf("GitOpsCommitURL = %q", resp.GitOpsCommitURL)
	}
	if len(resp.Warnings) != 0 {
		t.Errorf("Warnings = %v, want empty", resp.Warnings)
	}

	// Verify the handler stamped a server-side timestamp.
	if fg.capturedInputs.Timestamp == "" {
		t.Error("captured Timestamp is empty; handler must stamp it server-side")
	}
	if fg.capturedSlug != "feature-add-pricing" {
		t.Errorf("captured slug = %q", fg.capturedSlug)
	}
	if !fg.fanOutBodyCalled {
		t.Error("FanOutPRComments was not called")
	}
}

func TestDeploy_MissingField(t *testing.T) {
	fg := &fakeGitops{catalog: []string{"backend-1"}}
	body := `{
		"service": "backend-1",
		"head_ref": "feature/x",
		"commit_sha": "",
		"pr_author": "octocat",
		"pr_url": "https://github.com/acme/backend-1/pull/1",
		"pr_number": "1",
		"workflow_run_url": "https://github.com/acme/backend-1/actions/runs/1"
	}`
	rec := postDeploy(t, newTestServer(t, fg), body, "test-token")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "commit_sha") {
		t.Errorf("body must name missing field, got: %s", rec.Body.String())
	}
}

func TestDeploy_ServiceNotInCatalog(t *testing.T) {
	fg := &fakeGitops{catalog: []string{"backend-2", "front"}}
	rec := postDeploy(t, newTestServer(t, fg), validBody(), "test-token")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "backend-1") {
		t.Errorf("body must name the missing service, got: %s", rec.Body.String())
	}
}

func TestDeploy_WriteUnauthorized(t *testing.T) {
	fg := &fakeGitops{
		catalog:  []string{"backend-1"},
		writeErr: fmt.Errorf("%w: token revoked", gitops.ErrUnauthorized),
	}
	rec := postDeploy(t, newTestServer(t, fg), validBody(), "test-token")

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestDeploy_FanOutPartialFailureSurfacesAsWarnings(t *testing.T) {
	joined := errors.Join(
		errors.New("service backend-2 (acme/backend-2#7): rate limited"),
		errors.New("service front (acme/front#11): not found"),
	)
	fg := &fakeGitops{
		catalog: []string{"backend-1", "backend-2", "front"},
		writeResult: &gitops.WriteResult{
			Values:        preview.PreviewValues{Services: []preview.ServiceEntry{{Name: "backend-1"}}},
			CommitURL:     "https://github.com/acme/gitops/commit/abc",
			CommitMessage: "chore(preview): update backend-1 in feature-add-pricing",
		},
		fanOutErr: joined,
	}
	rec := postDeploy(t, newTestServer(t, fg), validBody(), "test-token")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (write succeeded)", rec.Code)
	}
	var resp DeployResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Warnings) != 2 {
		t.Errorf("Warnings = %v, want 2 entries", resp.Warnings)
	}
}

func TestDeploy_MissingBearer(t *testing.T) {
	fg := &fakeGitops{catalog: []string{"backend-1"}}
	rec := postDeploy(t, newTestServer(t, fg), validBody(), "")

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if fg.capturedSlug != "" {
		t.Error("gitops handler should not be reached when bearer is missing")
	}
}

func TestDeploy_InvalidJSON(t *testing.T) {
	fg := &fakeGitops{}
	rec := postDeploy(t, newTestServer(t, fg), "{not json", "test-token")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestDeploy_UnknownField(t *testing.T) {
	// DisallowUnknownFields guards against drift between client and server.
	fg := &fakeGitops{catalog: []string{"backend-1"}}
	body := `{
		"service": "backend-1",
		"head_ref": "feature/x",
		"commit_sha": "abc",
		"pr_author": "octocat",
		"pr_url": "https://github.com/acme/backend-1/pull/1",
		"pr_number": "1",
		"workflow_run_url": "https://github.com/acme/backend-1/actions/runs/1",
		"extra_field": "should be rejected"
	}`
	rec := postDeploy(t, newTestServer(t, fg), body, "test-token")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for unknown field", rec.Code)
	}
}

const servicesWithPreview = `serviceRepos:
  backend-1: https://github.com/acme/backend-1.git
  backend-2: https://github.com/acme/backend-2.git
  front: https://github.com/acme/front.git
preview:
  domain: 127.0.0.1.nip.io
  tls: false
  expose:
    front: {}
    backend-1:
      path: /docs
`

func previewWriteResult() *gitops.WriteResult {
	return &gitops.WriteResult{
		Values: preview.PreviewValues{Services: []preview.ServiceEntry{
			{Name: "backend-1", CommitSha: "abc1234567890"},
			{Name: "backend-2"},
			{Name: "front"},
		}},
		CommitURL:     "https://github.com/acme/gitops/commit/deadbeef",
		CommitMessage: "chore(preview): update backend-1 in feature-pricing",
	}
}

func previewBody() string {
	return strings.Replace(validBody(), "feature/add-pricing", "feature/pricing", 1)
}

func TestDeploy_MalformedPreviewFails(t *testing.T) {
	yml := strings.Replace(servicesWithPreview, "path: /docs", "path: docs", 1)
	fg := &fakeGitops{servicesYAML: yml, writeResult: previewWriteResult()}
	rec := postDeploy(t, newTestServer(t, fg), previewBody(), "test-token")

	if rec.Code < http.StatusBadRequest {
		t.Fatalf("status = %d, want an error (body: %s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "preview.expose.backend-1.path") {
		t.Errorf("body must name the offending key, got: %s", rec.Body.String())
	}
	if fg.capturedSlug != "" || fg.fanOutBodyCalled {
		t.Error("a malformed preview block must fail before any write or comment")
	}
}
