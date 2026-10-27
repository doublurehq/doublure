package server

import (
	"context"
	"log/slog"
	"time"

	gh "github.com/google/go-github/v71/github"

	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/preview"
)

// teardownTimeout caps the background work for a single closed PR: read,
// write or delete, then the closed-PR comment and the remaining PRs' comments.
const teardownTimeout = 20 * time.Second

// startTeardown tears down the closed PR's part of its preview in a background
// goroutine, so the webhook is answered within GitHub's 10-second limit. The
// goroutine is tracked for WaitBackground. Events missing the fields needed to
// find the preview start nothing.
func (s *Server) startTeardown(ctx context.Context, e *gh.PullRequestEvent) {
	tt := lifecycle.Target{
		Slug:     preview.Slugify(e.GetPullRequest().GetHead().GetRef()),
		Owner:    e.GetRepo().GetOwner().GetLogin(),
		Repo:     e.GetRepo().GetName(),
		FullName: e.GetRepo().GetFullName(),
		Number:   e.GetNumber(),
	}
	if tt.Slug == "" || tt.Owner == "" || tt.Repo == "" || tt.Number == 0 {
		slog.InfoContext(ctx, "preview teardown skipped",
			slog.String("slug", tt.Slug),
			slog.String("repo", tt.FullName),
			slog.Int("pr_number", tt.Number),
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
					slog.String("slug", tt.Slug),
					slog.String("repo", tt.FullName),
					slog.Int("pr_number", tt.Number),
					slog.Any("panic", rec),
				)
			}
		}()
		tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), teardownTimeout)
		defer cancel()
		lifecycle.TeardownPR(tctx, s.gitops, tt)
	}()
}
