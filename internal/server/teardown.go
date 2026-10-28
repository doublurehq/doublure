package server

import (
	"context"
	"log/slog"
	"time"

	gh "github.com/google/go-github/v71/github"

	"github.com/doublurehq/doublure/internal/lifecycle"
	"github.com/doublurehq/doublure/internal/preview"
)

// reconcileTimeout caps the background work for a single reconcile, shared by
// every service it deploys or by its teardown.
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
// and capped at s.jobTimeout from when it holds s.jobs, and has its panics
// passed to onPanic.
func (s *Server) runJob(ctx context.Context, onPanic func(rec any), job func(context.Context)) {
	s.bg.Go(func() {
		defer func() {
			if rec := recover(); rec != nil {
				onPanic(rec)
			}
		}()
		s.jobs.Lock()
		defer s.jobs.Unlock()
		jctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.jobTimeout)
		defer cancel()
		job(jctx)
	})
}

// startReconcile reconciles the event's PR with its preview in a background
// job. Events missing the fields needed to find the PR, and closed events
// without a head branch, start nothing. A closed event logs as a teardown.
func (s *Server) startReconcile(ctx context.Context, e *gh.PullRequestEvent) {
	var labels []string
	if pr := e.GetPullRequest(); pr != nil {
		for _, l := range pr.Labels {
			labels = append(labels, l.GetName())
		}
	}
	ev := lifecycle.Event{
		Target:  prTarget(e),
		Action:  e.GetAction(),
		HeadRef: e.GetPullRequest().GetHead().GetRef(),
		Label:   e.GetLabel().GetName(),
		Labels:  labels,
	}
	if ev.Action == "closed" {
		s.startClosed(ctx, ev)
		return
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

	s.runJob(ctx, func(rec any) {
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

// startClosed reconciles a closed event, logging as a teardown of the
// payload's head branch.
func (s *Server) startClosed(ctx context.Context, ev lifecycle.Event) {
	slug := preview.Slugify(ev.HeadRef)
	if slug == "" || missingPRFields(ev.Target) {
		slog.InfoContext(ctx, "preview teardown skipped",
			slog.String("slug", slug),
			slog.String("repo", ev.FullName),
			slog.Int("pr_number", ev.Number),
			slog.String("reason", "missing pr fields"),
		)
		return
	}

	s.runJob(ctx, func(rec any) {
		slog.ErrorContext(ctx, "preview teardown panicked",
			slog.String("slug", slug),
			slog.String("repo", ev.FullName),
			slog.Int("pr_number", ev.Number),
			slog.Any("panic", rec),
		)
	}, func(jctx context.Context) {
		lifecycle.ReconcilePR(jctx, s.gitops, ev)
	})
}
