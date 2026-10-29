package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

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

// assertSweepMetrics fails t unless m's sweep runs and last success are
// exactly expected, in the text exposition format.
func assertSweepMetrics(t *testing.T, m *metrics.Metrics, expected string) {
	t.Helper()
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected),
		"doublure_sweep_runs_total", "doublure_sweep_last_success_timestamp_seconds"); err != nil {
		t.Error(err)
	}
}

// startLoop runs runSweepLoop over f in the background and returns a func
// that cancels it and waits for it to return; calling it again is a no-op.
// It must run in a synctest bubble, where a loop that never returns
// deadlocks the test.
func startLoop(f *loopFake, interval time.Duration, now func() time.Time) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSweepLoop(ctx, f, f.Metrics, interval, 0, now)
	}()
	return func() {
		cancel()
		<-done
	}
}

// advance moves the bubble's clock by d and waits for every goroutine in it
// to block.
func advance(d time.Duration) {
	time.Sleep(d)
	synctest.Wait()
}

func TestRunSweepLoop_RecordsRuns(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		at := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
		f := &loopFake{Metrics: metrics.New(), slugs: []string{"a", "b"}}
		stop := startLoop(f, time.Minute, func() time.Time { return at })
		advance(2*time.Minute + time.Second)
		stop()

		assertSweepMetrics(t, f.Metrics, `
# HELP doublure_sweep_runs_total In-server cleanup sweep runs, by result.
# TYPE doublure_sweep_runs_total counter
doublure_sweep_runs_total{result="ok"} 2
# HELP doublure_sweep_last_success_timestamp_seconds Unix time of the last successful in-server cleanup sweep, 0 before the first one.
# TYPE doublure_sweep_last_success_timestamp_seconds gauge
doublure_sweep_last_success_timestamp_seconds `+strconv.FormatInt(at.Unix(), 10)+`
`)
		expected := "# HELP doublure_previews Previews in the gitops repo as of the last in-server cleanup sweep, " +
			"0 before the first one.\n# TYPE doublure_previews gauge\ndoublure_previews 2\n"
		err := testutil.GatherAndCompare(f.Gatherer(), strings.NewReader(expected), "doublure_previews")
		if err != nil {
			t.Error(err)
		}
	})
}

func TestRunSweepLoop_RecordsFailures(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &loopFake{Metrics: metrics.New(), listErr: errors.New("boom")}
		stop := startLoop(f, time.Minute, time.Now)
		advance(time.Minute + time.Second)
		stop()

		assertSweepMetrics(t, f.Metrics, `
# HELP doublure_sweep_runs_total In-server cleanup sweep runs, by result.
# TYPE doublure_sweep_runs_total counter
doublure_sweep_runs_total{result="error"} 1
# HELP doublure_sweep_last_success_timestamp_seconds Unix time of the last successful in-server cleanup sweep, 0 before the first one.
# TYPE doublure_sweep_last_success_timestamp_seconds gauge
doublure_sweep_last_success_timestamp_seconds 0
`)
	})
}

func TestRunSweepLoop_WaitsOneIntervalFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &loopFake{}
		stop := startLoop(f, time.Hour, time.Now)
		advance(time.Hour - time.Nanosecond)
		stop()

		if calls, _ := f.counts(); calls != 0 {
			t.Errorf("sweeps = %d before the first interval, want 0", calls)
		}
	})
}

func TestRunSweepLoop_NeverOverlaps(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &loopFake{block: make(chan struct{})}
		stop := startLoop(f, time.Minute, time.Now)
		defer stop()
		advance(time.Minute + time.Second)
		if calls, _ := f.counts(); calls != 1 {
			t.Fatalf("sweeps = %d after the first interval, want 1", calls)
		}
		advance(cleanupTimeout / 2)
		if calls, _ := f.counts(); calls != 1 {
			t.Fatalf("sweeps = %d while the first one runs, want 1", calls)
		}
		close(f.block)
		advance(time.Minute)
		stop()

		calls, most := f.counts()
		if calls < 3 {
			t.Errorf("sweeps = %d once the first one returned, want at least 3", calls)
		}
		if most != 1 {
			t.Errorf("concurrent sweeps = %d, want 1", most)
		}
	})
}

func TestRunSweepLoop_CancelStopsAnInFlightSweepUnrecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &loopFake{Metrics: metrics.New(), block: make(chan struct{})}
		stop := startLoop(f, time.Minute, time.Now)
		defer stop()
		advance(time.Minute + time.Second)
		if calls, _ := f.counts(); calls != 1 {
			t.Fatalf("sweeps = %d after the first interval, want 1", calls)
		}
		stop()

		if n, err := testutil.GatherAndCount(f.Gatherer(), "doublure_sweep_runs_total"); err != nil || n != 0 {
			t.Errorf("sweep runs = %d (err %v) recorded for a sweep cancelled by shutdown, want 0", n, err)
		}
	})
}

func TestStartSweepLoop_TrackedByServer(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := server.NewServer(nil, nil, "t", 0)
		ctx, cancel := context.WithCancel(context.Background())
		startSweepLoop(ctx, srv, &loopFake{}, nil, time.Hour, 0)

		short, cancelShort := context.WithTimeout(context.Background(), time.Second)
		defer cancelShort()
		if err := srv.WaitBackground(short); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("WaitBackground with the loop running = %v, want deadline exceeded", err)
		}
		cancel()
		if err := srv.WaitBackground(context.Background()); err != nil {
			t.Fatalf("WaitBackground: %v", err)
		}
	})
}

func TestStartSweepLoop_OffStartsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := server.NewServer(nil, nil, "t", 0)
		startSweepLoop(context.Background(), srv, &loopFake{}, nil, 0, 0)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := srv.WaitBackground(ctx); err != nil {
			t.Errorf("WaitBackground = %v, want nil", err)
		}
	})
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
