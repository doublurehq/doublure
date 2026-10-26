package github_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	gh "github.com/google/go-github/v71/github"

	"github.com/doublurehq/doublure/internal/github"
)

func signPayload(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newApp(t *testing.T, secret string) *github.App {
	t.Helper()
	app, err := github.NewApp(github.AppConfig{
		AppID:         12345,
		PrivateKey:    generateTestKey(t),
		WebhookSecret: secret,
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	return app
}

func TestValidateWebhook(t *testing.T) {
	secret := "test-secret"
	app := newApp(t, secret)

	t.Run("valid signature", func(t *testing.T) {
		body := []byte(`{"action":"opened"}`)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", signPayload(body, secret))
		req.Header.Set("Content-Type", "application/json")

		payload, err := app.ValidateWebhook(req)
		if err != nil {
			t.Fatalf("ValidateWebhook() error = %v", err)
		}
		if !bytes.Equal(payload, body) {
			t.Fatalf("payload mismatch: got %s, want %s", payload, body)
		}
	})

	t.Run("invalid signature", func(t *testing.T) {
		body := []byte(`{"action":"opened"}`)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", "sha256=invalid")
		req.Header.Set("Content-Type", "application/json")

		_, err := app.ValidateWebhook(req)
		if err == nil {
			t.Fatal("ValidateWebhook() expected error for invalid signature")
		}
	})

	t.Run("missing signature", func(t *testing.T) {
		body := []byte(`{"action":"opened"}`)
		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		_, err := app.ValidateWebhook(req)
		if err == nil {
			t.Fatal("ValidateWebhook() expected error for missing signature")
		}
	})
}

func TestParseEvent(t *testing.T) {
	app := newApp(t, "secret")

	t.Run("pull_request event", func(t *testing.T) {
		payload := []byte(`{"action":"opened","number":1,"pull_request":{"number":1}}`)
		event, err := app.ParseEvent("pull_request", payload)
		if err != nil {
			t.Fatalf("ParseEvent() error = %v", err)
		}
		pr, ok := event.(*gh.PullRequestEvent)
		if !ok {
			t.Fatalf("expected *PullRequestEvent, got %T", event)
		}
		if pr.GetAction() != "opened" {
			t.Fatalf("action = %q, want %q", pr.GetAction(), "opened")
		}
	})

	t.Run("installation event", func(t *testing.T) {
		payload := []byte(`{"action":"created","installation":{"id":123}}`)
		event, err := app.ParseEvent("installation", payload)
		if err != nil {
			t.Fatalf("ParseEvent() error = %v", err)
		}
		inst, ok := event.(*gh.InstallationEvent)
		if !ok {
			t.Fatalf("expected *InstallationEvent, got %T", event)
		}
		if inst.GetAction() != "created" {
			t.Fatalf("action = %q, want %q", inst.GetAction(), "created")
		}
	})

	t.Run("unknown event type", func(t *testing.T) {
		_, err := app.ParseEvent("unknown_event", []byte(`{}`))
		if err == nil {
			t.Fatal("ParseEvent() expected error for unknown event type")
		}
	})
}
