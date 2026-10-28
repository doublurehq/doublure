package lifecycle

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

// ReconcileAPI is the subset of gitops.Client used by ReconcilePR.
type ReconcileAPI interface {
	DeployAPI
	TeardownAPI
	GetPullRequest(ctx context.Context, owner, repo string, number int) (*gitops.PullRequest, error)
}

// Event is a pull_request webhook delivery to reconcile. Its Target's Slug is
// empty: the slug comes from the PR read from GitHub.
type Event struct {
	Target

	// Action is the webhook action (opened, labeled, ...).
	Action string
	// Label is the added or removed label, on labeled and unlabeled.
	Label string
	// Labels holds the names of the PR's labels in the payload.
	Labels []string
}

// ReconcilePR brings the preview in line with the PR's current state on
// GitHub. When the payload shows the event cannot concern the preview label,
// nothing is read. Otherwise the PR is read: when it is open and carries the
// label, every catalog service of its repo is deployed at its head SHA; when
// it is not, the PR is torn down. Fork PRs and repos outside the catalog are
// skipped. Every outcome is logged.
func ReconcilePR(ctx context.Context, api ReconcileAPI, ev Event) {
	catalog, err := api.FetchServiceCatalog(ctx)
	if err != nil {
		reconcileFailed(ctx, ev, err)
		return
	}
	if reason := payloadSkipReason(ev, catalog.Label); reason != "" {
		reconcileSkipped(ctx, ev, reason)
		return
	}
	services := catalog.ServicesForRepo(ev.Owner, ev.Repo)
	if len(services) == 0 {
		reconcileSkipped(ctx, ev, "repo not in catalog")
		return
	}
	pr, err := api.GetPullRequest(ctx, ev.Owner, ev.Repo, ev.Number)
	if err != nil {
		reconcileFailed(ctx, ev, err)
		return
	}
	if !strings.EqualFold(pr.HeadFullName, pr.FullName) {
		reconcileSkipped(ctx, ev, "fork pr")
		return
	}

	if pr.Open && containsFold(pr.Labels, catalog.Label) {
		deployServices(ctx, api, catalog, services, ev, pr)
		return
	}
	reason := PRClosedReason
	if pr.Open {
		reason = fmt.Sprintf("label `%s` removed", catalog.Label)
	}
	t := ev.Target
	t.Slug = preview.Slugify(pr.HeadRef)
	TeardownPR(ctx, api, t, reason)
}

// payloadSkipReason returns why ev cannot concern label, or "" when the PR
// must be read.
func payloadSkipReason(ev Event, label string) string {
	switch ev.Action {
	case "labeled", "unlabeled":
		if !strings.EqualFold(ev.Label, label) {
			return "other label"
		}
	case "opened", "reopened", "synchronize":
		if !containsFold(ev.Labels, label) {
			return "not labeled"
		}
	}
	return ""
}

// deployServices deploys each of services, in order, at pr's head SHA. A
// failed service is logged and the next one is still deployed.
func deployServices(
	ctx context.Context,
	api DeployAPI,
	catalog *gitops.ServiceCatalog,
	services []string,
	ev Event,
	pr *gitops.PullRequest,
) {
	slug := preview.Slugify(pr.HeadRef)
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
			result, fanErr, err = DeployPR(ctx, api, catalog, preview.DeployInputs{
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

// containsFold reports whether names holds name, compared case-insensitively.
func containsFold(names []string, name string) bool {
	return slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) })
}

func reconcileSkipped(ctx context.Context, ev Event, reason string) {
	slog.InfoContext(ctx, "preview reconcile skipped",
		slog.String("repo", ev.FullName),
		slog.Int("pr_number", ev.Number),
		slog.String("action", ev.Action),
		slog.String("reason", reason),
	)
}

func reconcileFailed(ctx context.Context, ev Event, err error) {
	slog.ErrorContext(ctx, "preview reconcile failed",
		slog.String("repo", ev.FullName),
		slog.Int("pr_number", ev.Number),
		slog.String("err", err.Error()),
	)
}
