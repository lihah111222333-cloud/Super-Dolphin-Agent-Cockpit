package metrics

import (
	"testing"

	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// TestCountersIncrementIndependently is a smoke test for P22 P4 S6b:
// each counter is reachable and Inc() actually moves the underlying
// value. Guards against accidentally re-declaring a counter in a way
// that shadows the exported accessor (e.g. promauto panic on dup
// registration surfaces here instead of at process start).
func TestSkillMetricsExporterSnapshotIncludesHostToolOutcomes(t *testing.T) {
	skillmetrics.ResetForTesting()
	t.Cleanup(skillmetrics.ResetForTesting)

	skillmetrics.IncUntrustedManifestRedaction()
	skillmetrics.IncTrimCorruptionFallback()
	skillmetrics.IncArtifactApprovalMiss()
	skillmetrics.IncSkillExpandInvoke()
	skillmetrics.IncSkillMCPToolCall()
	skillmetrics.IncSkillMCPToolSuccess()
	skillmetrics.IncSkillMCPToolError()
	skillmetrics.IncSkillMCPApprovalRequired()
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
		{name: "skill_untrusted_manifest_redaction_total", got: testutil.ToFloat64(SkillUntrustedManifestRedactionTotal), want: 1},
		{name: "skill_trim_corruption_fallback_count", got: testutil.ToFloat64(SkillTrimCorruptionFallbackCount), want: 1},
		{name: "skill_artifact_approval_miss_total", got: testutil.ToFloat64(SkillArtifactApprovalMissTotal), want: 1},
		{name: "skill_expand_invoke_rate", got: testutil.ToFloat64(SkillExpandInvokeRate), want: 1},
		{name: "skill_mcp_tool_calls_total", got: testutil.ToFloat64(SkillMCPToolCallsTotal), want: 1},
		{name: "skill_mcp_tool_success_total", got: testutil.ToFloat64(SkillMCPToolSuccessTotal), want: 1},
		{name: "skill_mcp_tool_error_total", got: testutil.ToFloat64(SkillMCPToolErrorTotal), want: 1},
		{name: "skill_mcp_approval_required_total", got: testutil.ToFloat64(SkillMCPApprovalRequiredTotal), want: 1},
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
