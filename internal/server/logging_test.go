package server

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAccessLog(t *testing.T) {
	capture := &LogCapture{}
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

	records := capture.Records()
	if len(records) != 1 {
		t.Fatalf("got %d log records, want 1", len(records))
	}
	r := records[0]
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
