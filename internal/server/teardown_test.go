package server

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	gh "github.com/google/go-github/v71/github"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

const (
	teardownSlug      = "feature-add-pricing"
	teardownCommitURL = "https://github.com/acme/gitops/commit/c0ffee"
)

// newTeardownServer returns a server backed by fg, with every log record
// captured.
func newTeardownServer(t *testing.T, fg *fakeGitops) (*Server, *LogCapture) {
	t.Helper()
	capture := &LogCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return NewServer(nil, fg, "t", 123), capture
}

// prEvent builds a pull_request event for acme/backend-1#42 on
// feature/add-pricing.
func prEvent(action string, merged bool) *gh.PullRequestEvent {
	return &gh.PullRequestEvent{
		Action: gh.Ptr(action),
		Number: gh.Ptr(42),
		Repo: &gh.Repository{
			Name:     gh.Ptr("backend-1"),
			FullName: gh.Ptr("acme/backend-1"),
			Owner:    &gh.User{Login: gh.Ptr("acme")},
		},
		PullRequest: &gh.PullRequest{
			Merged: gh.Ptr(merged),
			Head:   &gh.PullRequestBranch{Ref: gh.Ptr("feature/add-pricing")},
		},
	}
}

// closeAndWait delivers a closed event and waits for the background teardown.
func closeAndWait(t *testing.T, s *Server) {
	t.Helper()
	s.handlePullRequest(context.Background(), prEvent("closed", true))
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
}

// teardownRecord returns the record logged with msg, and its attributes.
func teardownRecord(t *testing.T, capture *LogCapture, msg string) (slog.Record, map[string]slog.Value) {
	t.Helper()
	for _, r := range capture.Records() {
		if r.Message != msg {
			continue
		}
		attrs := map[string]slog.Value{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value
			return true
		})
		return r, attrs
	}
	t.Fatalf("no %q log record", msg)
	return slog.Record{}, nil
}

// assertTeardownAttrs checks the attributes every teardown log line carries.
func assertTeardownAttrs(t *testing.T, attrs map[string]slog.Value) {
	t.Helper()
	if got := attrs["slug"].String(); got != teardownSlug {
		t.Errorf("slug = %q, want %q", got, teardownSlug)
	}
	if got := attrs["repo"].String(); got != "acme/backend-1" {
		t.Errorf("repo = %q, want acme/backend-1", got)
	}
	if got := attrs["pr_number"].Int64(); got != 42 {
		t.Errorf("pr_number = %d, want 42", got)
	}
}

func TestTeardown_ClosedStartsTeardown(t *testing.T) {
	for _, merged := range []bool{true, false} {
		t.Run(map[bool]string{true: "merged", false: "not merged"}[merged], func(t *testing.T) {
			fg := &fakeGitops{}
			s, _ := newTeardownServer(t, fg)
			s.handlePullRequest(context.Background(), prEvent("closed", merged))
			if err := s.WaitBackground(context.Background()); err != nil {
				t.Fatalf("WaitBackground: %v", err)
			}
			if fg.teardownSlug != teardownSlug || fg.teardownOwner != "acme" ||
				fg.teardownRepo != "backend-1" || fg.teardownNumber != 42 {
				t.Errorf("teardown(%q, %q, %q, %d), want (%q, acme, backend-1, 42)",
					fg.teardownSlug, fg.teardownOwner, fg.teardownRepo, fg.teardownNumber, teardownSlug)
			}
		})
	}
}

func TestTeardown_OpenedStartsNothing(t *testing.T) {
	fg := &fakeGitops{}
	s, _ := newTeardownServer(t, fg)
	s.handlePullRequest(context.Background(), prEvent("opened", false))
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
	if fg.teardownSlug != "" {
		t.Errorf("opened started a teardown for %q", fg.teardownSlug)
	}
}

func TestTeardown_RunsInBackground(t *testing.T) {
	fg := &fakeGitops{teardownGate: make(chan struct{})}
	s, _ := newTeardownServer(t, fg)

	returned := make(chan struct{})
	go func() {
		s.handlePullRequest(context.Background(), prEvent("closed", true))
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("handlePullRequest blocked on the teardown")
	}

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.WaitBackground(expired); !errors.Is(err, context.Canceled) {
		t.Errorf("WaitBackground while blocked = %v, want context.Canceled", err)
	}

	close(fg.teardownGate)
	if err := s.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
	if fg.teardownSlug != teardownSlug {
		t.Errorf("teardown slug = %q, want %q", fg.teardownSlug, teardownSlug)
	}
}

func TestTeardown_UpdateComments(t *testing.T) {
	const msg = "chore(preview): remove backend-1 from feature-add-pricing"
	fg := &fakeGitops{
		catalog: []string{"backend-1", "backend-2", "front"},
		teardownResult: &gitops.TeardownResult{
			Values: preview.PreviewValues{Services: []preview.ServiceEntry{
				{Name: "backend-1"},
				{Name: "backend-2", CommitSha: "bbbbbbbbbb", Metadata: &preview.Metadata{
					PRURL: "https://github.com/acme/backend-2/pull/7", PRNumber: "7",
				}},
				{Name: "front"},
			}},
			Removed:       []string{"backend-1"},
			CommitURL:     teardownCommitURL,
			CommitMessage: msg,
		},
	}
	s, capture := newTeardownServer(t, fg)
	closeAndWait(t, s)

	if len(fg.upserts) != 1 {
		t.Fatalf("upserts = %+v, want one on the closed PR", fg.upserts)
	}
	up := fg.upserts[0]
	if up.owner != "acme" || up.repo != "backend-1" || up.number != 42 {
		t.Errorf("upsert target = %s/%s#%d, want acme/backend-1#42", up.owner, up.repo, up.number)
	}
	wantComment := "## 🧹 Preview: `feature-add-pricing`\n\n" +
		"Removed `backend-1` from this preview (PR closed).\n\n" +
		"---\n" +
		"🔗 [" + msg + "](" + teardownCommitURL + ")"
	if up.body != wantComment {
		t.Errorf("closed-PR comment mismatch\n got:\n%s\nwant:\n%s", up.body, wantComment)
	}

	wantSummary := "## 🚀 Preview: `feature-add-pricing`\n\n" +
		"- **backend-1** · 🔄 `main`\n" +
		"- **backend-2** · 📌 [`bbbbbbbb`](https://github.com/acme/backend-2/commit/bbbbbbbbbb)" +
		" · [PR #7](https://github.com/acme/backend-2/pull/7)\n" +
		"- **front** · 🔄 `main`\n\n" +
		"---\n" +
		"🔗 [" + msg + "](" + teardownCommitURL + ")"
	if !fg.fanOutBodyCalled || fg.capturedSummary != wantSummary {
		t.Errorf("fan-out summary mismatch (called=%v)\n got:\n%s\nwant:\n%s",
			fg.fanOutBodyCalled, fg.capturedSummary, wantSummary)
	}

	r, attrs := teardownRecord(t, capture, "preview teardown")
	if r.Level != slog.LevelInfo {
		t.Errorf("level = %v, want INFO", r.Level)
	}
	assertTeardownAttrs(t, attrs)
	if got := attrs["removed"].String(); got != "backend-1" {
		t.Errorf("removed = %q, want backend-1", got)
	}
	if attrs["deleted"].Bool() {
		t.Error("deleted = true, want false")
	}
	if got := attrs["gitops_commit_url"].String(); got != teardownCommitURL {
		t.Errorf("gitops_commit_url = %q", got)
	}
}

func TestTeardown_DeleteComment(t *testing.T) {
	const msg = "chore(preview): delete feature-add-pricing preview"
	fg := &fakeGitops{
		teardownResult: &gitops.TeardownResult{
			Removed:       []string{"backend-1"},
			Deleted:       true,
			CommitURL:     teardownCommitURL,
			CommitMessage: msg,
		},
	}
	s, capture := newTeardownServer(t, fg)
	closeAndWait(t, s)

	wantComment := "## 🧹 Preview: `feature-add-pricing`\n\n" +
		"Preview deleted: no open PR remains.\n\n" +
		"---\n" +
		"🔗 [" + msg + "](" + teardownCommitURL + ")"
	if len(fg.upserts) != 1 || fg.upserts[0].body != wantComment {
		t.Errorf("upserts = %+v, want one with body:\n%s", fg.upserts, wantComment)
	}
	if fg.fanOutBodyCalled {
		t.Error("a deleted preview must not fan out to remaining PRs")
	}
	_, attrs := teardownRecord(t, capture, "preview teardown")
	if !attrs["deleted"].Bool() {
		t.Error("deleted = false, want true")
	}
}

func TestTeardown_Failure(t *testing.T) {
	fg := &fakeGitops{teardownErr: errors.New("boom")}
	s, capture := newTeardownServer(t, fg)
	closeAndWait(t, s)

	r, attrs := teardownRecord(t, capture, "preview teardown failed")
	if r.Level != slog.LevelError {
		t.Errorf("level = %v, want ERROR", r.Level)
	}
	assertTeardownAttrs(t, attrs)
	if got := attrs["err"].String(); got != "boom" {
		t.Errorf("err = %q, want boom", got)
	}
	if len(fg.upserts) != 0 || fg.fanOutBodyCalled {
		t.Error("a failed teardown must not comment")
	}
}

func TestTeardown_NothingRemoved(t *testing.T) {
	fg := &fakeGitops{}
	s, capture := newTeardownServer(t, fg)
	closeAndWait(t, s)

	r, attrs := teardownRecord(t, capture, "preview teardown skipped")
	if r.Level != slog.LevelInfo {
		t.Errorf("level = %v, want INFO", r.Level)
	}
	assertTeardownAttrs(t, attrs)
	if got := attrs["reason"].String(); got != "no preview or no matching service" {
		t.Errorf("reason = %q", got)
	}
	if len(fg.upserts) != 0 || fg.fanOutBodyCalled {
		t.Error("a skipped teardown must not comment")
	}
}

func TestTeardown_PanicRecovered(t *testing.T) {
	fg := &fakeGitops{teardownPanic: true}
	s, capture := newTeardownServer(t, fg)
	closeAndWait(t, s)

	r, attrs := teardownRecord(t, capture, "preview teardown panicked")
	if r.Level != slog.LevelError {
		t.Errorf("level = %v, want ERROR", r.Level)
	}
	assertTeardownAttrs(t, attrs)
	if got := attrs["panic"].String(); got != "fake teardown panic" {
		t.Errorf("panic = %q", got)
	}
}
