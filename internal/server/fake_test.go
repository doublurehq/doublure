package server

import (
	"context"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

// fakeGitops captures calls and returns canned responses. Each field controls
// one of the methods on gitopsAPI. When servicesYAML is set, the catalog is
// parsed from it instead of being taken from catalog. When started is non-nil,
// FetchServiceCatalog sends "catalog" and TeardownPreviewWithRetry sends
// "teardown" on entry; the test buffers the channel.
type fakeGitops struct {
	catalog      []string
	servicesYAML string
	catalogErr   error
	writeResult  *gitops.WriteResult
	writeErr     error
	fanOutErr    error

	// teardownResult nil means TeardownPreviewWithRetry returns an empty
	// result. A non-nil teardownGate blocks the call until it is closed.
	teardownResult *gitops.TeardownResult
	teardownErr    error
	teardownGate   chan struct{}
	teardownPanic  bool

	// pr is what GetPullRequest returns, unless prErr is set.
	pr      *gitops.PullRequest
	prErr   error
	prCalls int

	// previews is what GetPreview reads; an unknown slug is ErrNotFound.
	previews map[string]*preview.PreviewValues

	started chan string

	capturedInputs   preview.DeployInputs
	capturedCatalog  []string
	capturedURLs     preview.URLConfig
	capturedService  string
	capturedSlug     string
	fanOutBodyCalled bool
	capturedSummary  string

	teardownSlug   string
	teardownOwner  string
	teardownRepo   string
	teardownNumber int
	upserts        []struct {
		owner, repo string
		number      int
		body        string
	}
}

func (f *fakeGitops) FetchServiceCatalog(_ context.Context) (*gitops.ServiceCatalog, error) {
	if f.started != nil {
		f.started <- "catalog"
	}
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
	urls preview.URLConfig,
	serviceName, _ string,
	inputs preview.DeployInputs,
	_, _ string,
) (*gitops.WriteResult, error) {
	f.capturedSlug = slug
	f.capturedCatalog = catalog
	f.capturedURLs = urls
	f.capturedService = serviceName
	f.capturedInputs = inputs
	if f.writeResult == nil {
		return nil, f.writeErr
	}
	result := *f.writeResult
	result.Values = preview.ApplyHosts(f.writeResult.Values, slug, urls)
	return &result, f.writeErr
}

func (f *fakeGitops) FanOutPRComments(_ context.Context, _ preview.PreviewValues, body string) error {
	f.fanOutBodyCalled = true
	f.capturedSummary = body
	return f.fanOutErr
}

func (f *fakeGitops) TeardownPreviewWithRetry(
	_ context.Context,
	slug, owner, repo string,
	prNumber int,
) (*gitops.TeardownResult, error) {
	if f.started != nil {
		f.started <- "teardown"
	}
	f.teardownSlug, f.teardownOwner, f.teardownRepo, f.teardownNumber = slug, owner, repo, prNumber
	if f.teardownGate != nil {
		<-f.teardownGate
	}
	if f.teardownPanic {
		panic("fake teardown panic")
	}
	if f.teardownErr != nil {
		return nil, f.teardownErr
	}
	if f.teardownResult == nil {
		return &gitops.TeardownResult{}, nil
	}
	return f.teardownResult, nil
}

func (f *fakeGitops) UpsertPRComment(_ context.Context, owner, repo string, prNumber int, body string) error {
	f.upserts = append(f.upserts, struct {
		owner, repo string
		number      int
		body        string
	}{owner, repo, prNumber, body})
	return nil
}

func (f *fakeGitops) GetPullRequest(_ context.Context, _, _ string, _ int) (*gitops.PullRequest, error) {
	f.prCalls++
	if f.prErr != nil {
		return nil, f.prErr
	}
	return f.pr, nil
}

func (f *fakeGitops) GetPreview(_ context.Context, slug string) (*preview.PreviewValues, string, error) {
	vals, ok := f.previews[slug]
	if !ok {
		return nil, "", gitops.ErrNotFound
	}
	return vals, "blob-" + slug, nil
}
