package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// WebhookValidator validates GitHub webhook signatures and parses payloads
// into typed event structs.
type WebhookValidator interface {
	ValidateWebhook(r *http.Request) ([]byte, error)
	ParseEvent(eventType string, payload []byte) (any, error)
}

type Server struct {
	webhook     WebhookValidator
	gitops      gitopsAPI
	deployToken string
	// allowedInstallationID is the only GitHub App installation whose webhook
	// deliveries are dispatched.
	allowedInstallationID int64
	router                chi.Router
	// bg tracks background work (PR-close teardowns) for WaitBackground.
	bg sync.WaitGroup
}

func NewServer(webhook WebhookValidator, gitops gitopsAPI, deployToken string, allowedInstallationID int64) *Server {
	s := &Server{
		webhook:               webhook,
		gitops:                gitops,
		deployToken:           deployToken,
		allowedInstallationID: allowedInstallationID,
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(accessLog)
	r.Get("/healthz", s.handleHealthz)
	r.Post("/webhooks/github", s.handleWebhook)
	r.Route("/v1", func(r chi.Router) {
		r.Use(requireBearer(deployToken))
		r.Post("/deploy", s.handleDeploy)
	})
	s.router = r
	return s
}

func (s *Server) Handler() http.Handler {
	return s.router
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// WaitBackground blocks until every background teardown has finished. It
// returns nil once they are done, or ctx.Err() when ctx expires first.
func (s *Server) WaitBackground(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
