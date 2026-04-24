package archtest

import (
	"os"
	"strings"
	"testing"
)

// TestObservabilityMetricAnchorsWired enforces P22 P4 S6b /
// observability-contract §322: the three stable metric counters
// (bootstrap_heartbeat_failures_total,
// bootstrap_report_queue_dropped_total,
// bootstrap_reconnect_attempts_total) must stay declared in
// internal/platform/metrics and emitted at their documented
// injection sites with the documented label dimensions.
//
// Source-shape based for the same reason as the log-event guard in
// observability_log_event_guard_test.go: the emission paths are
// triggered by reconnect / drain / heartbeat loops that are expensive
// to synthesise from a unit test, but pinning the literal to its
// producer file gives the same freeze with zero runtime cost.
func TestObservabilityMetricAnchorsWired(t *testing.T) {
	// 1. metrics package declares the three counters with documented
	//    names and label dimensions.
	metricsPath := "../../internal/platform/metrics/metrics.go"
	data, err := os.ReadFile(metricsPath)
	if err != nil {
		t.Fatalf("read %s: %v", metricsPath, err)
	}
	metricsSrc := string(data)
	required := []string{
		"\"bootstrap_heartbeat_failures_total\"",
		"\"bootstrap_report_queue_dropped_total\"",
		"\"bootstrap_reconnect_attempts_total\"",
		"\"binary_name\"",
		"\"client_kind\"",
		"\"outcome\"",
	}
	for _, tok := range required {
		if !strings.Contains(metricsSrc, tok) {
			t.Errorf("%s: expected metric declaration token %s (P22 P4 S6b / plan §322)", metricsPath, tok)
		}
	}

	// 2. Each producer must still reference its metric accessor at
	//    the right injection site.
	producers := []struct {
		path   string
		tokens []string
	}{
		{
			path: "../../internal/mcpserver/common/bootstrap/heartbeat.go",
			tokens: []string{
				"metrics.BootstrapHeartbeatFailures",
				"c.cfg.BinaryName",
				"c.cfg.ClientKind",
			},
		},
		{
			path: "../../internal/mcpserver/common/bootstrap/report_queue.go",
			tokens: []string{
				"metrics.BootstrapReportQueueDropped",
			},
		},
		{
			path: "../../internal/mcpserver/common/bootstrap/reconnect.go",
			tokens: []string{
				"metrics.BootstrapReconnectAttempts",
				"\"success\"",
				"\"fail\"",
			},
		},
	}
	for _, spec := range producers {
		raw, err := os.ReadFile(spec.path)
		if err != nil {
			t.Fatalf("read %s: %v", spec.path, err)
		}
		body := string(raw)
		for _, tok := range spec.tokens {
			if !strings.Contains(body, tok) {
				t.Errorf("%s: expected metric emit token %s to stay wired (P22 P4 S6b / plan §322)", spec.path, tok)
			}
		}
	}
}
