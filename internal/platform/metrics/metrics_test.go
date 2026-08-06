package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dreammetrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestSkillMetricsExporterSnapshotIncludesHostToolOutcomes 覆盖 skill metrics 的核心计数器。
// 每个 counter 都必须可达且 Inc 后数值前进，避免重复声明或遮蔽导出的采集器。
func TestSkillMetricsExporterSnapshotIncludesHostToolOutcomes(t *testing.T) {
	source := skillmetrics.NewRegistry()
	source.IncTrimCorruptionFallback()
	source.IncArtifactApprovalMiss()
	source.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeOK)
	source.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeOK)
	source.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeCWDMissing)
	source.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeApprovalRequired)
	source.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeError)
	source.IncHostToolCallOutcome("unknown")
	source.IncEnrichFailure()

	handler := newMetricsHandler(t, cronmetrics.New(), source)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))
	body := rec.Body.String()
	for _, sample := range []string{
		"skill_trim_corruption_fallback_count 1", "skill_artifact_approval_miss_total 1",
		`host_tool_calls_total{outcome="ok"} 2`, `host_tool_calls_total{outcome="cwd_missing"} 1`,
		`host_tool_calls_total{outcome="approval_required"} 1`, `host_tool_calls_total{outcome="error"} 2`,
		"enrich_failures_total 1",
	} {
		if !strings.Contains(body, sample) {
			t.Fatalf("metrics body missing %q:\n%s", sample, body)
		}
	}
}

func TestCronRecoveryMetricsExportSourceSnapshotAndHandler(t *testing.T) {
	source := cronmetrics.New()
	source.IncRecoveryFinalizeConflict()
	source.IncRecoveryFinalizeError()
	source.IncRecoveryFinalizeError()
	handler := newMetricsHandler(t, source, skillmetrics.NewRegistry())

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("metrics status = %d, want %d", rec.Code, http.StatusOK)
	}
	body := rec.Body.String()
	for _, sample := range []string{
		"cron_recovery_finalize_conflict_total 1",
		"cron_recovery_finalize_error_total 2",
	} {
		if !strings.Contains(body, sample) {
			t.Fatalf("metrics body missing %q:\n%s", sample, body)
		}
	}
}

func newMetricsHandler(t *testing.T, cronSource *cronmetrics.Metrics, skillSource *skillmetrics.Registry) http.Handler {
	t.Helper()
	cron, err := NewCronRecoveryCollector(cronSource)
	if err != nil {
		t.Fatalf("NewCronRecoveryCollector() error = %v", err)
	}
	dream, err := NewDreamCollector(dreammetrics.NewRegistry())
	if err != nil {
		t.Fatalf("NewDreamCollector() error = %v", err)
	}
	skill, err := NewSkillCollector(skillSource)
	if err != nil {
		t.Fatalf("NewSkillCollector() error = %v", err)
	}
	handler, err := NewHandler(Collectors{Cron: cron, DAG: NewDAGCollector(), Dream: dream, Bootstrap: NewBootstrapMetrics(), Skill: skill})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func TestCountersIncrementIndependently(t *testing.T) {
	metrics := NewBootstrapMetrics()
	hbBefore := testutil.ToFloat64(metrics.HeartbeatFailures.WithLabelValues("binA", "orch"))
	metrics.IncHeartbeatFailure("binA", "orch")
	if got := testutil.ToFloat64(metrics.HeartbeatFailures.WithLabelValues("binA", "orch")); got != hbBefore+1 {
		t.Fatalf("heartbeat counter: got %v want %v", got, hbBefore+1)
	}

	dropBefore := testutil.ToFloat64(metrics.ReportQueueDropped)
	metrics.IncReportQueueDropped()
	if got := testutil.ToFloat64(metrics.ReportQueueDropped); got != dropBefore+1 {
		t.Fatalf("report queue dropped counter: got %v want %v", got, dropBefore+1)
	}

	okBefore := testutil.ToFloat64(metrics.ReconnectAttempts.WithLabelValues("success"))
	failBefore := testutil.ToFloat64(metrics.ReconnectAttempts.WithLabelValues("fail"))
	metrics.IncReconnectAttempt("success")
	metrics.IncReconnectAttempt("fail")
	if got := testutil.ToFloat64(metrics.ReconnectAttempts.WithLabelValues("success")); got != okBefore+1 {
		t.Fatalf("reconnect success counter: got %v want %v", got, okBefore+1)
	}
	if got := testutil.ToFloat64(metrics.ReconnectAttempts.WithLabelValues("fail")); got != failBefore+1 {
		t.Fatalf("reconnect fail counter: got %v want %v", got, failBefore+1)
	}
}
