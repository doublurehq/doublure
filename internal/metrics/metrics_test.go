package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/doublurehq/doublure/internal/metrics"
)

// scrape serves m's handler once and returns every sample, keyed by its name
// and labels as exposed (e.g. `doublure_reconciles_total{outcome="deployed"}`).
func scrape(t *testing.T, m *metrics.Metrics) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d", rec.Code)
	}
	out := map[string]float64{}
	for line := range strings.Lines(rec.Body.String()) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
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

func assertSamples(t *testing.T, got map[string]float64, want map[string]float64) {
	t.Helper()
	for k, v := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("%s missing", k)
			continue
		}
		if g != v {
			t.Errorf("%s = %v, want %v", k, g, v)
		}
	}
}

func TestMetrics_Counters(t *testing.T) {
	m := metrics.New()
	m.WebhookDelivery("pull_request", "200")
	m.WebhookDelivery("unverified", "401")
	m.Reconcile("deployed", "")
	m.Reconcile("skipped", "fork pr")
	m.Reconcile("skipped", "fork pr")
	m.Reconcile("failed", "")
	m.Teardown("webhook", "unpinned")
	m.Teardown("sweep", "noop")
	m.SweepRun(false, time.Unix(1, 0))

	assertSamples(t, scrape(t, m), map[string]float64{
		`doublure_webhook_deliveries_total{event="pull_request",status="200"}`: 1,
		`doublure_webhook_deliveries_total{event="unverified",status="401"}`:   1,
		`doublure_reconciles_total{outcome="deployed"}`:                        1,
		`doublure_reconciles_total{outcome="skipped"}`:                         2,
		`doublure_reconciles_total{outcome="failed"}`:                          1,
		`doublure_reconcile_skips_total{reason="fork pr"}`:                     2,
		`doublure_teardowns_total{result="unpinned",trigger="webhook"}`:        1,
		`doublure_teardowns_total{result="noop",trigger="sweep"}`:              1,
		`doublure_sweep_runs_total{result="error"}`:                            1,
		`doublure_sweep_last_success_timestamp_seconds`:                        0,
		`doublure_previews`: 0,
	})
}

func TestMetrics_OnlySkipsCountReasons(t *testing.T) {
	m := metrics.New()
	m.Reconcile("deployed", "ignored")
	for k := range scrape(t, m) {
		if strings.HasPrefix(k, "doublure_reconcile_skips_total") {
			t.Errorf("unexpected sample %s", k)
		}
	}
}

func TestMetrics_PreviewsGauge(t *testing.T) {
	m := metrics.New()
	m.SweepListed(5)
	m.SweepDeleted("no_pin")
	m.SweepDeleted("ttl")
	m.Teardown("sweep", "deleted")
	m.Teardown("webhook", "deleted")
	m.Teardown("sweep", "unpinned")
	at := time.Date(2026, 6, 10, 0, 0, 0, 0, time.UTC)
	m.SweepRun(true, at)

	assertSamples(t, scrape(t, m), map[string]float64{
		`doublure_previews`: 2,
		`doublure_sweep_previews_deleted_total{rule="no_pin"}`: 1,
		`doublure_sweep_previews_deleted_total{rule="ttl"}`:    1,
		`doublure_sweep_runs_total{result="ok"}`:               1,
		`doublure_sweep_last_success_timestamp_seconds`:        float64(at.Unix()),
	})
}

func TestMetrics_GitopsOp(t *testing.T) {
	m := metrics.New()
	m.GitopsOp("write", "ok", 30*time.Millisecond)
	m.GitopsOp("teardown", "error", 2*time.Second)

	assertSamples(t, scrape(t, m), map[string]float64{
		`doublure_gitops_operation_duration_seconds_count{op="write",result="ok"}`:                 1,
		`doublure_gitops_operation_duration_seconds_sum{op="write",result="ok"}`:                   0.03,
		`doublure_gitops_operation_duration_seconds_count{op="teardown",result="error"}`:           1,
		`doublure_gitops_operation_duration_seconds_bucket{op="teardown",result="error",le="2.5"}`: 1,
	})
}

func TestMetrics_ProcessAndGoCollectors(t *testing.T) {
	got := scrape(t, metrics.New())
	for _, name := range []string{"go_goroutines", "process_start_time_seconds"} {
		if _, ok := got[name]; !ok {
			t.Errorf("%s missing", name)
		}
	}
}

func TestMetrics_NilIsSafe(t *testing.T) {
	var m *metrics.Metrics
	m.WebhookDelivery("ping", "200")
	m.Reconcile("skipped", "fork pr")
	m.Teardown("sweep", "deleted")
	m.SweepListed(3)
	m.SweepDeleted("ttl")
	m.GitopsOp("write", "ok", time.Second)
	m.SweepRun(true, time.Now())

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("nil Handler = %d, want 404", rec.Code)
	}
}
