package metrics

import (
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
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
