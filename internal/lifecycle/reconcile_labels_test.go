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

// removedFront is the teardown result of a PR that pinned front in slug, the
// preview's last pin.
func removedFront(slug string) *gitops.TeardownResult {
	return &gitops.TeardownResult{
		Removed:       []string{"front"},
		Deleted:       true,
		CommitURL:     sweepCommitURL,
		CommitMessage: "chore(preview): delete " + slug + " preview",
	}
}

// deletedBody is the comment of a teardown with removedFront(slug).
func deletedBody(slug, reason string) string {
	return "## 🧹 Preview: `" + slug + "`\n\n" +
		"Preview deleted (" + reason + "): no other PR is pinned.\n\n" +
		"---\n" +
		"🔗 [chore(preview): delete " + slug + " preview](" + sweepCommitURL + ")"
}

func TestReconcile_NamedLabelSharesPreviewAcrossRepos(t *testing.T) {
	captureLogs(t)
	f := &reconcileFake{}

	f.pr = fetchedPR("front", true, "preview:pricing")
	f.pr.HeadRef = "x"
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "preview:pricing", "preview:pricing"))
	// The second PR reads the preview the first one pinned front in.
	f.previews = map[string]*preview.PreviewValues{"pricing": {Services: []preview.ServiceEntry{{
		Name:      "front",
		CommitSha: fetchedSHA,
		Metadata:  &preview.Metadata{PRURL: "https://github.com/acme/front/pull/42"},
	}}}}
	f.pr = fetchedPR("monorepo", true, "Preview:Pricing")
	f.pr.HeadRef = "y"
	f.calls, f.upserts = nil, nil
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "monorepo", "Preview:Pricing", "Preview:Pricing"))
	if !slices.Contains(f.calls, "get:pricing") {
		t.Errorf("calls = %v, want the holder check to read pricing", f.calls)
	}
	if len(f.upserts) != 0 {
		t.Errorf("upserts = %v, want none: front is not a monorepo service", f.upserts)
	}

	if got := services(f.writes); !slices.Equal(got, []string{"front", "api", "worker"}) {
		t.Fatalf("deployed = %v, want [front api worker]", got)
	}
	for _, w := range f.writes {
		if w.slug != "pricing" {
			t.Errorf("write %s to %q, want pricing", w.service, w.slug)
		}
	}
	if f.writes[0].inputs.HeadRef != "x" || f.writes[1].inputs.HeadRef != "y" {
		t.Errorf("head refs = %q, %q; want x, y", f.writes[0].inputs.HeadRef, f.writes[1].inputs.HeadRef)
	}

	// front moves to another preview: only front's PR is torn down.
	f.teardowns = nil
	f.pr = fetchedPR("front", true, "preview:other")
	f.pr.HeadRef = "x"
	lifecycle.ReconcilePR(context.Background(), f, event("unlabeled", "front", "preview:pricing", "preview:other"))
	want := []teardownCall{
		{slug: "pricing", owner: "acme", repo: "front", number: 42},
		{slug: "x", owner: "acme", repo: "front", number: 42},
	}
	if !slices.Equal(f.teardowns, want) {
		t.Errorf("teardowns = %+v, want %+v", f.teardowns, want)
	}
}

func TestReconcile_Moves(t *testing.T) {
	cases := []struct {
		name    string
		ev      lifecycle.Event
		labels  []string
		removed string // the slug whose teardown comments
		reason  string
		calls   []string
	}{
		{
			name:    "plain to named",
			ev:      event("labeled", "front", "preview:b", "preview", "preview:b"),
			labels:  []string{"preview", "preview:b"},
			removed: "feature-pricing",
			reason:  "moved to preview `b`",
			calls:   []string{"teardown:feature-pricing", "upsert", "get:b", "write:b", "fanout"},
		},
		{
			name:    "named to named",
			ev:      event("unlabeled", "front", "preview:a", "preview:b"),
			labels:  []string{"preview:b"},
			removed: "a",
			reason:  "label `preview:a` removed",
			calls:   []string{"teardown:a", "upsert", "teardown:feature-pricing", "get:b", "write:b", "fanout"},
		},
		{
			name:    "named to plain",
			ev:      event("unlabeled", "front", "preview:a", "preview"),
			labels:  []string{"preview"},
			removed: "a",
			reason:  "label `preview:a` removed",
			calls:   []string{"teardown:a", "upsert", "get:feature-pricing", "write:feature-pricing", "fanout"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			f := &reconcileFake{
				pr:             fetchedPR("front", true, tc.labels...),
				teardownBySlug: map[string]*gitops.TeardownResult{tc.removed: removedFront(tc.removed)},
			}
			lifecycle.ReconcilePR(context.Background(), f, tc.ev)

			if !slices.Equal(f.calls, tc.calls) {
				t.Errorf("calls = %v, want %v", f.calls, tc.calls)
			}
			for _, td := range f.teardowns {
				if td.owner != "acme" || td.repo != "front" || td.number != 42 {
					t.Errorf("teardown %+v, want acme/front#42 only", td)
				}
			}
			if want := deletedBody(tc.removed, tc.reason); len(f.upserts) != 1 || f.upserts[0] != want {
				t.Errorf("upserts = %q, want one with body:\n%s", f.upserts, want)
			}
		})
	}
}

func TestReconcile_ConflictingNamedLabels(t *testing.T) {
	capture := captureLogs(t)
	f := &reconcileFake{
		pr:             fetchedPR("front", true, "preview:b", "Preview:A", "preview"),
		teardownBySlug: map[string]*gitops.TeardownResult{"a": removedFront("a")},
	}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "preview:b", "preview:b"))

	want := []string{"teardown:a", "upsert", "teardown:b", "teardown:feature-pricing", "upsert"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	wantConflict := "## ⚠️ Preview: conflicting labels\n\n" +
		"Keep one `preview:` label. This PR has `Preview:A`, `preview:b`."
	if len(f.upserts) != 2 || f.upserts[0] != deletedBody("a", "conflicting `preview:` labels") ||
		f.upserts[1] != wantConflict {
		t.Errorf("upserts = %q", f.upserts)
	}
	logs := capture.find(slog.LevelInfo, "preview reconcile skipped")
	if len(logs) != 1 || logs[0]["reason"].String() != "conflicting preview labels" {
		t.Errorf("skip logs = %v", logs)
	}
}

func TestReconcile_ConflictCommentFailure(t *testing.T) {
	capture := captureLogs(t)
	f := &reconcileFake{
		pr:        fetchedPR("front", true, "preview:a", "preview:b"),
		upsertErr: errors.New("comment boom"),
	}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "preview:b", "preview:b"))

	logs := capture.find(slog.LevelWarn, "preview conflict comment failed")
	if len(logs) != 1 || logs[0]["err"].String() != "comment boom" || logs[0]["repo"].String() != "acme/front" ||
		logs[0]["pr_number"].Int64() != 42 {
		t.Errorf("conflict comment failed logs = %v", logs)
	}
}

func TestReconcile_ClosedWithNamedLabel(t *testing.T) {
	captureLogs(t)
	f := &reconcileFake{
		pr:             fetchedPR("front", false, "preview:a"),
		teardownBySlug: map[string]*gitops.TeardownResult{"a": removedFront("a")},
	}
	lifecycle.ReconcilePR(context.Background(), f, event("closed", "front", "", "preview:a"))

	if want := []string{"teardown:a", "upsert", "teardown:feature-pricing"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	if want := deletedBody("a", lifecycle.PRClosedReason); len(f.upserts) != 1 || f.upserts[0] != want {
		t.Errorf("upserts = %q, want one with body:\n%s", f.upserts, want)
	}
}

func TestReconcile_PayloadTeardownWithNamedLabel(t *testing.T) {
	cases := []struct {
		name string
		f    *reconcileFake
	}{
		{name: "catalog error", f: &reconcileFake{catalogErr: errors.New("boom")}},
		{name: "pull request error", f: &reconcileFake{prErr: errors.New("boom")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			ev := event("closed", "front", "", "preview:a", "preview:feature-pricing", "bug")
			ev.HeadRef = "feature/pricing"
			lifecycle.ReconcilePR(context.Background(), tc.f, ev)

			if want := []string{"teardown:feature-pricing", "teardown:a"}; !slices.Equal(tc.f.calls, want) {
				t.Errorf("calls = %v, want %v", tc.f.calls, want)
			}
		})
	}
}

func TestReconcile_IgnoredNamedLabelLogged(t *testing.T) {
	capture := captureLogs(t)
	f := &reconcileFake{pr: fetchedPR("front", true, "preview", "preview:")}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "preview", "preview", "preview:"))

	if want := []string{"get:feature-pricing", "write:feature-pricing", "fanout"}; !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	logs := capture.find(slog.LevelInfo, "preview label ignored")
	if len(logs) != 1 || logs[0]["label"].String() != "preview:" || logs[0]["repo"].String() != "acme/front" ||
		logs[0]["pr_number"].Int64() != 42 {
		t.Errorf("label ignored logs = %v", logs)
	}
}

func TestReconcile_ServiceHeldByAnotherPR(t *testing.T) {
	capture := captureLogs(t)
	holder := "https://github.com/acme/front/pull/7"
	f := &reconcileFake{
		pr: fetchedPR("front", true, "preview:pricing"),
		previews: map[string]*preview.PreviewValues{"pricing": values(
			preview.ServiceEntry{Name: "api"},
			pin("front", holder, recent),
		)},
	}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "preview:pricing", "preview:pricing"))

	want := []string{"teardown:feature-pricing", "get:pricing", "upsert"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls = %v, want %v", f.calls, want)
	}
	wantBody := "## ⚠️ Preview: `pricing`\n\n" +
		"`front` is already pinned in this preview by " + holder + ". " +
		"Once that pull request leaves this preview, push to this pull request or re-add its label. " +
		"If that pull request is closed, or now carries a different `preview:` label, " +
		"it is released within the next cleanup run."
	if len(f.upserts) != 1 || f.upserts[0] != wantBody {
		t.Errorf("upserts = %q, want one with body:\n%s", f.upserts, wantBody)
	}
	logs := capture.find(slog.LevelInfo, "preview reconcile skipped")
	if len(logs) != 1 || logs[0]["reason"].String() != "service held by another pr" ||
		logs[0]["holder"].String() != holder || logs[0]["repo"].String() != "acme/front" {
		t.Errorf("skip logs = %v", logs)
	}
}

func TestReconcile_HolderCheckDeploys(t *testing.T) {
	cases := []struct {
		name string
		file *preview.PreviewValues
	}{
		{name: "own pin", file: values(pin("front", "https://github.com/ACME/Front/pull/42", recent))},
		{name: "other service", file: values(pin("api", "https://github.com/acme/monorepo/pull/7", recent))},
		{name: "unparsable pr-url", file: values(pin("front", "not-a-pr-url", recent))},
		{name: "no preview"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			captureLogs(t)
			f := &reconcileFake{pr: fetchedPR("front", true, "preview:pricing")}
			if tc.file != nil {
				f.previews = map[string]*preview.PreviewValues{"pricing": tc.file}
			}
			lifecycle.ReconcilePR(
				context.Background(),
				f,
				event("labeled", "front", "preview:pricing", "preview:pricing"),
			)

			if len(f.writes) != 1 || f.writes[0].slug != "pricing" {
				t.Errorf("writes = %+v, want one to pricing", f.writes)
			}
		})
	}
}

func TestReconcile_HolderCheckReadError(t *testing.T) {
	capture := captureLogs(t)
	f := &reconcileFake{pr: fetchedPR("front", true, "preview:pricing"), previewErr: errors.New("read boom")}
	lifecycle.ReconcilePR(context.Background(), f, event("labeled", "front", "preview:pricing", "preview:pricing"))

	if len(f.writes) != 0 || len(f.upserts) != 0 {
		t.Errorf("writes = %+v, upserts = %q; want none", f.writes, f.upserts)
	}
	logs := capture.find(slog.LevelError, "preview reconcile failed")
	if len(logs) != 1 || logs[0]["err"].String() != "read boom" {
		t.Errorf("reconcile failed logs = %v", logs)
	}
}
