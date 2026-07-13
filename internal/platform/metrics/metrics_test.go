package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestSkillMetricsExporterSnapshotIncludesHostToolOutcomes 覆盖 skill metrics 的核心计数器。
// 每个 counter 都必须可达且 Inc 后数值前进，避免重复声明或遮蔽导出的采集器。
func TestSkillMetricsExporterSnapshotIncludesHostToolOutcomes(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)

	skillmetrics.IncTrimCorruptionFallback()
	skillmetrics.IncArtifactApprovalMiss()
	skillmetrics.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeOK)
	skillmetrics.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeOK)
	skillmetrics.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeCWDMissing)
	skillmetrics.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeApprovalRequired)
	skillmetrics.IncHostToolCallOutcome(skillmetrics.HostToolOutcomeError)
	skillmetrics.IncHostToolCallOutcome("unknown")
	skillmetrics.IncEnrichFailure()

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{name: "skill_trim_corruption_fallback_count", got: testutil.ToFloat64(SkillTrimCorruptionFallbackCount), want: 1},
		{name: "skill_artifact_approval_miss_total", got: testutil.ToFloat64(SkillArtifactApprovalMissTotal), want: 1},
		{name: `host_tool_calls_total{outcome="ok"}`, got: testutil.ToFloat64(HostToolCallsOK), want: 2},
		{name: `host_tool_calls_total{outcome="cwd_missing"}`, got: testutil.ToFloat64(HostToolCallsCWDMissing), want: 1},
		{name: `host_tool_calls_total{outcome="approval_required"}`, got: testutil.ToFloat64(HostToolCallsApprovalRequired), want: 1},
		{name: `host_tool_calls_total{outcome="error"}`, got: testutil.ToFloat64(HostToolCallsError), want: 2},
		{name: "enrich_failures_total", got: testutil.ToFloat64(EnrichFailuresTotal), want: 1},
	}
	for _, check := range checks {
		if check.got != check.want {
			t.Fatalf("%s = %v, want %v", check.name, check.got, check.want)
		}
	}
}

func TestCronRecoveryMetricsExportSourceSnapshotAndHandler(t *testing.T) {
	cronmetrics.ResetForTesting()
	t.Cleanup(cronmetrics.ResetForTesting)
	cronmetrics.IncRecoveryFinalizeConflict()
	cronmetrics.IncRecoveryFinalizeError()
	cronmetrics.IncRecoveryFinalizeError()

	if got := testutil.ToFloat64(CronRecoveryFinalizeConflictTotal); got != 1 {
		t.Fatalf("cron_recovery_finalize_conflict_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(CronRecoveryFinalizeErrorTotal); got != 2 {
		t.Fatalf("cron_recovery_finalize_error_total = %v, want 2", got)
	}

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, PrometheusMetricsPath, nil))
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
