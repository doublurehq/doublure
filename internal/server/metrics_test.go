package server_test

import (
	"bytes"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/doublurehq/doublure/internal/github"
	"github.com/doublurehq/doublure/internal/metrics"
	"github.com/doublurehq/doublure/internal/server"
)

// deliveries scrapes m and returns its webhook delivery samples, keyed by
// name and labels as exposed.
func deliveries(t *testing.T, m *metrics.Metrics) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	out := map[string]float64{}
	for line := range strings.Lines(rec.Body.String()) {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "doublure_webhook_deliveries_total{") {
			continue
		}
		i := strings.LastIndexByte(line, ' ')
		v, err := strconv.ParseFloat(line[i+1:], 64)
		if err != nil {
			t.Fatalf("parse %q: %v", line, err)
		}
		out[line[:i]] = v
	}
	return out
}

// newMetricsServer is newWebhookServer recording into a fresh *Metrics.
func newMetricsServer(t *testing.T) (*server.Server, *metrics.Metrics) {
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

	m := metrics.New()
	return server.NewServer(app, nil, "test-deploy-token", testInstallationID, server.WithMetrics(m)), m
}

func TestWebhookMetrics(t *testing.T) {
	unsigned := func(event string) *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader([]byte(`{}`)))
		req.Header.Set("X-Github-Event", event)
		req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
		return req
	}
	cases := []struct {
		name string
		req  *http.Request
		want string
	}{
		{
			name: "signed ping",
			req:  signedRequest("ping", []byte(`{"zen":"x"}`)),
			want: `doublure_webhook_deliveries_total{event="ping",status="200"}`,
		},
		{
			name: "signed other event",
			req:  signedRequest("discussion", []byte(`{"action":"created","installation":{"id":123}}`)),
			want: `doublure_webhook_deliveries_total{event="other",status="200"}`,
		},
		{
			name: "signed unparsable",
			req:  signedRequest("pull_request", []byte(`{not json`)),
			want: `doublure_webhook_deliveries_total{event="pull_request",status="200"}`,
		},
		{
			name: "signed foreign installation",
			req:  signedRequest("installation", []byte(`{"action":"created","installation":{"id":999}}`)),
			want: `doublure_webhook_deliveries_total{event="installation",status="403"}`,
		},
		{
			name: "unsigned with an arbitrary event",
			req:  unsigned("random-123"),
			want: `doublure_webhook_deliveries_total{event="unverified",status="401"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, m := newMetricsServer(t)
			srv.Handler().ServeHTTP(httptest.NewRecorder(), tc.req)

			if got, want := deliveries(t, m), map[string]float64{tc.want: 1}; !maps.Equal(got, want) {
				t.Errorf("deliveries = %v, want %v", got, want)
			}
		})
	}
}

func TestServerHasNoMetricsRoute(t *testing.T) {
	srv, _ := newMetricsServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /metrics on the main handler = %d, want 404", rec.Code)
	}
}
