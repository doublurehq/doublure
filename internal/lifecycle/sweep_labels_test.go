package lifecycle_test

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/preview"
)

// openPRAt is a sweep fake whose only preview, slug, is pinned by the open PR
// acme/backend-1#42 on branch feature/x with labels.
func openPRAt(slug string, labels ...string) *fakeAPI {
	return &fakeAPI{
		label:     "preview",
		previews:  []string{slug},
		files:     map[string]*preview.PreviewValues{slug: values(pin("backend-1", pr42, recent))},
		prOpen:    map[string]bool{"acme/backend-1#42": true},
		prLabels:  map[string][]string{"acme/backend-1#42": labels},
		prHeadRef: map[string]string{"acme/backend-1#42": "feature/x"},
	}
}

func TestSweep_OpenPRUnpinnedWithoutComment(t *testing.T) {
	cases := []struct {
		name   string
		labels []string
	}{
		{name: "conflict", labels: []string{"preview:pricing", "preview:other"}},
		{name: "moved to a named preview", labels: []string{"preview:other"}},
		{name: "moved to its branch preview", labels: []string{"preview"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			f := openPRAt("pricing", tc.labels...)
			if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			want := []teardownCall{{slug: "pricing", owner: "acme", repo: "backend-1", number: 42}}
			if !slices.Equal(f.teardowns, want) {
				t.Errorf("teardowns = %+v, want %+v", f.teardowns, want)
			}
			if len(f.upserts) != 0 {
				t.Errorf("upserts = %v, want none", f.upserts)
			}
		})
	}
}

func TestSweep_OpenPRLabelGoneTearsDown(t *testing.T) {
	captureLogs(t)
	f := openPRAt("pricing", "bug")
	if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(f.teardowns) != 1 || f.teardowns[0].slug != "pricing" {
		t.Errorf("teardowns = %+v, want one from pricing", f.teardowns)
	}
	wantBody := "## 🧹 Preview: `pricing`\n\n" +
		"Preview deleted (label `preview` removed): no other PR is pinned.\n\n" +
		"---\n" +
		"🔗 [chore(preview): delete pricing preview](" + sweepCommitURL + ")"
	if len(f.bodies) != 1 || f.bodies[0] != wantBody {
		t.Errorf("bodies = %q, want one with body:\n%s", f.bodies, wantBody)
	}
}

func TestSweep_OpenPRLeftAlone(t *testing.T) {
	cases := []struct {
		name   string
		slug   string
		labels []string
	}{
		{name: "unlabeled at its branch slug", slug: "feature-x"},
		{name: "plain label at its branch slug", slug: "feature-x", labels: []string{"preview"}},
		{name: "named label at its slug", slug: "pricing", labels: []string{"preview", "Preview:Pricing"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			f := openPRAt(tc.slug, tc.labels...)
			if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
				t.Fatalf("Sweep: %v", err)
			}
			if len(f.teardowns) != 0 || len(f.upserts) != 0 {
				t.Errorf("teardowns = %+v, upserts = %v; want none", f.teardowns, f.upserts)
			}
		})
	}
}

func TestSweep_NoLabelRunsOnlyTheClosedRule(t *testing.T) {
	cases := []struct {
		name       string
		label      string
		catalogErr error
	}{
		{name: "catalog error", label: "preview", catalogErr: errors.New("catalog boom")},
		{name: "empty label"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			capture := captureLogs(t)
			f := openPRAt("pricing", "preview:other")
			f.label, f.catalogErr = tc.label, tc.catalogErr
			f.files["pricing"] = values(pin("backend-1", pr42, recent), pin("backend-2", pr7, recent))
			if err := lifecycle.Sweep(context.Background(), f, now, ttl); err != nil {
				t.Fatalf("Sweep: %v (a catalog error is not a failure)", err)
			}
			// backend-1#42 is open but moved: left alone. backend-2#7 is closed.
			want := []teardownCall{{slug: "pricing", owner: "acme", repo: "backend-2", number: 7}}
			if !slices.Equal(f.teardowns, want) {
				t.Errorf("teardowns = %+v, want %+v", f.teardowns, want)
			}
			warns := capture.find(slog.LevelWarn, "cleanup catalog unavailable")
			if tc.catalogErr != nil && (len(warns) != 1 || warns[0]["err"].String() != "catalog boom") {
				t.Errorf("catalog unavailable logs = %v", warns)
			}
			if tc.catalogErr == nil && len(warns) != 0 {
				t.Errorf("catalog unavailable logs = %v, want none", warns)
			}
			if n := capture.count(slog.LevelWarn); n != len(warns) {
				t.Errorf("%d WARN records, want only cleanup catalog unavailable", n)
			}
		})
	}
}
