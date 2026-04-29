package metrics

import (
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	SkillUntrustedManifestRedactionTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_untrusted_manifest_redaction_total",
			Help: "Number of untrusted skill manifest entries redacted before model exposure.",
		},
		func() float64 { return float64(skillmetrics.UntrustedManifestRedaction()) },
	)

	SkillTrimCorruptionFallbackCount = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_trim_corruption_fallback_count",
			Help: "Number of skill block trim operations that fell back because paired fences were corrupt.",
		},
		func() float64 { return float64(skillmetrics.TrimCorruptionFallback()) },
	)

	SkillArtifactApprovalMissTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_artifact_approval_miss_total",
			Help: "Number of skill artifact approval cache misses.",
		},
		func() float64 { return float64(skillmetrics.ArtifactApprovalMiss()) },
	)

	SkillExpandInvokeRate = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_expand_invoke_rate",
			Help: "Raw count of skill ExpandBody and ReadResource invocations; dashboards derive rate().",
		},
		func() float64 { return float64(skillmetrics.SkillExpandInvoke()) },
	)

	SkillMCPToolCallsTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_mcp_tool_calls_total",
			Help: "Number of same-binary skill MCP child tools/call requests.",
		},
		func() float64 { return float64(skillmetrics.SkillMCPToolCall()) },
	)

	SkillMCPToolSuccessTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_mcp_tool_success_total",
			Help: "Number of same-binary skill MCP child tools/call requests that returned a host result successfully.",
		},
		func() float64 { return float64(skillmetrics.SkillMCPToolSuccess()) },
	)

	SkillMCPToolErrorTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_mcp_tool_error_total",
			Help: "Number of same-binary skill MCP child tools/call requests that failed with non-approval errors.",
		},
		func() float64 { return float64(skillmetrics.SkillMCPToolError()) },
	)

	SkillMCPApprovalRequiredTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_mcp_approval_required_total",
			Help: "Number of same-binary skill MCP child tools/call requests that returned approval_required.",
		},
		func() float64 { return float64(skillmetrics.SkillMCPApprovalRequired()) },
	)

	HostToolCallsOK = newHostToolCallsCounter(skillmetrics.HostToolOutcomeOK, skillmetrics.HostToolCallOK)

	HostToolCallsCWDMissing = newHostToolCallsCounter(skillmetrics.HostToolOutcomeCWDMissing, skillmetrics.HostToolCallCWDMissing)

	HostToolCallsApprovalRequired = newHostToolCallsCounter(skillmetrics.HostToolOutcomeApprovalRequired, skillmetrics.HostToolCallApprovalRequired)

	HostToolCallsError = newHostToolCallsCounter(skillmetrics.HostToolOutcomeError, skillmetrics.HostToolCallError)

	EnrichFailuresTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "enrich_failures_total",
			Help: "Number of codexapp tool-call parameter enrichment failures.",
		},
		func() float64 { return float64(skillmetrics.EnrichFailures()) },
	)
)

func newHostToolCallsCounter(outcome string, read func() uint64) prometheus.CounterFunc {
	return promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": outcome},
		},
		func() float64 { return float64(read()) },
	)
}
