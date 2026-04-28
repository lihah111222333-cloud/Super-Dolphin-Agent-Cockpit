package wails

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/metrics"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

func TestHTTPAssetRoutesExposePrometheusMetricsEndpoint(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)
	skillmetrics.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeOK)

	mux := http.NewServeMux()
	registerHTTPAssetRoutes(mux, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "asset fallback should not handle /metrics", http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, metrics.PrometheusMetricsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `host_tool_calls_total{outcome="ok"} 1`) {
		t.Fatalf("metrics endpoint did not expose host tool counters:\n%s", body)
	}
}
