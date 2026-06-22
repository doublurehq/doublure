package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// logCapture is a minimal slog.Handler retaining every record, scoped to this
// file's tests (the external webhook_test.go has its own in package server_test).
type logCapture struct {
	mu      sync.Mutex
	entries []slog.Record
}

func (h *logCapture) Enabled(context.Context, slog.Level) bool { return true }

func (h *logCapture) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, r.Clone())
	return nil
}

func (h *logCapture) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *logCapture) WithGroup(string) slog.Handler      { return h }

func TestAccessLog(t *testing.T) {
	capture := &logCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// Downstream handler sets an explicit status the middleware must capture.
	h := accessLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("hi"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/deploy", http.NoBody))

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.entries) != 1 {
		t.Fatalf("got %d log records, want 1", len(capture.entries))
	}
	r := capture.entries[0]
	if r.Message != "http request" {
		t.Fatalf("message = %q, want 'http request'", r.Message)
	}

	attrs := map[string]slog.Value{}
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value
		return true
	})
	if got := attrs["status"].Int64(); got != http.StatusTeapot {
		t.Errorf("status = %d, want %d", got, http.StatusTeapot)
	}
	if got := attrs["method"].String(); got != http.MethodPost {
		t.Errorf("method = %q, want POST", got)
	}
	if got := attrs["path"].String(); got != "/v1/deploy" {
		t.Errorf("path = %q, want /v1/deploy", got)
	}
	if got := attrs["bytes"].Int64(); got != 2 {
		t.Errorf("bytes = %d, want 2", got)
	}
}
