package server_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/doublurehq/doublure/internal/github"
	"github.com/doublurehq/doublure/internal/server"
)

const testWebhookSecret = "test-webhook-secret"

// testInstallationID is the installation the test server accepts deliveries from.
const testInstallationID int64 = 123

// generateTestKey returns a PEM-encoded RSA private key. The App only needs a
// well-formed key to construct its transport; webhook validation and parsing
// don't exercise it.
func generateTestKey(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if encErr := pem.Encode(&buf, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}); encErr != nil {
		t.Fatal(encErr)
	}
	return buf.Bytes()
}

// newWebhookServer builds a Server backed by a real github.App so signature
// validation and event parsing run for real, plus a capturing slog handler.
// Returns the server and a func that snapshots the records logged so far.
func newWebhookServer(t *testing.T) (*server.Server, func() []slog.Record) {
	t.Helper()
	return newWebhookServerForInstallation(t, testInstallationID)
}

// newWebhookServerForInstallation is newWebhookServer with an explicit
// allowed installation ID.
func newWebhookServerForInstallation(t *testing.T, allowedInstallationID int64) (*server.Server, func() []slog.Record) {
	t.Helper()
	app, err := github.NewApp(github.AppConfig{
		AppID:         1,
		PrivateKey:    generateTestKey(t),
		WebhookSecret: testWebhookSecret,
	})
	if err != nil {
		t.Fatalf("NewApp: %v", err)
	}

	capture := &server.LogCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	return server.NewServer(app, nil, "test-deploy-token", allowedInstallationID), capture.Records
}

// signedRequest builds a POST to /webhooks/github with a valid HMAC-SHA256
// signature for body, using testWebhookSecret.
func signedRequest(eventType string, body []byte) *http.Request {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Github-Event", eventType)
	req.Header.Set("X-Hub-Signature-256", sig)
	return req
}

// findRecord returns the first captured record with the given message, or a
// zero record + false.
func findRecord(records []slog.Record, msg string) (slog.Record, bool) {
	for _, r := range records {
		if r.Message == msg {
			return r, true
		}
	}
	return slog.Record{}, false
}

// recordAttrs flattens a record's attributes into a map for assertions.
func recordAttrs(r slog.Record) map[string]slog.Value {
	out := make(map[string]slog.Value, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value
		return true
	})
	return out
}

func TestWebhookDispatch(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		body      string
		wantMsg   string
		wantAttrs map[string]string
	}{
		{
			name:      "ping",
			eventType: "ping",
			body:      `{"zen":"Keep it simple","hook_id":42}`,
			wantMsg:   "webhook ping",
			wantAttrs: map[string]string{"zen": "Keep it simple"},
		},
		{
			name:      "installation",
			eventType: "installation",
			body:      `{"action":"created","installation":{"id":123,"account":{"login":"acme"}},"repositories":[{"name":"backend-1"}]}`,
			wantMsg:   "webhook installation",
			wantAttrs: map[string]string{"action": "created", "account": "acme"},
		},
		{
			name:      "installation_repositories",
			eventType: "installation_repositories",
			body:      `{"action":"added","installation":{"id":123},"repositories_added":[{"name":"backend-1"},{"name":"front"}]}`,
			wantMsg:   "webhook installation_repositories",
			wantAttrs: map[string]string{"action": "added"},
		},
		{
			name:      "pull_request",
			eventType: "pull_request",
			body:      `{"action":"opened","number":7,"installation":{"id":123},"repository":{"full_name":"acme/backend-1"},"pull_request":{"head":{"ref":"feature/x","sha":"deadbeef"},"user":{"login":"octocat"}}}`,
			wantMsg:   "webhook pull_request",
			wantAttrs: map[string]string{"action": "opened", "repo": "acme/backend-1", "head_ref": "feature/x"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, records := newWebhookServer(t)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, signedRequest(c.eventType, []byte(c.body)))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			r, ok := findRecord(records(), c.wantMsg)
			if !ok {
				t.Fatalf("expected log %q not found", c.wantMsg)
			}
			attrs := recordAttrs(r)
			for k, want := range c.wantAttrs {
				if got := attrs[k].String(); got != want {
					t.Errorf("attr %q = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func TestWebhookBadSignature(t *testing.T) {
	srv, records := newWebhookServer(t)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader([]byte(`{"zen":"x"}`)))
	req.Header.Set("X-Github-Event", "ping")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	r, ok := findRecord(records(), "webhook signature rejected")
	if !ok {
		t.Fatal("expected 'webhook signature rejected' WARN log")
	}
	if r.Level != slog.LevelWarn {
		t.Errorf("level = %v, want WARN", r.Level)
	}
	if got := recordAttrs(r)["event"].String(); got != "ping" {
		t.Errorf("event attr = %q, want ping", got)
	}
}

func TestWebhookParseFailure(t *testing.T) {
	srv, records := newWebhookServer(t)
	// Valid signature over a malformed JSON body for a known event type.
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, signedRequest("pull_request", []byte(`{not json`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (parse errors must not fail the delivery)", rec.Code)
	}
	if _, ok := findRecord(records(), "webhook parse failed"); !ok {
		t.Error("expected 'webhook parse failed' log record")
	}
}

func TestWebhookUnknownEvent(t *testing.T) {
	srv, records := newWebhookServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, signedRequest("discussion", []byte(`{"action":"created","installation":{"id":123}}`)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	r, ok := findRecord(records(), "unhandled webhook event")
	if !ok {
		t.Fatal("expected 'unhandled webhook event' log record")
	}
	if got := recordAttrs(r)["event"].String(); got != "discussion" {
		t.Errorf("event attr = %q, want discussion", got)
	}
}

func TestWebhookMissingOptionalFields(t *testing.T) {
	// pull_request with only the minimum fields — head/user/repo absent.
	// Handler must not panic; deref-safe accessors return zero values.
	srv, records := newWebhookServer(t)
	rec := httptest.NewRecorder()
	body := `{"action":"closed","number":9,"installation":{"id":123}}`
	srv.Handler().ServeHTTP(rec, signedRequest("pull_request", []byte(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	r, ok := findRecord(records(), "webhook pull_request")
	if !ok {
		t.Fatal("expected 'webhook pull_request' log record")
	}
	attrs := recordAttrs(r)
	if got := attrs["action"].String(); got != "closed" {
		t.Errorf("action = %q, want closed", got)
	}
	if got := attrs["head_ref"].String(); got != "" {
		t.Errorf("head_ref = %q, want empty (field absent)", got)
	}
	skipped, ok := findRecord(records(), "preview teardown skipped")
	if !ok {
		t.Fatal("expected 'preview teardown skipped' log record")
	}
	if got := recordAttrs(skipped)["reason"].String(); got != "missing pr fields" {
		t.Errorf("reason = %q, want 'missing pr fields'", got)
	}
}

func TestWebhookForeignInstallation(t *testing.T) {
	srv, records := newWebhookServer(t)
	rec := httptest.NewRecorder()
	body := `{"action":"opened","number":7,"installation":{"id":999,"account":{"login":"evil"}}}`
	srv.Handler().ServeHTTP(rec, signedRequest("pull_request", []byte(body)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	r, ok := findRecord(records(), "webhook installation rejected")
	if !ok {
		t.Fatal("expected 'webhook installation rejected' log record")
	}
	if r.Level != slog.LevelWarn {
		t.Errorf("level = %v, want WARN", r.Level)
	}
	attrs := recordAttrs(r)
	if got := attrs["installation_id"].Int64(); got != 999 {
		t.Errorf("installation_id = %d, want 999", got)
	}
	if got := attrs["account"].String(); got != "evil" {
		t.Errorf("account = %q, want evil", got)
	}
	if got := attrs["event"].String(); got != "pull_request" {
		t.Errorf("event = %q, want pull_request", got)
	}
	if _, dispatched := findRecord(records(), "webhook pull_request"); dispatched {
		t.Error("foreign installation delivery must not be dispatched")
	}
}

func TestWebhookMissingInstallation(t *testing.T) {
	srv, records := newWebhookServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, signedRequest("pull_request", []byte(`{"action":"opened","number":7}`)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	r, ok := findRecord(records(), "webhook installation rejected")
	if !ok {
		t.Fatal("expected 'webhook installation rejected' log record")
	}
	if got := recordAttrs(r)["installation_id"].Int64(); got != 0 {
		t.Errorf("installation_id = %d, want 0", got)
	}
}

func TestWebhookMissingInstallationZeroAllowed(t *testing.T) {
	srv, records := newWebhookServerForInstallation(t, 0)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, signedRequest("pull_request", []byte(`{"action":"opened","number":7}`)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if _, ok := findRecord(records(), "webhook installation rejected"); !ok {
		t.Fatal("expected 'webhook installation rejected' log record")
	}
}

func TestWebhookForeignInstallationEvent(t *testing.T) {
	srv, records := newWebhookServer(t)
	rec := httptest.NewRecorder()
	body := `{"action":"created","installation":{"id":999,"account":{"login":"evil"}}}`
	srv.Handler().ServeHTTP(rec, signedRequest("installation", []byte(body)))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if _, ok := findRecord(records(), "webhook installation"); ok {
		t.Error("foreign installation event must not be dispatched")
	}
}
