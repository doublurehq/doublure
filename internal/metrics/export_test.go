package metrics

import "github.com/prometheus/client_golang/prometheus"

// PreviewsGauge exposes the previews gauge to the external tests.
func (m *Metrics) PreviewsGauge() prometheus.Gauge { return m.previews }

// SweepLastSuccessGauge exposes the last sweep success gauge to the external
// tests.
func (m *Metrics) SweepLastSuccessGauge() prometheus.Gauge { return m.sweepLastSuccess }
