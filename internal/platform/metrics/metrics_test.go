package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestCountersIncrementIndependently is a smoke test for P22 P4 S6b:
// each counter is reachable and Inc() actually moves the underlying
// value. Guards against accidentally re-declaring a counter in a way
// that shadows the exported accessor (e.g. promauto panic on dup
// registration surfaces here instead of at process start).
func TestCountersIncrementIndependently(t *testing.T) {
	hbBefore := testutil.ToFloat64(BootstrapHeartbeatFailures.WithLabelValues("binA", "orch"))
	BootstrapHeartbeatFailures.WithLabelValues("binA", "orch").Inc()
	if got := testutil.ToFloat64(BootstrapHeartbeatFailures.WithLabelValues("binA", "orch")); got != hbBefore+1 {
		t.Fatalf("heartbeat counter: got %v want %v", got, hbBefore+1)
	}

	dropBefore := testutil.ToFloat64(BootstrapReportQueueDropped)
	BootstrapReportQueueDropped.Inc()
	if got := testutil.ToFloat64(BootstrapReportQueueDropped); got != dropBefore+1 {
		t.Fatalf("report queue dropped counter: got %v want %v", got, dropBefore+1)
	}

	okBefore := testutil.ToFloat64(BootstrapReconnectAttempts.WithLabelValues("success"))
	failBefore := testutil.ToFloat64(BootstrapReconnectAttempts.WithLabelValues("fail"))
	BootstrapReconnectAttempts.WithLabelValues("success").Inc()
	BootstrapReconnectAttempts.WithLabelValues("fail").Inc()
	if got := testutil.ToFloat64(BootstrapReconnectAttempts.WithLabelValues("success")); got != okBefore+1 {
		t.Fatalf("reconnect success counter: got %v want %v", got, okBefore+1)
	}
	if got := testutil.ToFloat64(BootstrapReconnectAttempts.WithLabelValues("fail")); got != failBefore+1 {
		t.Fatalf("reconnect fail counter: got %v want %v", got, failBefore+1)
	}
}
