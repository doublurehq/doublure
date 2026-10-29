package gitops_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	gh "github.com/google/go-github/v71/github"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/metrics"
)

func TestClientMetrics_TimesRetryOperations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/broken/"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"message":"boom"}`)
		case r.Method == http.MethodGet:
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"Not Found"}`)
		case r.Method == http.MethodPut:
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"commit":{"html_url":"https://github.com/x/y/commit/c0ffee"}}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)
	u, parseErr := url.Parse(srv.URL + "/")
	if parseErr != nil {
		t.Fatal(parseErr)
	}
	ghc := gh.NewClient(nil)
	ghc.BaseURL = u
	m := metrics.New()
	c := gitops.NewClient(ghc, "acme", "gitops", gitops.WithMetrics(m))

	ctx := context.Background()
	if _, err := c.WritePreviewWithRetry(ctx, "fresh", []string{"backend-1"}, hostURLs(),
		"backend-1", "abc", sampleInputs(), "create", "update"); err != nil {
		t.Fatalf("WritePreviewWithRetry: %v", err)
	}
	if _, err := c.TeardownPreviewWithRetry(ctx, "broken", "acme", "backend-1", 42); err == nil {
		t.Fatal("TeardownPreviewWithRetry: want an error")
	}
	if _, err := c.TeardownPreviewWithRetry(ctx, "gone", "acme", "backend-1", 42); err != nil {
		t.Fatalf("TeardownPreviewWithRetry: %v", err)
	}

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`doublure_gitops_operation_duration_seconds_count{op="write",result="ok"} 1`,
		`doublure_gitops_operation_duration_seconds_count{op="teardown",result="error"} 1`,
		`doublure_gitops_operation_duration_seconds_count{op="teardown",result="ok"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %q", want)
		}
	}
}
