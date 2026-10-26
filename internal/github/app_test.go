package github_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/bradleyfalzon/ghinstallation/v2"

	"github.com/doublurehq/doublure/internal/github"
)

func generateTestKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	encodeErr := pem.Encode(&buf, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	return buf.Bytes()
}

func TestNewApp(t *testing.T) {
	t.Run("valid key", func(t *testing.T) {
		app, err := github.NewApp(github.AppConfig{
			AppID:         12345,
			PrivateKey:    generateTestKey(t),
			WebhookSecret: "test-secret",
		})
		if err != nil {
			t.Fatalf("NewApp() error = %v", err)
		}
		if app == nil {
			t.Fatal("NewApp() returned nil")
		}
	})

	t.Run("invalid key", func(t *testing.T) {
		_, err := github.NewApp(github.AppConfig{
			AppID:         12345,
			PrivateKey:    []byte("not-a-pem-key"),
			WebhookSecret: "test-secret",
		})
		if err == nil {
			t.Fatal("NewApp() expected error for invalid key")
		}
	})
}

func TestClientForInstallation(t *testing.T) {
	app, err := github.NewApp(github.AppConfig{
		AppID:         12345,
		PrivateKey:    generateTestKey(t),
		WebhookSecret: "test-secret",
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}

	client := app.ClientForInstallation(67890)
	if client == nil {
		t.Fatal("ClientForInstallation() returned nil")
	}
}

func TestGetInstallationToken(t *testing.T) {
	wantToken := "ghs_fake_installation_token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      wantToken,
			"expires_at": "2099-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()

	key := generateTestKey(t)
	itr, err := ghinstallation.NewAppsTransport(http.DefaultTransport, 12345, key)
	if err != nil {
		t.Fatalf("NewAppsTransport() error = %v", err)
	}
	itr.BaseURL = srv.URL

	app, err := github.NewAppFromTransport(itr, "test-secret")
	if err != nil {
		t.Fatalf("NewAppFromTransport() error = %v", err)
	}

	token, err := app.GetInstallationToken(context.Background(), 67890)
	if err != nil {
		t.Fatalf("GetInstallationToken() error = %v", err)
	}
	if token != wantToken {
		t.Errorf("token = %q, want %q", token, wantToken)
	}
}
