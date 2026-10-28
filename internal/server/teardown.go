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

// reconcileTimeout caps the background work for a single reconcile, shared by
// every service it deploys.
const reconcileTimeout = 20 * time.Second

// prTarget builds the Target of a pull_request event, without its slug.
func prTarget(e *gh.PullRequestEvent) lifecycle.Target {
	return lifecycle.Target{
		Owner:    e.GetRepo().GetOwner().GetLogin(),
		Repo:     e.GetRepo().GetName(),
		FullName: e.GetRepo().GetFullName(),
		Number:   e.GetNumber(),
	}
}

// missingPRFields reports whether t lacks the fields needed to find its PR.
func missingPRFields(t lifecycle.Target) bool {
	return t.Owner == "" || t.Repo == "" || t.Number == 0
}

// runJob runs job in the background, so the webhook is answered within
// GitHub's 10-second limit. The job is tracked for WaitBackground, runs once
// no other job holds s.jobs, gets a context detached from ctx's cancellation
// and capped at timeout, and has its panics passed to onPanic.
func (s *Server) runJob(ctx context.Context, timeout time.Duration, onPanic func(rec any), job func(context.Context)) {
	s.bg.Go(func() {
		defer func() {
			if rec := recover(); rec != nil {
				onPanic(rec)
			}
		}()
		s.jobs.Lock()
		defer s.jobs.Unlock()
		jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		job(jctx)
	})
}

// startTeardown tears down the closed PR's part of its preview in a background
// job. Events missing the fields needed to find the preview start nothing.
func (s *Server) startTeardown(ctx context.Context, e *gh.PullRequestEvent) {
	tt := prTarget(e)
	tt.Slug = preview.Slugify(e.GetPullRequest().GetHead().GetRef())
	if tt.Slug == "" || missingPRFields(tt) {
		slog.InfoContext(ctx, "preview teardown skipped",
			slog.String("slug", tt.Slug),
			slog.String("repo", tt.FullName),
			slog.Int("pr_number", tt.Number),
			slog.String("reason", "missing pr fields"),
		)
		return
	}

	s.runJob(ctx, teardownTimeout, func(rec any) {
		slog.ErrorContext(ctx, "preview teardown panicked",
			slog.String("slug", tt.Slug),
			slog.String("repo", tt.FullName),
			slog.Int("pr_number", tt.Number),
			slog.Any("panic", rec),
		)
	}, func(jctx context.Context) {
		lifecycle.TeardownPR(jctx, s.gitops, tt, lifecycle.PRClosedReason)
	})
}

// startReconcile reconciles the event's PR with its preview in a background
// job. Events missing the fields needed to find the PR start nothing.
func (s *Server) startReconcile(ctx context.Context, e *gh.PullRequestEvent) {
	labels := make([]string, 0, len(e.GetPullRequest().Labels))
	for _, l := range e.GetPullRequest().Labels {
		labels = append(labels, l.GetName())
	}
	ev := lifecycle.Event{
		Target: prTarget(e),
		Action: e.GetAction(),
		Label:  e.GetLabel().GetName(),
		Labels: labels,
	}
	if missingPRFields(ev.Target) {
		slog.InfoContext(ctx, "preview reconcile skipped",
			slog.String("repo", ev.FullName),
			slog.Int("pr_number", ev.Number),
			slog.String("action", ev.Action),
			slog.String("reason", "missing pr fields"),
		)
		return
	}

	s.runJob(ctx, reconcileTimeout, func(rec any) {
		slog.ErrorContext(ctx, "preview reconcile panicked",
			slog.String("repo", ev.FullName),
			slog.Int("pr_number", ev.Number),
			slog.String("action", ev.Action),
			slog.Any("panic", rec),
		)
	}, func(jctx context.Context) {
		lifecycle.ReconcilePR(jctx, s.gitops, ev)
	})
}
