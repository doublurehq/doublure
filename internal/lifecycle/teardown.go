package lifecycle

import (
	"context"
	"log/slog"
	"strings"

	"github.com/doublurehq/doublure/internal/gitops"
	"github.com/doublurehq/doublure/internal/preview"
)

// TeardownAPI is the subset of gitops.Client used by TeardownPR.
type TeardownAPI interface {
	TeardownPreviewWithRetry(
		ctx context.Context,
		slug, owner, repo string,
		prNumber int,
	) (*gitops.TeardownResult, error)
	UpsertPRComment(ctx context.Context, owner, repo string, prNumber int, body string) error
	FetchServiceCatalog(ctx context.Context) (*gitops.ServiceCatalog, error)
	FanOutPRComments(ctx context.Context, vals preview.PreviewValues, body string) error
}

// Target identifies the preview and the closed PR to tear down.
type Target struct {
	Slug     string
	Owner    string
	Repo     string
	FullName string
	Number   int
}

// TeardownPR unpins the closed PR from its preview (or deletes the preview when
// no pin remains), then edits the closed PR's comment and, when the preview
// survives, refreshes the remaining PRs' summaries. Comment failures are
// logged and never undo the teardown.
func TeardownPR(ctx context.Context, api TeardownAPI, tt Target) {
	result, err := api.TeardownPreviewWithRetry(ctx, tt.Slug, tt.Owner, tt.Repo, tt.Number)
	if err != nil {
		slog.ErrorContext(ctx, "preview teardown failed",
			slog.String("slug", tt.Slug),
			slog.String("repo", tt.FullName),
			slog.Int("pr_number", tt.Number),
			slog.String("err", err.Error()),
		)
		return
	}
	if len(result.Removed) == 0 {
		slog.InfoContext(ctx, "preview teardown skipped",
			slog.String("slug", tt.Slug),
			slog.String("repo", tt.FullName),
			slog.Int("pr_number", tt.Number),
			slog.String("reason", "no preview or no matching service"),
		)
		return
	}
	slog.InfoContext(ctx, "preview teardown",
		slog.String("slug", tt.Slug),
		slog.String("repo", tt.FullName),
		slog.Int("pr_number", tt.Number),
		slog.String("removed", strings.Join(result.Removed, ", ")),
		slog.Bool("deleted", result.Deleted),
		slog.String("gitops_commit_url", result.CommitURL),
	)

	body := preview.BuildTeardownSummary(
		tt.Slug, result.Removed, result.Deleted, result.CommitMessage, result.CommitURL,
	)
	if upsertErr := api.UpsertPRComment(ctx, tt.Owner, tt.Repo, tt.Number, body); upsertErr != nil {
		teardownCommentFailed(ctx, tt, upsertErr)
	}
	if result.Deleted {
		return
	}

	catalog, err := api.FetchServiceCatalog(ctx)
	if err != nil {
		teardownCommentFailed(ctx, tt, err)
		return
	}
	summary := preview.BuildSummary(tt.Slug, result.Values, catalog.URLs, result.CommitMessage, result.CommitURL)
	if fanErr := api.FanOutPRComments(ctx, result.Values, summary); fanErr != nil {
		teardownCommentFailed(ctx, tt, fanErr)
	}
}

// teardownCommentFailed logs a comment or catalog failure after a successful
// teardown write.
func teardownCommentFailed(ctx context.Context, tt Target, err error) {
	slog.WarnContext(ctx, "preview teardown comment failed",
		slog.String("slug", tt.Slug),
		slog.String("repo", tt.FullName),
		slog.Int("pr_number", tt.Number),
		slog.String("err", err.Error()),
	)
}
