package lifecycle_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/preview"
)

const reconcileServices = `serviceRepos:
  api: https://github.com/acme/monorepo.git
  worker: https://github.com/acme/monorepo
  front: https://github.com/acme/front.git
`

const fetchedSHA = "fe7c4ed0000000000000000000000000000000ff"

type deployCall struct {
	slug, service, commitSha string
	catalog                  []string
	inputs                   preview.DeployInputs
}

// reconcileFake is an in-memory ReconcileAPI. The catalog is parsed from
// reconcileServices unless catalogErr is set. teardownBySlug, when it has the
// slug, overrides teardown. previews is what GetPreview reads, unless
// previewErr is set; an unknown slug is ErrNotFound. calls logs, in order,
// "teardown:<slug>", "get:<slug>", "write:<slug>", "upsert" and "fanout".
type reconcileFake struct {
	catalogErr     error
	pr             *gitops.PullRequest
	prErr          error
	writeErr       map[string]error
	fanErr         error
	upsertErr      error
	teardown       *gitops.TeardownResult
	teardownBySlug map[string]*gitops.TeardownResult
	previews       map[string]*preview.PreviewValues
	previewErr     error

	prReads   int
	writes    []deployCall
	fanOuts   []string
	teardowns []teardownCall
	upserts   []string
	calls     []string
}

func (f *reconcileFake) GetPreview(_ context.Context, slug string) (*preview.PreviewValues, string, error) {
	f.calls = append(f.calls, "get:"+slug)
	if f.previewErr != nil {
		return nil, "", f.previewErr
	}
	vals, ok := f.previews[slug]
	if !ok {
		return nil, "", gitops.ErrNotFound
	}
	return vals, "blob-" + slug, nil
}

func (f *reconcileFake) FetchServiceCatalog(context.Context) (*gitops.ServiceCatalog, error) {
	if f.catalogErr != nil {
		return nil, f.catalogErr
	}
	return gitops.ParseServices([]byte(reconcileServices))
}

func (f *reconcileFake) GetPullRequest(context.Context, string, string, int) (*gitops.PullRequest, error) {
	f.prReads++
	if f.prErr != nil {
		return nil, f.prErr
	}
	return f.pr, nil
}

func (f *reconcileFake) WritePreviewWithRetry(
	_ context.Context,
	slug string,
	catalog []string,
	serviceName, commitSha string,
	inputs preview.DeployInputs,
	_, _ string,
) (*gitops.WriteResult, error) {
	if err := f.writeErr[serviceName]; err != nil {
		return nil, err
	}
	f.calls = append(f.calls, "write:"+slug)
	f.writes = append(f.writes, deployCall{
		slug: slug, service: serviceName, commitSha: commitSha, catalog: catalog, inputs: inputs,
	})
	return &gitops.WriteResult{
		Values: preview.PreviewValues{
			Services: []preview.ServiceEntry{{Name: serviceName, CommitSha: commitSha}},
		},
		CommitURL:     sweepCommitURL,
		CommitMessage: "chore(preview): update " + serviceName + " in " + slug,
	}, nil
}

func (f *reconcileFake) FanOutPRComments(_ context.Context, _ preview.PreviewValues, body string) error {
	f.calls = append(f.calls, "fanout")
	f.fanOuts = append(f.fanOuts, body)
	return f.fanErr
}

func (f *reconcileFake) TeardownPreviewWithRetry(
	_ context.Context,
	slug, owner, repo string,
	prNumber int,
) (*gitops.TeardownResult, error) {
	f.calls = append(f.calls, "teardown:"+slug)
	f.teardowns = append(f.teardowns, teardownCall{slug: slug, owner: owner, repo: repo, number: prNumber})
	if res, ok := f.teardownBySlug[slug]; ok {
		return res, nil
	}
	if f.teardown == nil {
		return &gitops.TeardownResult{}, nil
	}
	return f.teardown, nil
}

func (f *reconcileFake) UpsertPRComment(_ context.Context, _, _ string, _ int, body string) error {
	f.calls = append(f.calls, "upsert")
	f.upserts = append(f.upserts, body)
	return f.upsertErr
}

// event builds a reconcile event for acme/<repo>#42 whose payload carries
// labels.
func event(action, repo, label string, labels ...string) lifecycle.Event {
	return lifecycle.Event{
		Target: lifecycle.Target{Owner: "acme", Repo: repo, FullName: "acme/" + repo, Number: 42},
		Action: action,
		Label:  label,
		Labels: labels,
	}
}

// fetchedPR is the PR acme/<repo>#42 as read from GitHub.
func fetchedPR(repo string, open bool, labels ...string) *gitops.PullRequest {
	return &gitops.PullRequest{
		Open:         open,
		HeadRef:      "feature/pricing",
		HeadSHA:      fetchedSHA,
		Author:       "octocat",
		URL:          "https://github.com/acme/" + repo + "/pull/42",
		Labels:       labels,
		FullName:     "acme/" + repo,
		HeadFullName: "acme/" + repo,
	}
}

func services(writes []deployCall) []string {
	out := make([]string, 0, len(writes))
	for _, w := range writes {
		out = append(out, w.service)
	}
	return out
}

func TestReconcile_LabeledDeploysFetchedHead(t *testing.T) {
	capture := captureLogs(t)
	f := &reconcileFake{pr: fetchedPR("front", true, "bug", "Preview")}
	ev := event("labeled", "front", "preview", "preview")
	lifecycle.ReconcilePR(context.Background(), f, ev)

	if len(f.writes) != 1 {
		t.Fatalf("writes = %+v, want one", f.writes)
	}
	w := f.writes[0]
	if w.slug != "feature-pricing" || w.service != "front" || w.commitSha != fetchedSHA {
		t.Errorf("write = (%q, %q, %q), want (feature-pricing, front, %s)", w.slug, w.service, w.commitSha, fetchedSHA)
	}
	if want := []string{"api", "worker", "front"}; !slices.Equal(w.catalog, want) {
		t.Errorf("catalog = %v, want %v", w.catalog, want)
	}
	in := w.inputs
	if in.HeadRef != "feature/pricing" || in.CommitSha != fetchedSHA || in.PRAuthor != "octocat" ||
		in.PRURL != "https://github.com/acme/front/pull/42" || in.PRNumber != "42" || in.WorkflowRunURL != "" ||
		in.Timestamp == "" {
		t.Errorf("inputs = %+v", in)
	}
	if len(f.fanOuts) != 1 || len(f.teardowns) != 0 {
		t.Errorf("fanOuts = %d, teardowns = %v; want one fan-out, no teardown", len(f.fanOuts), f.teardowns)
	}
	logs := capture.find(slog.LevelInfo, "preview deploy")
	if len(logs) != 1 || logs[0]["slug"].String() != "feature-pricing" || logs[0]["service"].String() != "front" ||
		logs[0]["repo"].String() != "acme/front" || logs[0]["pr_number"].Int64() != 42 ||
		logs[0]["commit_sha"].String() != fetchedSHA || logs[0]["gitops_commit_url"].String() != sweepCommitURL {
		t.Errorf("preview deploy logs = %v", logs)
	}
}

func TestReconcile_LabeledButFetchedUnlabeledTearsDown(t *testing.T) {
	captureLogs(t)
	f := &reconcileFake{
		pr: fetchedPR("front", true, "bug"),
		teardown: &gitops.TeardownResult{
			Removed:       []string{"front"},
			Deleted:       true,
			CommitURL:     sweepCommitURL,
			CommitMessage: "chore(preview): delete feature-pricing preview",
		},
	}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "preview", "preview"))

	if len(f.writes) != 0 {
		t.Errorf("writes = %+v, want none", f.writes)
	}
	want := []teardownCall{{slug: "feature-pricing", owner: "acme", repo: "front", number: 42}}
	if !slices.Equal(f.teardowns, want) {
		t.Errorf("teardowns = %+v, want %+v", f.teardowns, want)
	}
	wantBody := "## 🧹 Preview: `feature-pricing`\n\n" +
		"Preview deleted (label `preview` removed): no other PR is pinned.\n\n" +
		"---\n" +
		"🔗 [chore(preview): delete feature-pricing preview](" + sweepCommitURL + ")"
	if len(f.upserts) != 1 || f.upserts[0] != wantBody {
		t.Errorf("upserts = %q, want one with body:\n%s", f.upserts, wantBody)
	}
}

func TestReconcile_UnlabeledClosedTearsDownAsClosed(t *testing.T) {
	captureLogs(t)
	f := &reconcileFake{
		pr: fetchedPR("front", false),
		teardown: &gitops.TeardownResult{
			Removed:       []string{"front"},
			Values:        preview.PreviewValues{Services: []preview.ServiceEntry{{Name: "front"}}},
			CommitURL:     sweepCommitURL,
			CommitMessage: "chore(preview): remove front from feature-pricing",
		},
	}
	lifecycle.ReconcilePR(context.Background(), f, event("unlabeled", "front", "preview"))

	if len(f.teardowns) != 1 {
		t.Fatalf("teardowns = %+v, want one", f.teardowns)
	}
	wantBody := "## 🧹 Preview: `feature-pricing`\n\n" +
		"Removed `front` from this preview (PR closed).\n\n" +
		"---\n" +
		"🔗 [chore(preview): remove front from feature-pricing](" + sweepCommitURL + ")"
	if len(f.upserts) != 1 || f.upserts[0] != wantBody {
		t.Errorf("upserts = %q, want one with body:\n%s", f.upserts, wantBody)
	}
}

func TestReconcile_ClosedTearsDownOutsideCatalogAndForks(t *testing.T) {
	cases := []struct {
		name string
		repo string
		fork bool
	}{
		{name: "repo not in catalog", repo: "docs"},
		{name: "fork pr", repo: "front", fork: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			f := &reconcileFake{pr: fetchedPR(tc.repo, false)}
			if tc.fork {
				f.pr.HeadFullName = "octocat/" + tc.repo
			}
			lifecycle.ReconcilePR(context.Background(), f, event("closed", tc.repo, ""))

			want := teardownCall{slug: "feature-pricing", owner: "acme", repo: tc.repo, number: 42}
			if len(f.teardowns) != 1 || f.teardowns[0] != want {
				t.Errorf("teardowns = %+v, want [%+v]", f.teardowns, want)
			}
		})
	}
}

func TestReconcile_TwoServicesInCatalogOrder(t *testing.T) {
	captureLogs(t)
	f := &reconcileFake{pr: fetchedPR("monorepo", true, "preview")}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "monorepo", "preview", "preview"))

	if got, want := services(f.writes), []string{"api", "worker"}; !slices.Equal(got, want) {
		t.Errorf("deployed = %v, want %v", got, want)
	}
	if len(f.fanOuts) != 2 {
		t.Errorf("fanOuts = %d, want 2", len(f.fanOuts))
	}
}

func TestReconcile_SynchronizeOnLabeledPRDeploys(t *testing.T) {
	captureLogs(t)
	f := &reconcileFake{pr: fetchedPR("front", true, "preview")}
	lifecycle.ReconcilePR(context.Background(), f, event("synchronize", "front", "", "preview"))

	if got := services(f.writes); !slices.Equal(got, []string{"front"}) {
		t.Errorf("deployed = %v, want [front]", got)
	}
}

func TestReconcile_PayloadGuards(t *testing.T) {
	cases := []struct {
		name   string
		ev     lifecycle.Event
		reason string
	}{
		{"opened without the label", event("opened", "front", "", "bug"), "not labeled"},
		{"reopened without the label", event("reopened", "front", ""), "not labeled"},
		{"synchronize without the label", event("synchronize", "front", ""), "not labeled"},
		{"labeled with another label", event("labeled", "front", "bug", "bug", "preview"), "other label"},
		{"unlabeled another label", event("unlabeled", "front", "bug", "preview"), "other label"},
		{"repo not in catalog", event("labeled", "unknown", "preview", "preview"), "repo not in catalog"},
		{"labeled with an empty named label", event("labeled", "front", "preview:", "preview:"), "other label"},
		{"opened with an empty named label", event("opened", "front", "", "preview:--"), "not labeled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := captureLogs(t)
			f := &reconcileFake{pr: fetchedPR(tc.ev.Repo, true, "preview")}
			lifecycle.ReconcilePR(context.Background(), f, tc.ev)

			if f.prReads != 0 || len(f.writes) != 0 || len(f.teardowns) != 0 {
				t.Errorf("prReads = %d, writes = %v, teardowns = %v; want nothing", f.prReads, f.writes, f.teardowns)
			}
			logs := capture.find(slog.LevelInfo, "preview reconcile skipped")
			if len(logs) != 1 || logs[0]["reason"].String() != tc.reason ||
				logs[0]["repo"].String() != tc.ev.FullName || logs[0]["pr_number"].Int64() != 42 ||
				logs[0]["action"].String() != tc.ev.Action {
				t.Errorf("skip logs = %v, want reason %q", logs, tc.reason)
			}
		})
	}
}

func TestReconcile_LabelMatchIsCaseInsensitive(t *testing.T) {
	captureLogs(t)
	f := &reconcileFake{pr: fetchedPR("front", true, "PREVIEW")}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "Preview", "Preview"))

	if len(f.writes) != 1 {
		t.Errorf("writes = %+v, want one", f.writes)
	}
}

func TestReconcile_ForkPRSkipped(t *testing.T) {
	capture := captureLogs(t)
	f := &reconcileFake{pr: fetchedPR("front", true, "preview")}
	f.pr.HeadFullName = "octocat/front"
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "preview", "preview"))

	if len(f.writes) != 0 || len(f.teardowns) != 0 {
		t.Errorf("writes = %v, teardowns = %v; want nothing", f.writes, f.teardowns)
	}
	logs := capture.find(slog.LevelInfo, "preview reconcile skipped")
	if len(logs) != 1 || logs[0]["reason"].String() != "fork pr" {
		t.Errorf("skip logs = %v, want reason fork pr", logs)
	}
}

func TestReconcile_FailedServiceDoesNotStopTheNext(t *testing.T) {
	capture := captureLogs(t)
	f := &reconcileFake{
		pr:       fetchedPR("monorepo", true, "preview"),
		writeErr: map[string]error{"api": errors.New("write boom")},
		fanErr:   errors.New("comment boom"),
	}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "monorepo", "preview", "preview"))

	if got := services(f.writes); !slices.Equal(got, []string{"worker"}) {
		t.Errorf("deployed = %v, want [worker]", got)
	}
	failed := capture.find(slog.LevelError, "preview deploy failed")
	if len(failed) != 1 || failed[0]["service"].String() != "api" || failed[0]["err"].String() != "write boom" ||
		failed[0]["slug"].String() != "feature-pricing" || failed[0]["repo"].String() != "acme/monorepo" ||
		failed[0]["pr_number"].Int64() != 42 || failed[0]["commit_sha"].String() != fetchedSHA {
		t.Errorf("preview deploy failed logs = %v", failed)
	}
	comment := capture.find(slog.LevelWarn, "preview deploy comment failed")
	if len(comment) != 1 || comment[0]["service"].String() != "worker" || comment[0]["err"].String() != "comment boom" {
		t.Errorf("preview deploy comment failed logs = %v", comment)
	}
}

func TestReconcile_ExpiredContextFailsEveryService(t *testing.T) {
	capture := captureLogs(t)
	f := &reconcileFake{pr: fetchedPR("monorepo", true, "preview")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	lifecycle.ReconcilePR(ctx, f, event("labeled", "monorepo", "preview", "preview"))

	if len(f.writes) != 0 {
		t.Errorf("writes = %+v, want none", f.writes)
	}
	failed := capture.find(slog.LevelError, "preview deploy failed")
	if len(failed) != 2 || failed[0]["err"].String() != context.Canceled.Error() {
		t.Errorf("preview deploy failed logs = %v, want two with %q", failed, context.Canceled)
	}
}

func TestReconcile_Errors(t *testing.T) {
	cases := []struct {
		name string
		f    *reconcileFake
	}{
		{name: "catalog", f: &reconcileFake{catalogErr: errors.New("boom")}},
		{name: "pull request", f: &reconcileFake{prErr: errors.New("boom")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := captureLogs(t)
			lifecycle.ReconcilePR(context.Background(), tc.f, event("labeled", "front", "preview", "preview"))

			if len(tc.f.writes) != 0 || len(tc.f.teardowns) != 0 || len(tc.f.upserts) != 0 {
				t.Errorf("writes = %v, teardowns = %v, upserts = %v; want nothing",
					tc.f.writes, tc.f.teardowns, tc.f.upserts)
			}
			logs := capture.find(slog.LevelError, "preview reconcile failed")
			if len(logs) != 1 || logs[0]["err"].String() != "boom" || logs[0]["repo"].String() != "acme/front" ||
				logs[0]["pr_number"].Int64() != 42 {
				t.Errorf("preview reconcile failed logs = %v", logs)
			}
		})
	}
}
