package metrics

import "github.com/prometheus/client_golang/prometheus"

// BootstrapMetrics owns the bootstrap process metrics and their isolated registry.
// Each sidecar receives one explicit instance through its bootstrap configuration.
type BootstrapMetrics struct {
	registry           *prometheus.Registry
	HeartbeatFailures  *prometheus.CounterVec
	ReportQueueDropped prometheus.Counter
	ReconnectAttempts  *prometheus.CounterVec
}

// NewBootstrapMetrics creates an isolated bootstrap metrics owner.
func NewBootstrapMetrics() *BootstrapMetrics {
	registry := prometheus.NewRegistry()
	heartbeatFailures := prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "bootstrap_heartbeat_failures_total", Help: "Total failed bootstrap heartbeats."},
		[]string{"binary_name", "client_kind"},
	)
	reportQueueDropped := prometheus.NewCounter(
		prometheus.CounterOpts{Name: "bootstrap_report_queue_dropped_total", Help: "Total reports rejected because the durable bootstrap queue is full."},
	)
	reconnectAttempts := prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "bootstrap_reconnect_attempts_total", Help: "Total bootstrap reconnect attempts."},
		[]string{"outcome"},
	)
	registry.MustRegister(heartbeatFailures, reportQueueDropped, reconnectAttempts)
	return &BootstrapMetrics{
		registry:           registry,
		HeartbeatFailures:  heartbeatFailures,
		ReportQueueDropped: reportQueueDropped,
		ReconnectAttempts:  reconnectAttempts,
	}
}

// Gatherer returns the owner's isolated Prometheus gatherer.
func (m *BootstrapMetrics) Gatherer() prometheus.Gatherer { return m.registry }

// IncHeartbeatFailure records one failed heartbeat for the given sidecar identity.
func (m *BootstrapMetrics) IncHeartbeatFailure(binaryName, clientKind string) {
	m.HeartbeatFailures.WithLabelValues(binaryName, clientKind).Inc()
}

// IncReportQueueDropped records one durable report rejected by a full queue.
func (m *BootstrapMetrics) IncReportQueueDropped() { m.ReportQueueDropped.Inc() }

// IncReconnectAttempt records one reconnect attempt with its terminal attempt outcome.
func (m *BootstrapMetrics) IncReconnectAttempt(outcome string) {
	m.ReconnectAttempts.WithLabelValues(outcome).Inc()
}
