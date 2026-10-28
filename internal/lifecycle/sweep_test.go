package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/preview"
)

const (
	sweepCommitURL = "https://github.com/acme/gitops/commit/c0ffee"
	ttl            = 168 * time.Hour
)

// now is the sweep time: 2026-06-10, so 2026-05-26 is past a 168h TTL and
// 2026-06-09 is not.
var now = time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test fixture

// logCapture is a slog.Handler retaining every record.
type logCapture struct {
	mu      sync.Mutex
	entries []slog.Record
}

func (h *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *logCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, r.Clone())
	return nil
}

func (h *logCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *logCapture) WithGroup(string) slog.Handler      { return h }

// find returns the attributes of every record logged with msg at level.
func (h *logCapture) find(level slog.Level, msg string) []map[string]slog.Value {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []map[string]slog.Value
	for _, r := range h.entries {
		if r.Level != level || r.Message != msg {
			continue
		}
		attrs := map[string]slog.Value{}
		r.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value
			return true
		})
		out = append(out, attrs)
	}
	return out
}

func captureLogs(t *testing.T) *logCapture {
	t.Helper()
	capture := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return capture
}

type deleteCall struct {
	slug    string
	message string
}

type teardownCall struct {
	slug, owner, repo string
	number            int
}

type fanOut struct {
	vals preview.PreviewValues
	body string
}

// fakeAPI is an in-memory SweepAPI. files holds what GetPreview reads; fresh,
// when set for a slug, is what DeletePreviewWithRetry re-reads (a deploy that
// landed in between). PR states are keyed by "owner/repo#n".
type fakeAPI struct {
	previews  []string
	listErr   error
	files     map[string]*preview.PreviewValues
	getErr    map[string]error
	fresh     map[string]preview.PreviewValues
	deleteErr map[string]error
	prOpen    map[string]bool
	prErr     map[string]error
	fanErr    error

	deletes   []deleteCall
	prReads   []string
	teardowns []teardownCall
	upserts   []string
	fanOuts   []fanOut
}

func (f *fakeAPI) ListPreviews(context.Context) ([]string, error) {
	return f.previews, f.listErr
}

func (f *fakeAPI) GetPreview(_ context.Context, slug string) (*preview.PreviewValues, string, error) {
	if err := f.getErr[slug]; err != nil {
		return nil, "", err
	}
	vals, ok := f.files[slug]
	if !ok {
		return nil, "", gitops.ErrNotFound
	}
	return vals, "blob-" + slug, nil
}

func (f *fakeAPI) GetPullRequest(_ context.Context, owner, repo string, number int) (*gitops.PullRequest, error) {
	key := fmt.Sprintf("%s/%s#%d", owner, repo, number)
	f.prReads = append(f.prReads, key)
	if err := f.prErr[key]; err != nil {
		return nil, err
	}
	return &gitops.PullRequest{Open: f.prOpen[key]}, nil
}

func (f *fakeAPI) DeletePreviewWithRetry(
	_ context.Context,
	slug, message string,
	stillDelete func(preview.PreviewValues) bool,
) (*gitops.DeleteResult, error) {
	if err := f.deleteErr[slug]; err != nil {
		return nil, err
	}
	vals := *f.files[slug]
	if fresh, ok := f.fresh[slug]; ok {
		vals = fresh
	}
	if !stillDelete(vals) {
		return &gitops.DeleteResult{}, nil
	}
	f.deletes = append(f.deletes, deleteCall{slug: slug, message: message})
	return &gitops.DeleteResult{Deleted: true, Values: vals, CommitURL: sweepCommitURL}, nil
}

func (f *fakeAPI) TeardownPreviewWithRetry(
	_ context.Context,
	slug, owner, repo string,
	prNumber int,
) (*gitops.TeardownResult, error) {
	f.teardowns = append(f.teardowns, teardownCall{slug: slug, owner: owner, repo: repo, number: prNumber})
	return &gitops.TeardownResult{
		Removed:       []string{repo},
		Deleted:       true,
		CommitURL:     sweepCommitURL,
		CommitMessage: "chore(preview): delete " + slug + " preview",
	}, nil
}

func (f *fakeAPI) UpsertPRComment(_ context.Context, owner, repo string, prNumber int, _ string) error {
	f.upserts = append(f.upserts, fmt.Sprintf("%s/%s#%d", owner, repo, prNumber))
	return nil
}

func (f *fakeAPI) FetchServiceCatalog(context.Context) (*gitops.ServiceCatalog, error) {
	return &gitops.ServiceCatalog{}, nil
}

func (f *fakeAPI) FanOutPRComments(_ context.Context, vals preview.PreviewValues, body string) error {
	f.fanOuts = append(f.fanOuts, fanOut{vals: vals, body: body})
	return f.fanErr
}

// pin is an entry pinned by prURL and last deployed at updatedAt.
func pin(name, prURL, updatedAt string) preview.ServiceEntry {
	return preview.ServiceEntry{
		Name:      name,
		CommitSha: "abc123",
		Metadata:  &preview.Metadata{PRURL: prURL, UpdatedAt: updatedAt},
	}
}

func values(entries ...preview.ServiceEntry) *preview.PreviewValues {
	return &preview.PreviewValues{Services: entries}
}

const (
	pr42   = "https://github.com/acme/backend-1/pull/42"
	pr7    = "https://github.com/acme/backend-2/pull/7"
	recent = "2026-06-09T10:00:00Z"
	stale  = "2026-05-26T10:00:00Z"
)

func TestSweep_NoPinsDeletes(t *testing.T) {
	capture := captureLogs(t)
	f := &fakeAPI{
		previews: []string{"feature-x"},
		files:    map[string]*preview.PreviewValues{"feature-x": values(preview.ServiceEntry{Name: "backend-1"})},
	}
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	want := []deleteCall{{slug: "feature-x", message: "chore(preview): delete feature-x preview"}}
	if !slices.Equal(f.deletes, want) {
		t.Errorf("deletes = %+v, want %+v", f.deletes, want)
	}
	if len(f.fanOuts) != 0 || len(f.upserts) != 0 || len(f.prReads) != 0 {
		t.Errorf("rule 1 must not comment or read PRs: fanOuts=%d upserts=%v prReads=%v",
			len(f.fanOuts), f.upserts, f.prReads)
	}
	logs := capture.find(slog.LevelInfo, "preview deleted")
	if len(logs) != 1 || logs[0]["slug"].String() != "feature-x" || logs[0]["reason"].String() != "no pins" ||
		logs[0]["gitops_commit_url"].String() != sweepCommitURL {
		t.Errorf("preview deleted logs = %v", logs)
	}
	if _, ok := logs[0]["last_deploy"]; ok {
		t.Error("a no-pins delete must not carry last_deploy")
	}
}

func TestSweep_ExpiredDeletesAndComments(t *testing.T) {
	capture := captureLogs(t)
	f := &fakeAPI{
		previews: []string{"feature-x"},
		files: map[string]*preview.PreviewValues{"feature-x": values(
			pin("backend-1", pr42, stale),
			preview.ServiceEntry{Name: "front"},
		)},
		// The PR is closed: rule 2 still wins over rule 3.
		prOpen: map[string]bool{},
	}
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	want := []deleteCall{{slug: "feature-x", message: "chore(preview): expire feature-x preview"}}
	if !slices.Equal(f.deletes, want) {
		t.Errorf("deletes = %+v, want %+v", f.deletes, want)
	}
	if len(f.prReads) != 0 || len(f.teardowns) != 0 {
		t.Errorf("an expired preview must not go through rule 3: prReads=%v teardowns=%v", f.prReads, f.teardowns)
	}
	wantBody := "## 🧹 Preview: `feature-x`\n\n" +
		"Preview deleted: no deploy for 168h0m0s.\n\n" +
		"---\n" +
		"🔗 [chore(preview): expire feature-x preview](" + sweepCommitURL + ")"
	if len(f.fanOuts) != 1 || f.fanOuts[0].body != wantBody || len(f.fanOuts[0].vals.Services) != 2 {
		t.Fatalf("fanOuts = %+v, want one with body:\n%s", f.fanOuts, wantBody)
	}
	logs := capture.find(slog.LevelInfo, "preview deleted")
	if len(logs) != 1 || logs[0]["reason"].String() != "expired" ||
		logs[0]["last_deploy"].String() != stale || logs[0]["gitops_commit_url"].String() != sweepCommitURL {
		t.Errorf("preview deleted logs = %v", logs)
	}
}

func TestSweep_ExpiryCommentFailure(t *testing.T) {
	capture := captureLogs(t)
	f := &fakeAPI{
		previews: []string{"feature-x"},
		files:    map[string]*preview.PreviewValues{"feature-x": values(pin("backend-1", pr42, stale))},
		fanErr:   errors.New("comment boom"),
	}
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
		t.Fatalf("Sweep: %v (a comment failure is not a preview failure)", err)
	}
	logs := capture.find(slog.LevelWarn, "preview expiry comment failed")
	if len(logs) != 1 || logs[0]["slug"].String() != "feature-x" || logs[0]["err"].String() != "comment boom" {
		t.Errorf("expiry comment failed logs = %v", logs)
	}
}

func TestSweep_NeverExpires(t *testing.T) {
	cases := []struct {
		name string
		ttl  time.Duration
		vals *preview.PreviewValues
	}{
		{name: "ttl off", ttl: 0, vals: values(pin("backend-1", pr42, stale))},
		{name: "unparseable updated-at", ttl: ttl, vals: values(pin("backend-1", pr42, "2026-05-26"))},
		{name: "recent deploy", ttl: ttl, vals: values(pin("backend-1", pr42, stale), pin("backend-2", pr7, recent))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			f := &fakeAPI{
				previews: []string{"feature-x"},
				files:    map[string]*preview.PreviewValues{"feature-x": tc.vals},
				prOpen:   map[string]bool{"acme/backend-1#42": true, "acme/backend-2#7": true},
			}
			if err := lifecycle.Sweep(context.Background(), f, now, tc.ttl); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if len(f.deletes) != 0 || len(f.fanOuts) != 0 || len(f.teardowns) != 0 {
				t.Errorf("deletes=%v fanOuts=%d teardowns=%v, want nothing", f.deletes, len(f.fanOuts), f.teardowns)
			}
		})
	}
}

func TestSweep_ClosedPRTearsDown(t *testing.T) {
	captureLogs(t)
	f := &fakeAPI{
		previews: []string{"feature-x"},
		files: map[string]*preview.PreviewValues{"feature-x": values(
			pin("backend-1", pr42, recent),
			pin("backend-2", pr7, recent),
		)},
		prOpen: map[string]bool{"acme/backend-2#7": true},
	}
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if want := []string{"acme/backend-1#42", "acme/backend-2#7"}; !slices.Equal(f.prReads, want) {
		t.Errorf("prReads = %v, want %v", f.prReads, want)
	}
	want := []teardownCall{{slug: "feature-x", owner: "acme", repo: "backend-1", number: 42}}
	if !slices.Equal(f.teardowns, want) {
		t.Errorf("teardowns = %+v, want %+v", f.teardowns, want)
	}
	// The teardown path comments on the closed PR, exactly like a webhook close.
	if want := []string{"acme/backend-1#42"}; !slices.Equal(f.upserts, want) {
		t.Errorf("upserts = %v, want %v", f.upserts, want)
	}
	if len(f.deletes) != 0 {
		t.Errorf("deletes = %v, want none", f.deletes)
	}
}

func TestSweep_PRStateUnknownKeepsPin(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{name: "404", err: gitops.ErrNotFound},
		{name: "500", err: errors.New("GET pulls/42: 500 boom")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := captureLogs(t)
			f := &fakeAPI{
				previews: []string{"feature-x"},
				files:    map[string]*preview.PreviewValues{"feature-x": values(pin("backend-1", pr42, recent))},
				prErr:    map[string]error{"acme/backend-1#42": tc.err},
			}
			if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if len(f.teardowns) != 0 || len(f.deletes) != 0 || len(f.fanOuts) != 0 || len(f.upserts) != 0 {
				t.Errorf("teardowns=%v deletes=%v fanOuts=%d upserts=%v, want none",
					f.teardowns, f.deletes, len(f.fanOuts), f.upserts)
			}
			logs := capture.find(slog.LevelWarn, "cleanup pr state unknown")
			if len(logs) != 1 || logs[0]["slug"].String() != "feature-x" ||
				logs[0]["repo"].String() != "acme/backend-1" ||
				logs[0]["pr_number"].Int64() != 42 || logs[0]["err"].String() != tc.err.Error() {
				t.Errorf("pr state unknown logs = %v", logs)
			}
		})
	}
}

func TestSweep_OnePRPinningTwoServicesCheckedOnce(t *testing.T) {
	captureLogs(t)
	f := &fakeAPI{
		previews: []string{"feature-x"},
		files: map[string]*preview.PreviewValues{"feature-x": values(
			pin("backend-1", pr42, recent),
			pin("worker", "https://github.com/ACME/Backend-1/pull/42/files", recent),
			pin("front", "not-a-pr-url", recent),
			preview.ServiceEntry{Name: "backend-2", CommitSha: "abc123"},
		)},
	}
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if want := []string{"acme/backend-1#42"}; !slices.Equal(f.prReads, want) {
		t.Errorf("prReads = %v, want %v", f.prReads, want)
	}
	if len(f.teardowns) != 1 {
		t.Errorf("teardowns = %+v, want one", f.teardowns)
	}
}

func TestSweep_ConcurrentDeployCancelsDelete(t *testing.T) {
	cases := []struct {
		name  string
		file  *preview.PreviewValues
		fresh preview.PreviewValues
	}{
		{
			name:  "rule 1: a new pin",
			file:  values(preview.ServiceEntry{Name: "backend-1"}),
			fresh: *values(pin("backend-1", pr42, recent)),
		},
		{
			name:  "rule 2: a newer updated-at",
			file:  values(pin("backend-1", pr42, stale)),
			fresh: *values(pin("backend-1", pr42, recent)),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := captureLogs(t)
			f := &fakeAPI{
				previews: []string{"feature-x"},
				files:    map[string]*preview.PreviewValues{"feature-x": tc.file},
				fresh:    map[string]preview.PreviewValues{"feature-x": tc.fresh},
			}
			if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if len(f.deletes) != 0 || len(f.fanOuts) != 0 {
				t.Errorf("deletes=%v fanOuts=%d, want none", f.deletes, len(f.fanOuts))
			}
			if logs := capture.find(slog.LevelInfo, "preview deleted"); len(logs) != 0 {
				t.Errorf("preview deleted logged: %v", logs)
			}
		})
	}
}

func TestSweep_FailuresCountedAndSweepContinues(t *testing.T) {
	capture := captureLogs(t)
	unpinned := values(preview.ServiceEntry{Name: "backend-1"})
	f := &fakeAPI{
		previews: []string{"a", "b", "c", "d", "gone"},
		files: map[string]*preview.PreviewValues{
			"a": unpinned, "b": unpinned, "c": unpinned, "d": unpinned,
		},
		getErr:    map[string]error{"b": errors.New("read boom")},
		deleteErr: map[string]error{"c": errors.New("delete boom")},
	}
	err := lifecycle.Sweep(context.Background(), f, now, 0)
	if err == nil || err.Error() != "cleanup: 2 preview(s) failed" {
		t.Fatalf("err = %v, want cleanup: 2 preview(s) failed", err)
	}
	want := []deleteCall{
		{slug: "a", message: "chore(preview): delete a preview"},
		{slug: "d", message: "chore(preview): delete d preview"},
	}
	if !slices.Equal(f.deletes, want) {
		t.Errorf("deletes = %+v, want %+v", f.deletes, want)
	}
	failed := capture.find(slog.LevelError, "cleanup preview failed")
	if len(failed) != 2 || failed[0]["slug"].String() != "b" || failed[0]["err"].String() != "read boom" ||
		failed[1]["slug"].String() != "c" || failed[1]["err"].String() != "delete boom" {
		t.Errorf("cleanup preview failed logs = %v", failed)
	}
	started := capture.find(slog.LevelInfo, "cleanup started")
	if len(started) != 1 || started[0]["previews"].Int64() != 5 || started[0]["ttl"].String() != "0s" {
		t.Errorf("cleanup started logs = %v", started)
	}
	finished := capture.find(slog.LevelInfo, "cleanup finished")
	if len(finished) != 1 || finished[0]["previews"].Int64() != 5 || finished[0]["failed"].Int64() != 2 {
		t.Errorf("cleanup finished logs = %v", finished)
	}
}

func TestSweep_ListError(t *testing.T) {
	captureLogs(t)
	listErr := errors.New("list boom")
	f := &fakeAPI{listErr: listErr}
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); !errors.Is(err, listErr) {
		t.Fatalf("err = %v, want %v", err, listErr)
	}
}
