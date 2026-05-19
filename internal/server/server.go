package server

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// WebhookValidator validates GitHub webhook signatures.
type WebhookValidator interface {
	ValidateWebhook(r *http.Request) ([]byte, error)
}

type Server struct {
	webhook WebhookValidator
	router  chi.Router
}

func NewServer(webhook WebhookValidator) *Server {
	s := &Server{webhook: webhook}
	r := chi.NewRouter()
	r.Get("/healthz", s.handleHealthz)
	r.Post("/webhooks/github", s.handleWebhook)
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

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	payload, err := s.webhook.ValidateWebhook(r)
	if err != nil {
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-Github-Event")
	//nolint:sloglint // no scoped logger yet
	slog.InfoContext(r.Context(), "received webhook", "event", eventType, "size", len(payload))

	w.WriteHeader(http.StatusOK)
}
