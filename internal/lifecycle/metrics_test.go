package lifecycle_test

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/metrics"
	"github.com/doublurehq/doublure/internal/preview"
)

// samples scrapes m and returns its samples whose name starts with one of
// prefixes, keyed by name and labels as exposed.
func samples(t *testing.T, m *metrics.Metrics, prefixes ...string) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	out := map[string]float64{}
	for line := range strings.Lines(rec.Body.String()) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		key := line[:i]
		if !strings.HasPrefix(key, "doublure_") {
			continue
		}
		for _, p := range prefixes {
			if strings.HasPrefix(key, p) {
				v, err := strconv.ParseFloat(line[i+1:], 64)
				if err != nil {
					t.Fatalf("parse %q: %v", line, err)
				}
				out[key] = v
			}
		}
	}
	return out
}

func assertMetrics(t *testing.T, m *metrics.Metrics, want map[string]float64, prefixes ...string) {
	t.Helper()
	if got := samples(t, m, prefixes...); !maps.Equal(got, want) {
		t.Errorf("metrics = %v, want %v", got, want)
	}
}

func TestReconcileMetrics_Outcomes(t *testing.T) {
	deleted := &gitops.TeardownResult{Removed: []string{"front"}, Deleted: true, CommitURL: sweepCommitURL}
	held := values(pin("front", "https://github.com/acme/front/pull/7", recent))
	fork := fetchedPR("front", true, "preview")
	fork.HeadFullName = "octocat/front"
	cases := []struct {
		name string
		f    *reconcileFake
		ev   lifecycle.Event
		want map[string]float64
	}{
		{
			name: "deployed",
			f:    &reconcileFake{pr: fetchedPR("front", true, "preview")},
			ev:   event("labeled", "front", "preview", "preview"),
			want: map[string]float64{
				`doublure_reconciles_total{outcome="deployed"}`: 1,
			},
		},
		{
			name: "deployed despite a failed service",
			f: &reconcileFake{
				pr:       fetchedPR("monorepo", true, "preview"),
				writeErr: map[string]error{"api": errors.New("boom")},
			},
			ev: event("labeled", "monorepo", "preview", "preview"),
			want: map[string]float64{
				`doublure_reconciles_total{outcome="deployed"}`: 1,
			},
		},
		{
			name: "payload skip",
			f:    &reconcileFake{},
			ev:   event("labeled", "front", "bug", "bug"),
			want: map[string]float64{
				`doublure_reconciles_total{outcome="skipped"}`:         1,
				`doublure_reconcile_skips_total{reason="other label"}`: 1,
			},
		},
		{
			name: "fork skip",
			f:    &reconcileFake{pr: fork},
			ev:   event("labeled", "front", "preview", "preview"),
			want: map[string]float64{
				`doublure_reconciles_total{outcome="skipped"}`:     1,
				`doublure_reconcile_skips_total{reason="fork pr"}`: 1,
			},
		},
		{
			name: "held skip after cleanup",
			f: &reconcileFake{
				pr:       fetchedPR("front", true, "preview:pricing"),
				previews: map[string]*preview.PreviewValues{"pricing": held},
				teardown: deleted,
			},
			ev: event("labeled", "front", "preview:pricing", "preview:pricing"),
			want: map[string]float64{
				`doublure_reconciles_total{outcome="skipped"}`:                        1,
				`doublure_reconcile_skips_total{reason="service held by another pr"}`: 1,
				`doublure_teardowns_total{result="deleted",trigger="webhook"}`:        1,
			},
		},
		{
			name: "conflict skip",
			f:    &reconcileFake{pr: fetchedPR("front", true, "preview:a", "preview:b")},
			ev:   event("labeled", "front", "preview:b", "preview:b"),
			want: map[string]float64{
				`doublure_reconciles_total{outcome="skipped"}`:                        1,
				`doublure_reconcile_skips_total{reason="conflicting preview labels"}`: 1,
				`doublure_teardowns_total{result="noop",trigger="webhook"}`:           3,
			},
		},
		{
			name: "catalog failure",
			f:    &reconcileFake{catalogErr: errors.New("boom")},
			ev:   event("labeled", "front", "preview", "preview"),
			want: map[string]float64{`doublure_reconciles_total{outcome="failed"}`: 1},
		},
		{
			name: "preview read failure",
			f:    &reconcileFake{pr: fetchedPR("front", true, "preview:pricing"), previewErr: errors.New("boom")},
			ev:   event("labeled", "front", "preview:pricing", "preview:pricing"),
			want: map[string]float64{
				`doublure_reconciles_total{outcome="failed"}`:               1,
				`doublure_teardowns_total{result="noop",trigger="webhook"}`: 1,
			},
		},
		{
			name: "every write failed",
			f: &reconcileFake{
				pr:       fetchedPR("front", true, "preview"),
				writeErr: map[string]error{"front": errors.New("boom")},
			},
			ev:   event("labeled", "front", "preview", "preview"),
			want: map[string]float64{`doublure_reconciles_total{outcome="failed"}`: 1},
		},
		{
			name: "torn down",
			f:    &reconcileFake{pr: fetchedPR("front", true, "bug"), teardown: deleted},
			ev:   event("labeled", "front", "preview", "preview"),
			want: map[string]float64{
				`doublure_reconciles_total{outcome="torn_down"}`:               1,
				`doublure_teardowns_total{result="deleted",trigger="webhook"}`: 1,
			},
		},
		{
			name: "payload teardown",
			f:    &reconcileFake{catalogErr: errors.New("boom"), teardown: deleted},
			ev:   lifecycle.Event{Target: event("", "front", "").Target, Action: "closed", HeadRef: "feature/x"},
			want: map[string]float64{
				`doublure_reconciles_total{outcome="torn_down"}`:               1,
				`doublure_teardowns_total{result="deleted",trigger="webhook"}`: 1,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			tc.f.Metrics = metrics.New()
			lifecycle.ReconcilePR(context.Background(), tc.f, tc.ev)

			assertMetrics(t, tc.f.Metrics, tc.want,
				"doublure_reconciles_total", "doublure_reconcile_skips_total", "doublure_teardowns_total")
		})
	}
}

func TestTeardownMetrics_Results(t *testing.T) {
	cases := []struct {
		name   string
		f      *reconcileFake
		result string
	}{
		{name: "noop", f: &reconcileFake{}, result: "noop"},
		{name: "error", f: &reconcileFake{teardownErr: errors.New("boom")}, result: "error"},
		{
			name:   "deleted",
			f:      &reconcileFake{teardown: &gitops.TeardownResult{Removed: []string{"front"}, Deleted: true}},
			result: "deleted",
		},
		{
			name:   "unpinned",
			f:      &reconcileFake{teardown: &gitops.TeardownResult{Removed: []string{"front"}}},
			result: "unpinned",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			tc.f.Metrics = metrics.New()
			target := event("", "front", "").Target
			target.Slug = "feature-x"
			lifecycle.TeardownPR(context.Background(), tc.f, target, lifecycle.PRClosedReason, "webhook")

			assertMetrics(t, tc.f.Metrics, map[string]float64{
				`doublure_teardowns_total{result="` + tc.result + `",trigger="webhook"}`: 1,
			}, "doublure_teardowns_total")
		})
	}
}

func TestSweepMetrics(t *testing.T) {
	captureLogs(t)
	f := &fakeAPI{
		Metrics:  metrics.New(),
		label:    "preview",
		previews: []string{"unpinned", "expired", "closed", "open"},
		files: map[string]*preview.PreviewValues{
			"unpinned": values(preview.ServiceEntry{Name: "backend-1"}),
			"expired":  values(pin("backend-1", pr42, stale)),
			"closed":   values(pin("backend-2", pr7, recent)),
			"open":     values(pin("backend-1", pr42, recent)),
		},
		prOpen:    map[string]bool{"acme/backend-1#42": true},
		prHeadRef: map[string]string{"acme/backend-1#42": "open"},
		prLabels:  map[string][]string{"acme/backend-1#42": {"preview"}},
	}
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	assertMetrics(t, f.Metrics, map[string]float64{
		`doublure_previews`: 1,
		`doublure_sweep_previews_deleted_total{rule="no_pin"}`:       1,
		`doublure_sweep_previews_deleted_total{rule="ttl"}`:          1,
		`doublure_teardowns_total{result="deleted",trigger="sweep"}`: 1,
	}, "doublure_previews", "doublure_sweep_previews_deleted_total", "doublure_teardowns_total")
}

func TestSweepMetrics_UnpinOnlyIsASweepTeardown(t *testing.T) {
	captureLogs(t)
	f := openPRAt("feature-x", "preview:other")
	f.Metrics = metrics.New()
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	assertMetrics(t, f.Metrics, map[string]float64{
		`doublure_teardowns_total{result="deleted",trigger="sweep"}`: 1,
	}, "doublure_teardowns_total")
}
