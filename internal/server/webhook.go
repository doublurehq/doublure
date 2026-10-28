package server

import (
	"context"
	"log/slog"
	"net/http"

	gh "github.com/google/go-github/v71/github"
)

// installationEvent is implemented by go-github event types that carry the
// installation the delivery was sent for.
type installationEvent interface {
	GetInstallation() *gh.Installation
}

// handleWebhook validates the signature, parses the payload into a typed
// event, checks that it comes from the configured installation, and
// dispatches to a per-event handler. Deliveries from any other installation,
// or without one, get 403; ping is exempt because it carries no installation.
// Parse and dispatch failures are logged and answered with 200: the delivery
// is authentic and there is nothing to re-queue, so a non-2xx would only flag
// it as failed in Recent Deliveries. GitHub does not automatically redeliver
// failed deliveries.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	eventType := r.Header.Get("X-Github-Event")
	ctx := r.Context()

	payload, err := s.webhook.ValidateWebhook(r)
	if err != nil {
		// A rejected delivery means a misconfigured secret or someone probing
		// the public endpoint with forged requests — worth surfacing. Log the
		// source and event type only; never the (untrusted) payload.
		slog.WarnContext(ctx, "webhook signature rejected",
			slog.String("event", eventType),
			slog.String("remote_addr", r.RemoteAddr),
		)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	event, err := s.webhook.ParseEvent(eventType, payload)
	if err != nil {
		slog.ErrorContext(ctx, "webhook parse failed",
			slog.String("event", eventType), slog.String("err", err.Error()))
		w.WriteHeader(http.StatusOK)
		return
	}

	if eventType != "ping" {
		var inst *gh.Installation
		if ie, ok := event.(installationEvent); ok {
			inst = ie.GetInstallation()
		}
		if inst.GetID() == 0 || inst.GetID() != s.allowedInstallationID {
			slog.WarnContext(ctx, "webhook installation rejected",
				slog.String("event", eventType),
				slog.Int64("installation_id", inst.GetID()),
				slog.String("account", inst.GetAccount().GetLogin()),
			)
			http.Error(w, "installation not allowed", http.StatusForbidden)
			return
		}
	}

	// Backstop: go-github event structs are pointer-heavy; a handler that
	// dereferences a missing optional field would panic and, without recovery,
	// surface as a 500 that marks the delivery failed. Recover, log, 200.
	defer func() {
		if rec := recover(); rec != nil {
			slog.ErrorContext(ctx, "webhook handler panicked",
				slog.String("event", eventType), slog.Any("panic", rec))
		}
		w.WriteHeader(http.StatusOK)
	}()

	s.dispatchEvent(ctx, eventType, event)
}

// dispatchEvent routes a parsed event to its handler. Unknown event types are
// logged at WARN (GitHub adds event types over time; that's not an error).
func (s *Server) dispatchEvent(ctx context.Context, eventType string, event any) {
	switch e := event.(type) {
	case *gh.PingEvent:
		s.handlePing(ctx, e)
	case *gh.InstallationEvent:
		s.handleInstallation(ctx, e)
	case *gh.InstallationRepositoriesEvent:
		s.handleInstallationRepositories(ctx, e)
	case *gh.PullRequestEvent:
		s.handlePullRequest(ctx, e)
	default:
		slog.WarnContext(ctx, "unhandled webhook event", slog.String("event", eventType))
	}
}

func (s *Server) handlePing(ctx context.Context, e *gh.PingEvent) {
	slog.InfoContext(ctx, "webhook ping",
		slog.String("zen", e.GetZen()),
		slog.Int64("hook_id", e.GetHookID()),
	)
}

func (s *Server) handleInstallation(ctx context.Context, e *gh.InstallationEvent) {
	slog.InfoContext(ctx, "webhook installation",
		slog.String("action", e.GetAction()),
		slog.Int64("installation_id", e.GetInstallation().GetID()),
		slog.String("account", e.GetInstallation().GetAccount().GetLogin()),
		slog.Int("repositories", len(e.Repositories)),
	)
}

func (s *Server) handleInstallationRepositories(ctx context.Context, e *gh.InstallationRepositoriesEvent) {
	slog.InfoContext(ctx, "webhook installation_repositories",
		slog.String("action", e.GetAction()),
		slog.Int64("installation_id", e.GetInstallation().GetID()),
		slog.Int("repositories_added", len(e.RepositoriesAdded)),
		slog.Int("repositories_removed", len(e.RepositoriesRemoved)),
	)
}

func (s *Server) handlePullRequest(ctx context.Context, e *gh.PullRequestEvent) {
	slog.InfoContext(ctx, "webhook pull_request",
		slog.String("action", e.GetAction()),
		slog.String("repo", e.GetRepo().GetFullName()),
		slog.Int("pr_number", e.GetNumber()),
		slog.String("head_ref", e.GetPullRequest().GetHead().GetRef()),
		slog.String("head_sha", e.GetPullRequest().GetHead().GetSHA()),
		slog.String("pr_author", e.GetPullRequest().GetUser().GetLogin()),
	)
	switch e.GetAction() {
	case "opened", "reopened", "synchronize", "labeled", "unlabeled", "closed":
		s.startReconcile(ctx, e)
	}
}
