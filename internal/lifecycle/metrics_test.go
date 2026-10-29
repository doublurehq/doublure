package lifecycle_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/metrics"
	"github.com/doublurehq/doublure/internal/preview"
)

// assertMetrics fails t unless the samples of m's metrics named names are
// exactly want, keyed by name and labels as exposed.
func assertMetrics(t *testing.T, m *metrics.Metrics, want map[string]float64, names ...string) {
	t.Helper()
	// headers holds the HELP and TYPE lines of the metrics these tests assert.
	headers := map[string]string{
		"doublure_reconciles_total": "# HELP doublure_reconciles_total Pull request reconciles, by outcome.\n" +
			"# TYPE doublure_reconciles_total counter\n",
		"doublure_reconcile_skips_total": "# HELP doublure_reconcile_skips_total Skipped pull request reconciles, by reason.\n" +
			"# TYPE doublure_reconcile_skips_total counter\n",
		"doublure_teardowns_total": "# HELP doublure_teardowns_total Preview teardowns of a pull request, by trigger and result.\n" +
			"# TYPE doublure_teardowns_total counter\n",
		"doublure_previews": "# HELP doublure_previews Previews in the gitops repo as of the last in-server cleanup sweep, " +
			"0 before the first one.\n# TYPE doublure_previews gauge\n",
		"doublure_sweep_previews_deleted_total": "# HELP doublure_sweep_previews_deleted_total " +
			"Previews deleted by the cleanup sweep, by rule.\n# TYPE doublure_sweep_previews_deleted_total counter\n",
	}
	var expected strings.Builder
	matched := 0
	for _, name := range names {
		var lines strings.Builder
		for key, v := range want {
			if key == name || strings.HasPrefix(key, name+"{") {
				fmt.Fprintf(&lines, "%s %v\n", key, v)
				matched++
			}
		}
		if lines.Len() > 0 {
			expected.WriteString(headers[name] + lines.String())
		}
	}
	if matched != len(want) {
		t.Fatalf("want %v names a metric outside %v", want, names)
	}
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected.String()), names...); err != nil {
		t.Error(err)
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
