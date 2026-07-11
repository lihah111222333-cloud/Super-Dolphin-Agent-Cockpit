package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dagmetrics"
)

func TestMetricsHandlerServesDAGDispatchRetryCounters(t *testing.T) {
	registry := dagmetrics.DefaultRegistry()
	registry.ResetForTesting()
	t.Cleanup(registry.ResetForTesting)
	registry.IncDispatchFailed()
	registry.RecordRetry("dag-prom", "node-retry", 2)

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"dispatch_failed_total 1",
		`retry_count_per_node{dag_key="dag-prom",node_key="node-retry"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q:\n%s", want, body)
		}
	}
}
