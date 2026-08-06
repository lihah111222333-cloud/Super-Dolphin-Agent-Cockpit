package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dagmetrics"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func TestDAGCollectorServesDispatchRetryCounters(t *testing.T) {
	source := dagmetrics.NewRegistry()
	source.IncDispatchFailed()
	source.RecordRetry("dag-prom", "node-retry", 2)
	dag, err := NewDAGCollectorFor(source)
	if err != nil {
		t.Fatalf("NewDAGCollectorFor() error = %v", err)
	}
	handler := promhttp.HandlerFor(dag.Gatherer(), promhttp.HandlerOpts{})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))

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
