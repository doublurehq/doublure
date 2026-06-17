package server_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"

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

func TestWebhookValidSignature(t *testing.T) {
	m := mocks.NewMockWebhookValidator(t)
	m.EXPECT().ValidateWebhook(mock.AnythingOfType("*http.Request")).
		Return([]byte(`{"action":"opened"}`), nil)

	srv := server.NewServer(m, nil, "test-deploy-token")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(`{"action":"opened"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "pull_request")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestWebhookInvalidSignature(t *testing.T) {
	m := mocks.NewMockWebhookValidator(t)
	m.EXPECT().ValidateWebhook(mock.AnythingOfType("*http.Request")).
		Return(nil, errors.New("invalid signature"))

	srv := server.NewServer(m, nil, "test-deploy-token")

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(`{"action":"opened"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", "pull_request")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}
