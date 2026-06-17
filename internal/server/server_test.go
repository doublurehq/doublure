package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pixelfactoryio/previo/internal/server"
	"github.com/pixelfactoryio/previo/internal/server/mocks"
)

func TestHealthz(t *testing.T) {
	m := mocks.NewMockWebhookValidator(t)
	srv := server.NewServer(m, nil, "test-deploy-token")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}
