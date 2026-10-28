package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

// actionUnlabeled is the webhook action of a removed label.
const actionUnlabeled = "unlabeled"

// ReconcileAPI is the subset of gitops.Client used by ReconcilePR.
type ReconcileAPI interface {
	DeployAPI
	TeardownAPI
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*gitops.PullRequest, error)
	GetPreview(ctx context.Context, slug string) (*preview.PreviewValues, string, error)
}

// Event is a pull_request webhook delivery to reconcile. Its Target's Slug is
// empty: the slug comes from the PR read from GitHub.
type Event struct {
	Target

	// Action is the webhook action (opened, labeled, ...).
	Action string
	// HeadRef is the PR's head branch in the payload. Only the payload
	// teardown of a closed PR uses it.
	HeadRef string
	// Label is the added or removed label, on labeled and unlabeled.
	Label string
	// Labels holds the names of the PR's labels in the payload.
	Labels []string
}

// ReconcilePR brings the preview in line with the PR's current state on
// GitHub. When the payload shows the event cannot concern a preview label,
// nothing is read. Otherwise the PR is read and its labels resolve its target
// preview (preview.ResolveTarget). The PR is first torn down from every other
// preview it may still be pinned in. Then, when it is open and has a target,
// every catalog service of its repo is deployed there at its head SHA, unless
// another PR already pins one of them. An open PR with conflicting named labels
// is told so. Fork PRs and repos outside the catalog are skipped. Every outcome
// is logged.
//
// A closed event is not guarded by its payload nor skipped for a repo outside
// the catalog, and a closed fork PR is still torn down. When the catalog or
// the PR can't be read, it falls back to tearing down the payload's head
// branch and named labels as closed.
func ReconcilePR(ctx context.Context, api ReconcileAPI, ev Event) {
	closed := ev.Action == "closed"
	catalog, err := api.FetchServiceCatalog(ctx)
	if err != nil {
		if closed {
			payloadTeardown(ctx, api, ev, gitops.DefaultLabel, err)
			return
		}
		reconcileFailed(ctx, ev, err)
		return
	}
	services := catalog.ServicesForRepo(ev.Owner, ev.Repo)
	if !closed {
		if reason := payloadSkipReason(ev, catalog.Label); reason != "" {
			reconcileSkipped(ctx, ev, reason)
			return
		}
		if len(services) == 0 {
			reconcileSkipped(ctx, ev, "repo not in catalog")
			return
		}
	}
	pr, err := api.GetPullRequest(ctx, ev.Owner, ev.Repo, ev.Number)
	if err != nil {
		if closed {
			payloadTeardown(ctx, api, ev, catalog.Label, err)
			return
		}
		reconcileFailed(ctx, ev, err)
		return
	}

	t := preview.ResolveTarget(pr.Labels, pr.HeadRef, catalog.Label)
	logIgnored(ctx, ev, t)
	deploy := pr.Open && t.Slug != ""
	if (deploy || !closed) && !strings.EqualFold(pr.HeadFullName, pr.FullName) {
		reconcileSkipped(ctx, ev, "fork pr")
		return
	}
	if deploy && len(services) == 0 {
		reconcileSkipped(ctx, ev, "repo not in catalog")
		return
	}
	cleanup(ctx, api, ev, pr, t, catalog.Label)
	if refusedByHolder(ctx, api, ev, pr, t, services, catalog.Label) {
		return
	}
	if deploy {
		deployServices(ctx, api, catalog, services, ev, pr, t.Slug)
	}
	postLabelConflict(ctx, api, ev, pr, catalog.Label, t)
}

// logIgnored logs each named label of t whose name has no slug.
func logIgnored(ctx context.Context, ev Event, t preview.Resolution) {
	for _, label := range t.Ignored {
		slog.InfoContext(ctx, "preview label ignored",
			slog.String("repo", ev.FullName),
			slog.Int("pr_number", ev.Number),
			slog.String("label", label),
		)
	}
}

// cleanup tears the PR down from every slug of cleanupSlugs, with
// cleanupReason.
func cleanup(
	ctx context.Context,
	api TeardownAPI,
	ev Event,
	pr *gitops.PullRequest,
	t preview.Resolution,
	label string,
) {
	reason := cleanupReason(t, pr, ev, label)
	for _, slug := range cleanupSlugs(t, pr, ev, label) {
		tt := ev.Target
		tt.Slug = slug
		TeardownPR(ctx, api, tt, reason)
	}
}

// cleanupSlugs returns, sorted and deduplicated, the previews the PR may still
// be pinned in: its branch slug, every named slug of t and, on unlabeled, the
// slug of the removed label. Empty slugs are dropped, and so is t.Slug when the
// PR is deployed there.
func cleanupSlugs(t preview.Resolution, pr *gitops.PullRequest, ev Event, label string) []string {
	branch := preview.Slugify(pr.HeadRef)
	slugs := append([]string{branch}, t.Named...)
	if ev.Action == actionUnlabeled {
		slug, named, plain := preview.ParseLabel(ev.Label, label)
		switch {
		case named:
			slugs = append(slugs, slug)
		case plain:
			slugs = append(slugs, branch)
		}
	}
	deploy := pr.Open && t.Slug != ""
	slugs = slices.DeleteFunc(slugs, func(s string) bool { return s == "" || (deploy && s == t.Slug) })
	slices.Sort(slugs)
	return slices.Compact(slugs)
}

// cleanupReason returns why the PR leaves the previews of cleanupSlugs.
func cleanupReason(t preview.Resolution, pr *gitops.PullRequest, ev Event, label string) string {
	switch {
	case !pr.Open:
		return PRClosedReason
	case ev.Action == actionUnlabeled:
		return labelRemovedReason(ev.Label)
	case t.Conflict:
		return conflictReason(label)
	case t.Slug != "":
		return movedReason(t.Slug)
	}
	return labelRemovedReason(label)
}

func labelRemovedReason(label string) string {
	return fmt.Sprintf("label `%s` removed", label)
}

func conflictReason(label string) string {
	return fmt.Sprintf("conflicting `%s:` labels", label)
}

func movedReason(slug string) string {
	return fmt.Sprintf("moved to preview `%s`", slug)
}

// refusedByHolder reports whether the PR must not be deployed to t.Slug: one
// of its services is already pinned there by another PR (it is then told so),
// or the preview can't be read. It reads nothing unless the PR is open and has
// a target.
func refusedByHolder(
	ctx context.Context,
	api ReconcileAPI,
	ev Event,
	pr *gitops.PullRequest,
	t preview.Resolution,
	services []string,
	label string,
) bool {
	if !pr.Open || t.Slug == "" {
		return false
	}
	vals, _, err := api.GetPreview(ctx, t.Slug)
	if errors.Is(err, gitops.ErrNotFound) {
		return false
	}
	if err != nil {
		reconcileFailed(ctx, ev, err)
		return true
	}
	service, holder, held := heldBy(vals, services, ev.Target)
	if !held {
		return false
	}
	body := preview.BuildServiceHeldSummary(t.Slug, service, holder, label)
	if upsertErr := api.UpsertPRComment(ctx, ev.Owner, ev.Repo, ev.Number, body); upsertErr != nil {
		conflictCommentFailed(ctx, ev, upsertErr)
	}
	reconcileSkipped(ctx, ev, "service held by another pr", slog.String("holder", holder))
	return true
}

// heldBy returns the first entry of vals, in file order, that pins one of
// services for a PR other than self: its service, its holder's PR URL and
// true. It returns false when there is none.
func heldBy(vals *preview.PreviewValues, services []string, self Target) (string, string, bool) {
	for _, svc := range vals.Services {
		if !slices.Contains(services, svc.Name) || svc.CommitSha == "" || svc.Metadata == nil {
			continue
		}
		owner, repo, number, ok := preview.ParsePRURL(svc.Metadata.PRURL)
		if !ok {
			continue
		}
		if !strings.EqualFold(owner, self.Owner) || !strings.EqualFold(repo, self.Repo) || number != self.Number {
			return svc.Name, svc.Metadata.PRURL, true
		}
	}
	return "", "", false
}

// postLabelConflict tells an open PR whose named labels conflict to keep one.
func postLabelConflict(
	ctx context.Context,
	api TeardownAPI,
	ev Event,
	pr *gitops.PullRequest,
	label string,
	t preview.Resolution,
) {
	if !pr.Open || !t.Conflict {
		return
	}
	body := preview.BuildLabelConflictSummary(label, t.NamedLabels)
	if err := api.UpsertPRComment(ctx, ev.Owner, ev.Repo, ev.Number, body); err != nil {
		conflictCommentFailed(ctx, ev, err)
	}
	reconcileSkipped(ctx, ev, "conflicting preview labels")
}

// payloadTeardown tears down a closed PR from its payload alone, after err
// prevented reading the catalog or the PR: from its head branch's slug and
// from the slug of each named label of label in the payload.
func payloadTeardown(ctx context.Context, api TeardownAPI, ev Event, label string, err error) {
	slog.WarnContext(ctx, "preview reconcile fell back to payload teardown",
		slog.String("repo", ev.FullName),
		slog.Int("pr_number", ev.Number),
		slog.String("err", err.Error()),
	)
	branch := preview.Slugify(ev.HeadRef)
	slugs := []string{branch}
	for _, slug := range preview.ResolveTarget(ev.Labels, ev.HeadRef, label).Named {
		if slug != branch {
			slugs = append(slugs, slug)
		}
	}
	for _, slug := range slugs {
		t := ev.Target
		t.Slug = slug
		TeardownPR(ctx, api, t, PRClosedReason)
	}
}

// payloadSkipReason returns why ev cannot concern label or its named labels,
// or "" when the PR must be read.
func payloadSkipReason(ev Event, label string) string {
	switch ev.Action {
	case "labeled", actionUnlabeled:
		if !previewLabel(ev.Label, label) {
			return "other label"
		}
	case "opened", "reopened", "synchronize":
		if !slices.ContainsFunc(ev.Labels, func(l string) bool { return previewLabel(l, label) }) {
			return "not labeled"
		}
	}
	return ""
}

// previewLabel reports whether name is the plain label or a named label of
// label with a non-empty slug.
func previewLabel(name, label string) bool {
	slug, _, plain := preview.ParseLabel(name, label)
	return plain || slug != ""
}

// deployServices deploys each of services, in order, at pr's head SHA in the
// preview slug. A failed service is logged and the next one is still deployed.
func deployServices(
	ctx context.Context,
	api DeployAPI,
	catalog *gitops.ServiceCatalog,
	services []string,
	ev Event,
	pr *gitops.PullRequest,
	slug string,
) {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	for _, service := range services {
		attrs := func(extra ...any) []any {
			return append([]any{
				slog.String("slug", slug),
				slog.String("service", service),
				slog.String("repo", ev.FullName),
				slog.Int("pr_number", ev.Number),
			}, extra...)
		}
		err := ctx.Err()
		var result *gitops.WriteResult
		var fanErr error
		if err == nil {
			result, fanErr, err = DeployPR(ctx, api, catalog, slug, preview.DeployInputs{
				ServiceName:    service,
				HeadRef:        pr.HeadRef,
				CommitSha:      pr.HeadSHA,
				PRAuthor:       pr.Author,
				PRURL:          pr.URL,
				PRNumber:       strconv.Itoa(ev.Number),
				Timestamp:      timestamp,
				WorkflowRunURL: "",
			})
		}
		if err != nil {
			slog.ErrorContext(ctx, "preview deploy failed",
				attrs(slog.String("commit_sha", pr.HeadSHA), slog.String("err", err.Error()))...)
			continue
		}
		slog.InfoContext(ctx, "preview deploy",
			attrs(slog.String("commit_sha", pr.HeadSHA), slog.String("gitops_commit_url", result.CommitURL))...)
		if fanErr != nil {
			slog.WarnContext(ctx, "preview deploy comment failed", attrs(slog.String("err", fanErr.Error()))...)
		}
	}
}

func reconcileSkipped(ctx context.Context, ev Event, reason string, extra ...slog.Attr) {
	args := []any{
		slog.String("repo", ev.FullName),
		slog.Int("pr_number", ev.Number),
		slog.String("action", ev.Action),
		slog.String("reason", reason),
	}
	for _, a := range extra {
		args = append(args, a)
	}
	slog.InfoContext(ctx, "preview reconcile skipped", args...)
}

func conflictCommentFailed(ctx context.Context, ev Event, err error) {
	slog.WarnContext(ctx, "preview conflict comment failed",
		slog.String("repo", ev.FullName),
		slog.Int("pr_number", ev.Number),
		slog.String("err", err.Error()),
	)
}

func reconcileFailed(ctx context.Context, ev Event, err error) {
	slog.ErrorContext(ctx, "preview reconcile failed",
		slog.String("repo", ev.FullName),
		slog.Int("pr_number", ev.Number),
		slog.String("err", err.Error()),
	)
}
