package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	gh "github.com/google/go-github/v71/github"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/metrics"
)

const (
	payloadSHA = "0000000000000000000000000000000000000001"
	fetchedSHA = "fe7c4ed0000000000000000000000000000000ff"
)

// quiet is how long a test waits to conclude that nothing arrives.
const quiet = 100 * time.Millisecond

// labelEvent is prEvent with the given added or removed label, payload labels
// and a payload head SHA that differs from the fetched one.
func labelEvent(action, label string, labels ...string) *gh.PullRequestEvent {
	e := prEvent(action, false)
	if label != "" {
		e.Label = &gh.Label{Name: gh.Ptr(label)}
	}
	for _, l := range labels {
		e.PullRequest.Labels = append(e.PullRequest.Labels, &gh.Label{Name: gh.Ptr(l)})
	}
	e.PullRequest.Head.SHA = gh.Ptr(payloadSHA)
	return e
}

// fetchedPR is acme/backend-1#42 as read from GitHub.
func fetchedPR(open bool, labels ...string) *gitops.PullRequest {
	return &gitops.PullRequest{
		Open:         open,
		HeadRef:      "feature/pricing",
		HeadSHA:      fetchedSHA,
		Author:       "octocat",
		URL:          "https://github.com/acme/backend-1/pull/42",
		Labels:       labels,
		FullName:     "acme/backend-1",
		HeadFullName: "acme/backend-1",
	}
}

// reconcileFake is a fakeGitops whose catalog comes from servicesWithPreview
// and whose fetched PR is open and labeled.
func reconcileFake() *fakeGitops {
	return &fakeGitops{
		servicesYAML: servicesWithPreview,
		writeResult:  previewWriteResult(),
		pr:           fetchedPR(true, "preview"),
		started:      make(chan string, 8),
	}
}

// deliver hands e to the webhook handler and waits for the background jobs.
func deliver(t *testing.T, s *Server, e *gh.PullRequestEvent) {
	t.Helper()
	s.handlePullRequest(context.Background(), e)
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
}

// drain returns what was sent on started so far.
func drain(started chan string) []string {
	var out []string
	for {
		select {
		case s := <-started:
			out = append(out, s)
		default:
			return out
		}
	}
}

func TestReconcile_ClosedButReopenedAndLabeledDeploys(t *testing.T) {
	fg := reconcileFake()
	s, _ := newTeardownServer(t, fg)
	deliver(t, s, prEvent("closed", false))

	if fg.prCalls != 1 {
		t.Errorf("GetPullRequest calls = %d, want 1", fg.prCalls)
	}
	if fg.capturedSlug != "feature-pricing" || fg.capturedInputs.CommitSha != fetchedSHA {
		t.Errorf("deployed to %q at %q, want feature-pricing at %q",
			fg.capturedSlug, fg.capturedInputs.CommitSha, fetchedSHA)
	}
	if fg.teardownSlug != "" {
		t.Errorf("teardown of %q, want none", fg.teardownSlug)
	}
}

func TestReconcile_ClosedFallsBackToPayloadTeardown(t *testing.T) {
	const msg = "chore(preview): delete feature-add-pricing preview"
	cases := []struct {
		name        string
		set         func(*fakeGitops)
		wantPRCalls int
	}{
		{name: "pull request read fails", set: func(f *fakeGitops) { f.prErr = errors.New("boom") }, wantPRCalls: 1},
		{name: "catalog read fails", set: func(f *fakeGitops) { f.catalogErr = errors.New("boom") }, wantPRCalls: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fg := reconcileFake()
			tc.set(fg)
			fg.teardownResult = &gitops.TeardownResult{
				Removed: []string{"backend-1"}, Deleted: true, CommitURL: teardownCommitURL, CommitMessage: msg,
			}
			s, capture := newTeardownServer(t, fg)
			deliver(t, s, prEvent("closed", true))

			if fg.prCalls != tc.wantPRCalls {
				t.Errorf("GetPullRequest calls = %d, want %d", fg.prCalls, tc.wantPRCalls)
			}
			assertPayloadTeardown(t, fg, capture, msg)
		})
	}
}

// assertPayloadTeardown checks that fg tore down the closed event's payload
// head branch with the PR closed reason, deployed nothing, and that the
// fallback was logged.
func assertPayloadTeardown(t *testing.T, fg *fakeGitops, capture *LogCapture, msg string) {
	t.Helper()
	if fg.teardownSlug != teardownSlug || fg.teardownOwner != "acme" ||
		fg.teardownRepo != "backend-1" || fg.teardownNumber != 42 {
		t.Errorf("teardown(%q, %q, %q, %d), want (%q, acme, backend-1, 42)",
			fg.teardownSlug, fg.teardownOwner, fg.teardownRepo, fg.teardownNumber, teardownSlug)
	}
	wantComment := "## 🧹 Preview: `feature-add-pricing`\n\n" +
		"Preview deleted (PR closed): no other PR is pinned.\n\n" +
		"---\n" +
		"🔗 [" + msg + "](" + teardownCommitURL + ")"
	if len(fg.upserts) != 1 || fg.upserts[0].body != wantComment {
		t.Errorf("upserts = %+v, want one with body:\n%s", fg.upserts, wantComment)
	}
	if fg.capturedSlug != "" {
		t.Errorf("deploy to %q, want none", fg.capturedSlug)
	}
	r, attrs := teardownRecord(t, capture, "preview reconcile fell back to payload teardown")
	if r.Level != slog.LevelWarn {
		t.Errorf("level = %v, want WARN", r.Level)
	}
	if attrs["repo"].String() != "acme/backend-1" || attrs["pr_number"].Int64() != 42 ||
		attrs["err"].String() != "boom" {
		t.Errorf("attrs = %v", attrs)
	}
}

func TestReconcile_ActionsStartReconcile(t *testing.T) {
	for _, action := range []string{"opened", "reopened", "synchronize", "labeled", "unlabeled"} {
		t.Run(action, func(t *testing.T) {
			fg := reconcileFake()
			s, _ := newTeardownServer(t, fg)
			deliver(t, s, labelEvent(action, "preview", "preview"))

			if got := drain(fg.started); len(got) == 0 || got[0] != "catalog" {
				t.Errorf("started = %v, want a reconcile (catalog first)", got)
			}
		})
	}
}

func TestReconcile_OtherActionsStartNothing(t *testing.T) {
	for _, action := range []string{"edited", "assigned"} {
		t.Run(action, func(t *testing.T) {
			fg := reconcileFake()
			s, _ := newTeardownServer(t, fg)
			deliver(t, s, labelEvent(action, "preview", "preview"))

			if got := drain(fg.started); len(got) != 0 {
				t.Errorf("started = %v, want nothing", got)
			}
		})
	}
}

func TestReconcile_LabeledDeploysFetchedHead(t *testing.T) {
	fg := reconcileFake()
	s, _ := newTeardownServer(t, fg)
	deliver(t, s, labelEvent("labeled", "preview", "preview"))

	if fg.prCalls != 1 {
		t.Errorf("GetPullRequest calls = %d, want 1", fg.prCalls)
	}
	if fg.capturedService != "backend-1" || fg.capturedSlug != "feature-pricing" {
		t.Errorf("deployed %q to %q, want backend-1 to feature-pricing", fg.capturedService, fg.capturedSlug)
	}
	if got := fg.capturedInputs.CommitSha; got != fetchedSHA {
		t.Errorf("CommitSha = %q, want the fetched head %q (payload had %q)", got, fetchedSHA, payloadSHA)
	}
	if got := fg.capturedInputs.WorkflowRunURL; got != "" {
		t.Errorf("WorkflowRunURL = %q, want empty", got)
	}
	if !fg.fanOutBodyCalled {
		t.Error("FanOutPRComments was not called")
	}
}

func TestReconcile_UnlabeledTearsDownWithReason(t *testing.T) {
	const msg = "chore(preview): delete feature-pricing preview"
	fg := reconcileFake()
	fg.pr = fetchedPR(true, "bug")
	fg.teardownResult = &gitops.TeardownResult{
		Removed: []string{"backend-1"}, Deleted: true, CommitURL: teardownCommitURL, CommitMessage: msg,
	}
	s, _ := newTeardownServer(t, fg)
	deliver(t, s, labelEvent("unlabeled", "preview", "bug"))

	if fg.teardownSlug != "feature-pricing" || fg.teardownOwner != "acme" ||
		fg.teardownRepo != "backend-1" || fg.teardownNumber != 42 {
		t.Errorf("teardown(%q, %q, %q, %d), want (feature-pricing, acme, backend-1, 42)",
			fg.teardownSlug, fg.teardownOwner, fg.teardownRepo, fg.teardownNumber)
	}
	wantComment := "## 🧹 Preview: `feature-pricing`\n\n" +
		"Preview deleted (label `preview` removed): no other PR is pinned.\n\n" +
		"---\n" +
		"🔗 [" + msg + "](" + teardownCommitURL + ")"
	if len(fg.upserts) != 1 || fg.upserts[0].body != wantComment {
		t.Errorf("upserts = %+v, want one with body:\n%s", fg.upserts, wantComment)
	}
	if fg.capturedSlug != "" {
		t.Error("an unlabeled PR must not deploy")
	}
}

func TestReconcile_GuardSkipsPRRead(t *testing.T) {
	cases := []struct {
		name string
		e    *gh.PullRequestEvent
	}{
		{name: "opened without the label", e: labelEvent("opened", "", "bug")},
		{name: "synchronize without the label", e: labelEvent("synchronize", "")},
		{name: "labeled with another label", e: labelEvent("labeled", "bug", "bug", "preview")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fg := reconcileFake()
			s, _ := newTeardownServer(t, fg)
			deliver(t, s, tc.e)

			if fg.prCalls != 0 || fg.capturedSlug != "" || fg.teardownSlug != "" {
				t.Errorf("prCalls = %d, deploy = %q, teardown = %q; want nothing",
					fg.prCalls, fg.capturedSlug, fg.teardownSlug)
			}
		})
	}
}

func TestReconcile_MissingFields(t *testing.T) {
	fg := reconcileFake()
	s, capture := newTeardownServer(t, fg)
	e := labelEvent("labeled", "preview", "preview")
	e.Repo = nil
	deliver(t, s, e)

	if got := drain(fg.started); len(got) != 0 {
		t.Errorf("started = %v, want nothing", got)
	}
	r, attrs := teardownRecord(t, capture, "preview reconcile skipped")
	if r.Level != slog.LevelInfo {
		t.Errorf("level = %v, want INFO", r.Level)
	}
	if attrs["reason"].String() != "missing pr fields" || attrs["action"].String() != "labeled" ||
		attrs["pr_number"].Int64() != 42 || attrs["repo"].String() != "" {
		t.Errorf("attrs = %v", attrs)
	}
}

func TestReconcile_WaitBackgroundCoversReconcile(t *testing.T) {
	fg := reconcileFake()
	fg.pr = fetchedPR(false)
	fg.teardownGate = make(chan struct{})
	s, _ := newTeardownServer(t, fg)
	s.handlePullRequest(context.Background(), labelEvent("unlabeled", "preview"))

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.WaitBackground(expired); !errors.Is(err, context.Canceled) {
		t.Errorf("WaitBackground while blocked = %v, want context.Canceled", err)
	}
	close(fg.teardownGate)
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
	if fg.teardownSlug != "feature-pricing" {
		t.Errorf("teardown slug = %q, want feature-pricing", fg.teardownSlug)
	}
}

func TestReconcile_PanicRecovered(t *testing.T) {
	fg := reconcileFake()
	fg.pr = fetchedPR(false)
	fg.teardownPanic = true
	s, capture := newTeardownServer(t, fg)
	deliver(t, s, labelEvent("unlabeled", "preview"))

	r, attrs := teardownRecord(t, capture, "preview reconcile panicked")
	if r.Level != slog.LevelError {
		t.Errorf("level = %v, want ERROR", r.Level)
	}
	if attrs["repo"].String() != "acme/backend-1" || attrs["pr_number"].Int64() != 42 ||
		attrs["action"].String() != "unlabeled" || !strings.Contains(attrs["panic"].String(), "fake teardown panic") {
		t.Errorf("attrs = %v", attrs)
	}
}

// waitStarted waits for the next value sent on started and checks it is want.
func waitStarted(t *testing.T, started chan string, want string) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("started = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("%s never started", want)
	}
}

func TestJobs_NeverOverlap(t *testing.T) {
	fg := &fakeGitops{teardownGate: make(chan struct{}), pr: closedPR(), started: make(chan string, 4)}
	s, _ := newTeardownServer(t, fg)
	s.handlePullRequest(context.Background(), prEvent("closed", true))
	s.handlePullRequest(context.Background(), prEvent("closed", true))

	waitStarted(t, fg.started, "catalog")
	waitStarted(t, fg.started, "teardown")
	select {
	case got := <-fg.started:
		t.Fatalf("second job reached %q while the first held the gate", got)
	case <-time.After(quiet):
	}

	close(fg.teardownGate)
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
	if got := drain(fg.started); len(got) != 2 || got[0] != "catalog" || got[1] != "teardown" {
		t.Errorf("after release started = %v, want [catalog teardown]", got)
	}
}

func TestJobs_PanicReleasesLock(t *testing.T) {
	s, _ := newTeardownServer(t, &fakeGitops{})
	ctx := context.Background()

	started := make(chan struct{})
	panicked := make(chan any, 1)
	s.runJob(ctx, func(rec any) { panicked <- rec }, func(context.Context) {
		close(started)
		panic("job 1 panic")
	})
	<-started
	done := make(chan struct{})
	s.runJob(ctx, func(any) {}, func(context.Context) { close(done) })

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("job 2 never ran: the panicking job kept the lock")
	}
	if err := s.WaitBackground(ctx); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
	if rec := <-panicked; rec != "job 1 panic" {
		t.Errorf("panic = %v, want job 1 panic", rec)
	}
}

func TestJobs_TimeoutStartsAfterLock(t *testing.T) {
	const timeout = 200 * time.Millisecond
	fg := reconcileFake()
	s, _ := newTeardownServer(t, fg)
	s.jobTimeout = timeout
	ctx := context.Background()

	started := make(chan struct{})
	gate := make(chan struct{})
	s.runJob(ctx, func(any) {}, func(context.Context) {
		close(started)
		<-gate
	})
	<-started
	s.handlePullRequest(ctx, labelEvent("labeled", "preview", "preview"))
	time.Sleep(3 * timeout)
	close(gate)
	if err := s.WaitBackground(ctx); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}

	if fg.capturedSlug != "feature-pricing" || fg.capturedInputs.CommitSha != fetchedSHA {
		t.Errorf("deployed to %q at %q, want feature-pricing at %q: job 2's timeout ran while it waited for the lock",
			fg.capturedSlug, fg.capturedInputs.CommitSha, fetchedSHA)
	}
}

func TestReconcile_RecordsMetrics(t *testing.T) {
	m := metrics.New()
	s, _ := newTeardownServer(t, reconcileFake(), WithMetrics(m))
	deliver(t, s, labelEvent("labeled", "preview", "preview"))

	expected := `
# HELP doublure_reconciles_total Pull request reconciles, by outcome.
# TYPE doublure_reconciles_total counter
doublure_reconciles_total{outcome="deployed"} 1
`
	err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected), "doublure_reconciles_total")
	if err != nil {
		t.Error(err)
	}
}

func TestGo_TrackedByWaitBackground(t *testing.T) {
	s, _ := newTeardownServer(t, reconcileFake())
	release := make(chan struct{})
	done := make(chan struct{})
	s.Go(func() {
		<-release
		close(done)
	})

	ctx, cancel := context.WithTimeout(context.Background(), quiet)
	defer cancel()
	if err := s.WaitBackground(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground before release = %v, want deadline exceeded", err)
	}
	close(release)
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
	select {
	case <-done:
	default:
		t.Error("WaitBackground returned before f finished")
	}
}
