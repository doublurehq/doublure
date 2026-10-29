package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/doublurehq/doublure/internal/metrics"
)

// WebhookValidator validates GitHub webhook signatures and parses payloads
// into typed event structs.
type WebhookValidator interface {
	ValidateWebhook(r *http.Request) ([]byte, error)
	ParseEvent(eventType string, payload []byte) (any, error)
}

// lifecycleAPI is gitopsAPI with the metrics recorder lifecycle expects. A
// nil *metrics.Metrics records nothing.
type lifecycleAPI struct {
	gitopsAPI
	*metrics.Metrics
}

// Option configures a Server built by NewServer.
type Option func(*Server)

// WithMetrics records webhook deliveries and lifecycle outcomes in m.
func WithMetrics(m *metrics.Metrics) Option {
	return func(s *Server) {
		s.metrics = m
	}
}

// WithArgoCDToken enables POST /webhooks/argocd, authenticated by the Bearer
// token. An empty token leaves the route unregistered.
func WithArgoCDToken(token string) Option {
	return func(s *Server) {
		s.argoCDToken = token
	}
}

type Server struct {
	webhook     WebhookValidator
	gitops      gitopsAPI
	metrics     *metrics.Metrics
	lifecycle   lifecycleAPI
	deployToken string
	// argoCDToken authenticates POST /webhooks/argocd, "" when it is
	// disabled.
	argoCDToken string
	// now is the clock spin-up durations are measured against.
	now func() time.Time
	// allowedInstallationID is the only GitHub App installation whose webhook
	// deliveries are dispatched.
	allowedInstallationID int64
	router                chi.Router
	// bg tracks background jobs (reconciles), queued or running,
	// for WaitBackground.
	bg sync.WaitGroup
	// jobs serializes background jobs: at most one runs at a time.
	jobs sync.Mutex
	// jobTimeout caps each background job, counted from when it holds jobs.
	jobTimeout time.Duration
}

func NewServer(
	webhook WebhookValidator,
	gitops gitopsAPI,
	deployToken string,
	allowedInstallationID int64,
	opts ...Option,
) *Server {
	s := &Server{
		webhook:               webhook,
		gitops:                gitops,
		deployToken:           deployToken,
		allowedInstallationID: allowedInstallationID,
		jobTimeout:            reconcileTimeout,
		now:                   time.Now,
	}
	for _, opt := range opts {
		opt(s)
	}
	s.lifecycle = lifecycleAPI{gitopsAPI: gitops, Metrics: s.metrics}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(accessLog)
	r.Get("/healthz", s.handleHealthz)
	r.Post("/webhooks/github", s.handleWebhook)
	if s.argoCDToken != "" {
		r.With(requireBearer(s.argoCDToken)).Post("/webhooks/argocd", s.handleArgoCDEvent)
	}
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

// Go runs f in the background, tracked by WaitBackground. Unlike reconciles,
// f does not wait for other background jobs.
func (s *Server) Go(f func()) {
	s.bg.Go(f)
}

// WaitBackground blocks until every background job has finished. It
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
