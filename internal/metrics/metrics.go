// Package metrics holds doublure's Prometheus collectors in their own
// registry. A nil *Metrics records nothing, so callers that run without
// metrics pass nil.
package metrics

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	namespace = "doublure"

	outcomeSkipped = "skipped"
	triggerSweep   = "sweep"
	resultDeleted  = "deleted"
	resultOK       = "ok"
	resultError    = "error"

	labelResult = "result"
)

// Metrics holds every doublure collector and the registry serving them.
type Metrics struct {
	registry *prometheus.Registry

	webhookDeliveries *prometheus.CounterVec
	reconciles        *prometheus.CounterVec
	reconcileSkips    *prometheus.CounterVec
	teardowns         *prometheus.CounterVec
	gitopsDuration    *prometheus.HistogramVec
	sweepRuns         *prometheus.CounterVec
	sweepLastSuccess  prometheus.Gauge
	sweepDeleted      *prometheus.CounterVec
	previews          prometheus.Gauge
}

// New builds a registry holding the Go and process collectors and every
// doublure collector.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		webhookDeliveries: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "webhook_deliveries_total",
			Help:      "GitHub webhook deliveries, by verified event and HTTP status.",
		}, []string{"event", "status"}),
		reconciles: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "reconciles_total",
			Help:      "Pull request reconciles, by outcome.",
		}, []string{"outcome"}),
		reconcileSkips: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "reconcile_skips_total",
			Help:      "Skipped pull request reconciles, by reason.",
		}, []string{"reason"}),
		teardowns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "teardowns_total",
			Help:      "Preview teardowns of a pull request, by trigger and result.",
		}, []string{"trigger", labelResult}),
		gitopsDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "gitops_operation_duration_seconds",
			Help:      "Duration of gitops repo writes and teardowns, retries included.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"op", labelResult}),
		sweepRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "sweep_runs_total",
			Help:      "In-server cleanup sweep runs, by result.",
		}, []string{labelResult}),
		sweepLastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "sweep_last_success_timestamp_seconds",
			Help:      "Unix time of the last successful in-server cleanup sweep, 0 before the first one.",
		}),
		sweepDeleted: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "sweep_previews_deleted_total",
			Help:      "Previews deleted by the cleanup sweep, by rule.",
		}, []string{"rule"}),
		previews: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Name:      "previews",
			Help:      "Previews in the gitops repo as of the last in-server cleanup sweep, 0 before the first one.",
		}),
	}
	m.registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.webhookDeliveries,
		m.reconciles,
		m.reconcileSkips,
		m.teardowns,
		m.gitopsDuration,
		m.sweepRuns,
		m.sweepLastSuccess,
		m.sweepDeleted,
		m.previews,
	)
	return m
}

// Handler serves the registry in the Prometheus exposition format. A nil
// *Metrics serves 404.
func (m *Metrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// Reconcile counts one reconcile with outcome and, when it was skipped, its
// reason.
func (m *Metrics) Reconcile(outcome, reason string) {
	if m == nil {
		return
	}
	m.reconciles.WithLabelValues(outcome).Inc()
	if outcome == outcomeSkipped {
		m.reconcileSkips.WithLabelValues(reason).Inc()
	}
}

// Teardown counts one teardown. A sweep teardown that deleted its preview
// also decrements the previews gauge.
func (m *Metrics) Teardown(trigger, result string) {
	if m == nil {
		return
	}
	m.teardowns.WithLabelValues(trigger, result).Inc()
	if trigger == triggerSweep && result == resultDeleted {
		m.previews.Dec()
	}
}

// SweepListed sets the previews gauge to the n previews a sweep listed.
func (m *Metrics) SweepListed(n int) {
	if m == nil {
		return
	}
	m.previews.Set(float64(n))
}

// SweepDeleted counts one preview deleted by the sweep's rule and decrements
// the previews gauge.
func (m *Metrics) SweepDeleted(rule string) {
	if m == nil {
		return
	}
	m.sweepDeleted.WithLabelValues(rule).Inc()
	m.previews.Dec()
}

// WebhookDelivery counts one webhook delivery answered with status.
func (m *Metrics) WebhookDelivery(event, status string) {
	if m == nil {
		return
	}
	m.webhookDeliveries.WithLabelValues(event, status).Inc()
}

// GitopsOp observes the duration d of the gitops operation op, which ended
// with result.
func (m *Metrics) GitopsOp(op, result string, d time.Duration) {
	if m == nil {
		return
	}
	m.gitopsDuration.WithLabelValues(op, result).Observe(d.Seconds())
}

// SweepRun counts one in-server sweep run and, when ok, sets the last success
// timestamp to at.
func (m *Metrics) SweepRun(ok bool, at time.Time) {
	if m == nil {
		return
	}
	if !ok {
		m.sweepRuns.WithLabelValues(resultError).Inc()
		return
	}
	m.sweepRuns.WithLabelValues(resultOK).Inc()
	m.sweepLastSuccess.Set(float64(at.Unix()))
}
