// Package metrics owns super-agent-v3's process-wide observability
// counters. P22 P4 S6b / plan §322 pins three stable anchors on the
// bootstrap client hot paths; this package is the single declaration
// site so archtest can reverse-validate the names and label
// dimensions. All counters auto-register with
// prometheus.DefaultRegisterer via promauto; exporters plug into the
// default gatherer when / if a /metrics endpoint is mounted.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// BootstrapHeartbeatFailures counts every bootstrap heartbeat
	// roundtrip that returned a non-nil error (not just warn-level
	// failures). Dimensions mirror the slog-style anchors on the
	// surrounding log lines so dashboards can pivot on the same
	// (binary_name, client_kind) tuple.
	BootstrapHeartbeatFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bootstrap_heartbeat_failures_total",
			Help: "Number of bootstrap heartbeat failures, labelled by binary_name and client_kind.",
		},
		[]string{"binary_name", "client_kind"},
	)

	// BootstrapReportQueueDropped counts durable report enqueue
	// attempts that were rejected because the per-client queue is at
	// capacity. Not a drain-time drop: those are already visible via
	// the bootstrap.report_queue.drain log anchor.
	BootstrapReportQueueDropped = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "bootstrap_report_queue_dropped_total",
			Help: "Number of bootstrap report enqueue attempts dropped because the durable queue was full.",
		},
	)

	// BootstrapReconnectAttempts counts every bootstrap reconnect
	// attempt. outcome is "success" when connectAndRegister returned
	// without error, "fail" otherwise. One increment per loop
	// iteration, matching the observability contract.
	BootstrapReconnectAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bootstrap_reconnect_attempts_total",
			Help: "Number of bootstrap reconnect attempts, labelled by outcome (success|fail).",
		},
		[]string{"outcome"},
	)
)
