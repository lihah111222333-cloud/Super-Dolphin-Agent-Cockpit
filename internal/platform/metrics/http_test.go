package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

func TestMetricsHandlerServesSkillHostToolCounters(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)
	skillmetrics.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeOK)

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `host_tool_calls_total{outcome="ok"} 1`) {
		t.Fatalf("metrics body missing host_tool_calls_total ok sample:\n%s", body)
	}
}

func TestRegisterHTTPHandlersMountsMetricsPath(t *testing.T) {
	t.Setenv(EnableMetricsEnv, "1")
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)
	skillmetrics.IncEnrichFailure()

	mux := http.NewServeMux()
	RegisterHTTPHandlers(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "enrich_failures_total 1") {
		t.Fatalf("metrics body missing enrich_failures_total sample:\n%s", body)
	}
}
