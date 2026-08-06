package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

func TestMetricsHandlerServesSkillHostToolCounters(t *testing.T) {
	source := skillmetrics.NewRegistry()
	source.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeOK)

	rec := httptest.NewRecorder()
	newMetricsHandler(t, cronmetrics.New(), source).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, `host_tool_calls_total{outcome="ok"} 1`) {
		t.Fatalf("metrics body missing host_tool_calls_total ok sample:\n%s", body)
	}
}

func TestRegisterHTTPHandlersMountsMetricsPath(t *testing.T) {
	t.Setenv(EnableMetricsEnv, "1")
	source := skillmetrics.NewRegistry()
	source.IncEnrichFailure()

	mux := http.NewServeMux()
	if err := RegisterHTTPHandlers(mux, newMetricsHandler(t, cronmetrics.New(), source)); err != nil {
		t.Fatalf("RegisterHTTPHandlers() error = %v", err)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := rec.Body.String(); !strings.Contains(body, "enrich_failures_total 1") {
		t.Fatalf("metrics body missing enrich_failures_total sample:\n%s", body)
	}
}

func TestNewHandlerRejectsMissingCollector(t *testing.T) {
	if _, err := NewHandler(Collectors{}); err == nil {
		t.Fatal("NewHandler() error = nil, want required-owner failure")
	}
}
