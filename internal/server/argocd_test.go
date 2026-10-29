package server_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/doublurehq/doublure/internal/metrics"
	"github.com/doublurehq/doublure/internal/server"
)

const testArgoCDToken = "test-argocd-token"

// argoCDNow is the injected clock of the Argo CD event tests.
func argoCDNow() time.Time { return time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC) }

// newArgoCDServer builds a Server with the Argo CD route enabled by token,
// recording into a fresh *Metrics, with its clock at argoCDNow and its logs
// captured.
func newArgoCDServer(t *testing.T, token string) (*server.Server, *metrics.Metrics, *server.LogCapture) {
	t.Helper()
	capture := &server.LogCapture{}
	prev := slog.Default()
	slog.SetDefault(slog.New(capture))
	t.Cleanup(func() { slog.SetDefault(prev) })

	m := metrics.New()
	srv := server.NewServer(nil, nil, "test-deploy-token", testInstallationID,
		server.WithMetrics(m), server.WithArgoCDToken(token))
	srv.SetNow(argoCDNow)
	return srv, m, capture
}

func postArgoCDEvent(srv *server.Server, body, authorization string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/webhooks/argocd", strings.NewReader(body))
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// eventBody is an Argo CD event for backend-1 with updatedAt.
func eventBody(updatedAt string) string {
	return `{"app":"feature-x-backend-1","service":"backend-1","preview":"feature-x",` +
		`"revision":"abc123","updatedAt":"` + updatedAt + `"}`
}

// assertArgoCDEvents fails t unless doublure_argocd_events_total is exactly
// samples, one "{result=...} n" line each; no samples means no series.
func assertArgoCDEvents(t *testing.T, m *metrics.Metrics, samples ...string) {
	t.Helper()
	var expected strings.Builder
	if len(samples) > 0 {
		expected.WriteString("# HELP doublure_argocd_events_total Argo CD notification events, " +
			"by whether they were recorded as a spin-up.\n# TYPE doublure_argocd_events_total counter\n")
	}
	for _, s := range samples {
		expected.WriteString("doublure_argocd_events_total" + s + "\n")
	}
	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(expected.String()),
		"doublure_argocd_events_total"); err != nil {
		t.Error(err)
	}
}

func TestArgoCDEvent_RouteAbsentWithoutToken(t *testing.T) {
	srv, _, _ := newArgoCDServer(t, "")
	rec := postArgoCDEvent(srv, eventBody("2026-06-10T11:58:00Z"), "Bearer ")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestArgoCDEvent_Unauthorized(t *testing.T) {
	for name, authorization := range map[string]string{
		"missing": "",
		"wrong":   "Bearer not-the-token",
		"scheme":  "Basic " + testArgoCDToken,
	} {
		t.Run(name, func(t *testing.T) {
			srv, m, _ := newArgoCDServer(t, testArgoCDToken)
			rec := postArgoCDEvent(srv, eventBody("2026-06-10T11:58:00Z"), authorization)
			if rec.Code != http.StatusUnauthorized || strings.TrimSpace(rec.Body.String()) != "unauthorized" {
				t.Errorf("got %d %q, want 401 unauthorized", rec.Code, rec.Body.String())
			}
			assertArgoCDEvents(t, m)
		})
	}
}

func TestArgoCDEvent_InvalidPayload(t *testing.T) {
	oversize := `{"app":"` + strings.Repeat("a", 64<<10) + `"}`
	for name, body := range map[string]string{
		"bad json":   `{not json`,
		"not object": `["backend-1"]`,
		"wrong type": `{"service":1}`,
		"oversize":   oversize,
	} {
		t.Run(name, func(t *testing.T) {
			srv, m, _ := newArgoCDServer(t, testArgoCDToken)
			rec := postArgoCDEvent(srv, body, "Bearer "+testArgoCDToken)
			if rec.Code != http.StatusBadRequest || strings.TrimSpace(rec.Body.String()) != "invalid payload" {
				t.Errorf("got %d %q, want 400 invalid payload", rec.Code, rec.Body.String())
			}
			assertArgoCDEvents(t, m)
		})
	}
}

func TestArgoCDEvent_Recorded(t *testing.T) {
	srv, m, capture := newArgoCDServer(t, testArgoCDToken)
	rec := postArgoCDEvent(srv, eventBody("2026-06-10T11:58:00Z"), "bearer "+testArgoCDToken)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}

	if err := testutil.GatherAndCompare(m.Gatherer(), strings.NewReader(`
# HELP doublure_preview_spinup_seconds Time from doublure's gitops write to the service's Argo CD Application being synced and Healthy.
# TYPE doublure_preview_spinup_seconds histogram
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="15"} 0
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="30"} 0
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="60"} 0
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="120"} 1
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="180"} 1
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="300"} 1
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="600"} 1
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="900"} 1
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="1800"} 1
doublure_preview_spinup_seconds_bucket{catalog_service="backend-1",le="+Inf"} 1
doublure_preview_spinup_seconds_sum{catalog_service="backend-1"} 120
doublure_preview_spinup_seconds_count{catalog_service="backend-1"} 1
`), "doublure_preview_spinup_seconds"); err != nil {
		t.Error(err)
	}
	assertArgoCDEvents(t, m, `{result="recorded"} 1`)

	want := map[string]string{
		"app": "feature-x-backend-1", "service": "backend-1", "preview": "feature-x",
		"revision": "abc123", "result": "recorded", "spinup_seconds": "120",
	}
	assertEventLog(t, capture, want)
}

func TestArgoCDEvent_Ignored(t *testing.T) {
	for name, body := range map[string]string{
		"empty service":      `{"app":"feature-x","updatedAt":"2026-06-10T11:58:00Z"}`,
		"missing updatedAt":  `{"service":"backend-1"}`,
		"unparsable":         eventBody("2026-06-10 11:58:00"),
		"negative":           eventBody("2026-06-10T12:00:01Z"),
		"over 24h":           eventBody("2026-06-09T11:59:59Z"),
		"empty object":       `{}`,
		"null":               `null`,
		"unknown field only": `{"other":"x"}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv, m, capture := newArgoCDServer(t, testArgoCDToken)
			rec := postArgoCDEvent(srv, body, "Bearer "+testArgoCDToken)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}
			assertArgoCDEvents(t, m, `{result="ignored"} 1`)
			if n, err := testutil.GatherAndCount(
				m.Gatherer(),
				"doublure_preview_spinup_seconds",
			); err != nil ||
				n != 0 {
				t.Errorf("spin-up series = %d (err %v), want 0", n, err)
			}
			assertEventLog(t, capture, map[string]string{"result": "ignored"})
		})
	}
}

func TestArgoCDEvent_Bounds(t *testing.T) {
	for name, updatedAt := range map[string]string{
		"zero":    "2026-06-10T12:00:00Z",
		"exactly": "2026-06-09T12:00:00Z",
		"offset":  "2026-06-10T13:59:00+02:00",
	} {
		t.Run(name, func(t *testing.T) {
			srv, m, _ := newArgoCDServer(t, testArgoCDToken)
			if rec := postArgoCDEvent(
				srv,
				eventBody(updatedAt),
				"Bearer "+testArgoCDToken,
			); rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}
			assertArgoCDEvents(t, m, `{result="recorded"} 1`)
		})
	}
}

// assertEventLog fails t unless capture holds one "argocd event" record at
// INFO whose attributes include want.
func assertEventLog(t *testing.T, capture *server.LogCapture, want map[string]string) {
	t.Helper()
	var found []slog.Record
	for _, r := range capture.Records() {
		if r.Message == "argocd event" {
			found = append(found, r)
		}
	}
	if len(found) != 1 || found[0].Level != slog.LevelInfo {
		t.Fatalf("argocd event records = %d, want 1 at INFO", len(found))
	}
	got := map[string]string{}
	found[0].Attrs(func(a slog.Attr) bool {
		got[a.Key] = a.Value.String()
		return true
	})
	for k, v := range want {
		if got[k] != v {
			t.Errorf("log %s = %q, want %q", k, got[k], v)
		}
	}
}
