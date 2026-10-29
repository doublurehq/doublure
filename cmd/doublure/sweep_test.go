package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/metrics"
	"github.com/doublurehq/doublure/internal/preview"
	"github.com/doublurehq/doublure/internal/server"
)

var errUnexpected = errors.New("unexpected call")

// loopFake is a lifecycle.SweepAPI whose previews are all gone by the time
// they are read. ListPreviews returns slugs or listErr and, while block is
// open, waits for it to close or for its context to end. It tracks how many
// ListPreviews calls ran and the most that ran at once.
type loopFake struct {
	*metrics.Metrics

	slugs   []string
	listErr error
	block   chan struct{}

	mu        sync.Mutex
	calls     int
	active    int
	maxActive int
}

func (f *loopFake) ListPreviews(ctx context.Context) ([]string, error) {
	f.mu.Lock()
	f.calls++
	f.active++
	f.maxActive = max(f.maxActive, f.active)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.active--
		f.mu.Unlock()
	}()
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return f.slugs, f.listErr
}

func (f *loopFake) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.maxActive
}

func (f *loopFake) GetPreview(context.Context, string) (*preview.PreviewValues, string, error) {
	return nil, "", gitops.ErrNotFound
}

func (f *loopFake) FetchServiceCatalog(context.Context) (*gitops.ServiceCatalog, error) {
	return &gitops.ServiceCatalog{Label: "preview"}, nil
}

func (f *loopFake) GetPullRequest(context.Context, string, string, int) (*gitops.PullRequest, error) {
	return nil, errUnexpected
}

func (f *loopFake) DeletePreviewWithRetry(
	context.Context,
	string, string,
	func(preview.PreviewValues) bool,
) (*gitops.DeleteResult, error) {
	return nil, errUnexpected
}

func (f *loopFake) TeardownPreviewWithRetry(
	context.Context,
	string,
	string,
	string,
	int,
) (*gitops.TeardownResult, error) {
	return nil, errUnexpected
}

func (f *loopFake) UpsertPRComment(context.Context, string, string, int, string) error {
	return errUnexpected
}

func (f *loopFake) FanOutPRComments(context.Context, preview.PreviewValues, string) error {
	return errUnexpected
}

// sample scrapes m and returns the value of the sample key (name and labels
// as exposed), and whether it is exposed.
func sample(t *testing.T, m *metrics.Metrics, key string) (float64, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for line := range strings.Lines(rec.Body.String()) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), key+" "); ok {
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				t.Fatalf("parse %q: %v", line, err)
			}
			return f, true
		}
	}
	return 0, false
}

// waitFor polls cond until it holds, failing the test after a second.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

// startLoop runs runSweepLoop over f in the background and returns a func
// that cancels it and waits for it to return.
func startLoop(t *testing.T, f *loopFake, interval time.Duration, now func() time.Time) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSweepLoop(ctx, f, f.Metrics, interval, 0, now)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("runSweepLoop did not return after cancel")
		}
	}
}

func TestRunSweepLoop_RecordsRuns(t *testing.T) {
	at := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	f := &loopFake{Metrics: metrics.New(), slugs: []string{"a", "b"}}
	stop := startLoop(t, f, 5*time.Millisecond, func() time.Time { return at })
	waitFor(t, func() bool {
		n, _ := sample(t, f.Metrics, `doublure_sweep_runs_total{result="ok"}`)
		return n >= 2
	})
	stop()

	if got, _ := sample(t, f.Metrics, "doublure_sweep_last_success_timestamp_seconds"); got != float64(at.Unix()) {
		t.Errorf("last success = %v, want %v", got, at.Unix())
	}
	if got, _ := sample(t, f.Metrics, "doublure_previews"); got != 2 {
		t.Errorf("previews = %v, want 2", got)
	}
	if _, ok := sample(t, f.Metrics, `doublure_sweep_runs_total{result="error"}`); ok {
		t.Error("no run failed")
	}
}

func TestRunSweepLoop_RecordsFailures(t *testing.T) {
	f := &loopFake{Metrics: metrics.New(), listErr: errors.New("boom")}
	stop := startLoop(t, f, 5*time.Millisecond, time.Now)
	waitFor(t, func() bool {
		n, _ := sample(t, f.Metrics, `doublure_sweep_runs_total{result="error"}`)
		return n >= 1
	})
	stop()

	if got, _ := sample(t, f.Metrics, "doublure_sweep_last_success_timestamp_seconds"); got != 0 {
		t.Errorf("last success = %v, want 0", got)
	}
}

func TestRunSweepLoop_WaitsOneIntervalFirst(t *testing.T) {
	f := &loopFake{}
	stop := startLoop(t, f, time.Hour, time.Now)
	time.Sleep(20 * time.Millisecond)
	stop()

	if calls, _ := f.counts(); calls != 0 {
		t.Errorf("sweeps = %d before the first interval, want 0", calls)
	}
}

func TestRunSweepLoop_NeverOverlaps(t *testing.T) {
	f := &loopFake{block: make(chan struct{})}
	stop := startLoop(t, f, time.Millisecond, time.Now)
	waitFor(t, func() bool { calls, _ := f.counts(); return calls == 1 })
	time.Sleep(20 * time.Millisecond)
	if calls, _ := f.counts(); calls != 1 {
		t.Fatalf("sweeps = %d while the first one runs, want 1", calls)
	}
	close(f.block)
	waitFor(t, func() bool { calls, _ := f.counts(); return calls >= 3 })
	stop()

	if _, most := f.counts(); most != 1 {
		t.Errorf("concurrent sweeps = %d, want 1", most)
	}
}

func TestRunSweepLoop_CancelStopsAnInFlightSweepUnrecorded(t *testing.T) {
	f := &loopFake{Metrics: metrics.New(), block: make(chan struct{})}
	stop := startLoop(t, f, time.Millisecond, time.Now)
	waitFor(t, func() bool { calls, _ := f.counts(); return calls == 1 })
	stop()

	for _, key := range []string{`doublure_sweep_runs_total{result="ok"}`, `doublure_sweep_runs_total{result="error"}`} {
		if _, ok := sample(t, f.Metrics, key); ok {
			t.Errorf("%s recorded for a sweep cancelled by shutdown", key)
		}
	}
}

func TestStartSweepLoop_TrackedByServer(t *testing.T) {
	srv := server.NewServer(nil, nil, "t", 0)
	ctx, cancel := context.WithCancel(context.Background())
	startSweepLoop(ctx, srv, &loopFake{}, nil, time.Hour, 0)

	short, cancelShort := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelShort()
	if err := srv.WaitBackground(short); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitBackground with the loop running = %v, want deadline exceeded", err)
	}
	cancel()
	if err := srv.WaitBackground(context.Background()); err != nil {
		t.Fatalf("WaitBackground: %v", err)
	}
}

func TestStartSweepLoop_OffStartsNothing(t *testing.T) {
	srv := server.NewServer(nil, nil, "t", 0)
	startSweepLoop(context.Background(), srv, &loopFake{}, nil, 0, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := srv.WaitBackground(ctx); err != nil {
		t.Errorf("WaitBackground = %v, want nil", err)
	}
}

func TestParseCleanupInterval(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw     string
		want    time.Duration
		wantErr string
	}{
		{raw: "", want: 0},
		{raw: "1m", want: time.Minute},
		{raw: "1h", want: time.Hour},
		{raw: "30s", wantErr: `CLEANUP_INTERVAL must be a duration of at least 1m, got "30s"`},
		{raw: "abc", wantErr: `CLEANUP_INTERVAL must be a duration of at least 1m, got "abc"`},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()

			got, err := parseCleanupInterval(tc.raw)
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("parseCleanupInterval(%q) err = %v, want %q", tc.raw, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCleanupInterval(%q): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("parseCleanupInterval(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
