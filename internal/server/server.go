package server

import (
	"encoding/json"
	"net/http"

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
	router      chi.Router
}

func NewServer(webhook WebhookValidator, gitops gitopsAPI, deployToken string) *Server {
	s := &Server{webhook: webhook, gitops: gitops, deployToken: deployToken}
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
