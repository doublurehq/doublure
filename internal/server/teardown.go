package server

import (
	"context"
	"log/slog"
	"strings"
	"time"

	gh "github.com/google/go-github/v71/github"

	"github.com/doublurehq/doublure/internal/preview"
)

// teardownTimeout caps the background work for a single closed PR: read,
// write or delete, then the closed-PR comment and the remaining PRs' comments.
const teardownTimeout = 20 * time.Second

// teardownTarget identifies the preview and the closed PR to tear down.
type teardownTarget struct {
	slug     string
	owner    string
	repo     string
	fullName string
	number   int
}

// startTeardown tears down the closed PR's part of its preview in a background
// goroutine, so the webhook is answered within GitHub's 10-second limit. The
// goroutine is tracked for WaitBackground. Events missing the fields needed to
// find the preview start nothing.
func (s *Server) startTeardown(ctx context.Context, e *gh.PullRequestEvent) {
	tt := teardownTarget{
		slug:     preview.Slugify(e.GetPullRequest().GetHead().GetRef()),
		owner:    e.GetRepo().GetOwner().GetLogin(),
		repo:     e.GetRepo().GetName(),
		fullName: e.GetRepo().GetFullName(),
		number:   e.GetNumber(),
	}
	if tt.slug == "" || tt.owner == "" || tt.repo == "" || tt.number == 0 {
		slog.InfoContext(ctx, "preview teardown skipped",
			slog.String("slug", tt.slug),
			slog.String("repo", tt.fullName),
			slog.Int("pr_number", tt.number),
			slog.String("reason", "missing pr fields"),
		)
		return
	}

	s.bg.Add(1)
	go func() {
		defer s.bg.Done()
		defer func() {
			if rec := recover(); rec != nil {
				slog.ErrorContext(ctx, "preview teardown panicked",
					slog.String("slug", tt.slug),
					slog.String("repo", tt.fullName),
					slog.Int("pr_number", tt.number),
					slog.Any("panic", rec),
				)
			}
		}()
		tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), teardownTimeout)
		defer cancel()
		s.teardown(tctx, tt)
	}()
}

// teardown unpins the closed PR from its preview (or deletes the preview when
// no pin remains), then edits the closed PR's comment and, when the preview
// survives, refreshes the remaining PRs' summaries. Comment failures are
// logged and never undo the teardown.
func (s *Server) teardown(ctx context.Context, tt teardownTarget) {
	result, err := s.gitops.TeardownPreviewWithRetry(ctx, tt.slug, tt.owner, tt.repo, tt.number)
	if err != nil {
		slog.ErrorContext(ctx, "preview teardown failed",
			slog.String("slug", tt.slug),
			slog.String("repo", tt.fullName),
			slog.Int("pr_number", tt.number),
			slog.String("err", err.Error()),
		)
		return
	}
	if len(result.Removed) == 0 {
		slog.InfoContext(ctx, "preview teardown skipped",
			slog.String("slug", tt.slug),
			slog.String("repo", tt.fullName),
			slog.Int("pr_number", tt.number),
			slog.String("reason", "no preview or no matching service"),
		)
		return
	}
	slog.InfoContext(ctx, "preview teardown",
		slog.String("slug", tt.slug),
		slog.String("repo", tt.fullName),
		slog.Int("pr_number", tt.number),
		slog.String("removed", strings.Join(result.Removed, ", ")),
		slog.Bool("deleted", result.Deleted),
		slog.String("gitops_commit_url", result.CommitURL),
	)

	body := preview.BuildTeardownSummary(
		tt.slug, result.Removed, result.Deleted, result.CommitMessage, result.CommitURL,
	)
	if upsertErr := s.gitops.UpsertPRComment(ctx, tt.owner, tt.repo, tt.number, body); upsertErr != nil {
		teardownCommentFailed(ctx, tt, upsertErr)
	}
	if result.Deleted {
		return
	}

	catalog, err := s.gitops.FetchServiceCatalog(ctx)
	if err != nil {
		teardownCommentFailed(ctx, tt, err)
		return
	}
	summary := preview.BuildSummary(tt.slug, result.Values, catalog.URLs, result.CommitMessage, result.CommitURL)
	if fanErr := s.gitops.FanOutPRComments(ctx, result.Values, summary); fanErr != nil {
		teardownCommentFailed(ctx, tt, fanErr)
	}
}

// teardownCommentFailed logs a comment or catalog failure after a successful
// teardown write.
func teardownCommentFailed(ctx context.Context, tt teardownTarget, err error) {
	slog.WarnContext(ctx, "preview teardown comment failed",
		slog.String("slug", tt.slug),
		slog.String("repo", tt.fullName),
		slog.Int("pr_number", tt.number),
		slog.String("err", err.Error()),
	)
}
