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

	HostToolCallsOK = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeOK},
		},
		func() float64 { return float64(skillmetrics.HostToolCallOK()) },
	)

	HostToolCallsCWDMissing = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeCWDMissing},
		},
		func() float64 { return float64(skillmetrics.HostToolCallCWDMissing()) },
	)

	HostToolCallsApprovalRequired = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeApprovalRequired},
		},
		func() float64 { return float64(skillmetrics.HostToolCallApprovalRequired()) },
	)

	HostToolCallsError = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeError},
		},
		func() float64 { return float64(skillmetrics.HostToolCallError()) },
	)

	EnrichFailuresTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "enrich_failures_total",
			Help: "Number of codexapp tool-call parameter enrichment failures.",
		},
		func() float64 { return float64(skillmetrics.EnrichFailures()) },
	)
)
