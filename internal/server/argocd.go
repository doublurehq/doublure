package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const (
	// maxArgoCDEventBytes caps the body of POST /webhooks/argocd.
	maxArgoCDEventBytes = 64 << 10
	// maxSpinUp is the longest spin-up recorded; older updatedAt values are
	// ignored.
	maxSpinUp = 24 * time.Hour
)

// argoCDEvent is the body Argo CD notifications POST to /webhooks/argocd when
// a preview service's Application is synced and Healthy.
type argoCDEvent struct {
	App      string `json:"app"`
	Service  string `json:"service"`
	Preview  string `json:"preview"`
	Revision string `json:"revision"`
	// UpdatedAt is the preview.doublure.dev/updated-at annotation: the time
	// doublure wrote the service to the gitops repo.
	UpdatedAt string `json:"updatedAt"`
}

// spinUp returns how long ago e's service was written to the gitops repo,
// and false when e has no service, no RFC 3339 updatedAt, or one outside
// [now-24h, now].
func (e argoCDEvent) spinUp(now time.Time) (time.Duration, bool) {
	if e.Service == "" {
		return 0, false
	}
	updatedAt, err := time.Parse(time.RFC3339, e.UpdatedAt)
	if err != nil {
		return 0, false
	}
	d := now.Sub(updatedAt)
	if d < 0 || d > maxSpinUp {
		return 0, false
	}
	return d, true
}

// handleArgoCDEvent records the spin-up time of the event's service, or
// counts the event as ignored. It answers 204 to every valid event, and 400
// to an invalid or oversize body.
func (s *Server) handleArgoCDEvent(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxArgoCDEventBytes))
	if err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}
	var e argoCDEvent
	if err = json.Unmarshal(body, &e); err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	attrs := []any{
		slog.String("app", e.App),
		slog.String("service", e.Service),
		slog.String("preview", e.Preview),
		slog.String("revision", e.Revision),
	}
	if d, ok := e.spinUp(s.now()); ok {
		s.metrics.SpinUp(e.Service, d)
		attrs = append(attrs, slog.String("result", "recorded"), slog.Float64("spinup_seconds", d.Seconds()))
	} else {
		s.metrics.ArgoCDEventIgnored()
		attrs = append(attrs, slog.String("result", "ignored"))
	}
	slog.Default().InfoContext(ctx, "argocd event", attrs...)
	w.WriteHeader(http.StatusNoContent)
}
