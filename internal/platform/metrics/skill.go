package metrics

import (
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// SkillTrimCorruptionFallbackCount 统计技能块裁剪遇到损坏 fence 后回退的次数。
	SkillTrimCorruptionFallbackCount = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_trim_corruption_fallback_count",
			Help: "Number of skill block trim operations that fell back because paired fences were corrupt.",
		},
		func() float64 { return float64(skillmetrics.TrimCorruptionFallback()) },
	)

	// SkillArtifactApprovalMissTotal 统计技能 artifact approval 缓存未命中的次数。
	SkillArtifactApprovalMissTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "skill_artifact_approval_miss_total",
			Help: "Number of skill artifact approval cache misses.",
		},
		func() float64 { return float64(skillmetrics.ArtifactApprovalMiss()) },
	)

	// HostToolCallsOK 统计 codexapp host-direct skill tool 调用成功次数。
	HostToolCallsOK = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeOK},
		},
		func() float64 { return float64(skillmetrics.HostToolCallOK()) },
	)

	// HostToolCallsCWDMissing 统计 host-direct skill tool 因 cwd 缺失失败的次数。
	HostToolCallsCWDMissing = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeCWDMissing},
		},
		func() float64 { return float64(skillmetrics.HostToolCallCWDMissing()) },
	)

	// HostToolCallsApprovalRequired 统计 host-direct skill tool 因需要审批而未执行的次数。
	HostToolCallsApprovalRequired = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeApprovalRequired},
		},
		func() float64 { return float64(skillmetrics.HostToolCallApprovalRequired()) },
	)

	// HostToolCallsError 统计 host-direct skill tool 调用的普通错误次数。
	HostToolCallsError = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name:        "host_tool_calls_total",
			Help:        "Number of codexapp host-direct skill tool calls labelled by outcome.",
			ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeError},
		},
		func() float64 { return float64(skillmetrics.HostToolCallError()) },
	)

	// EnrichFailuresTotal 统计 codexapp 工具调用参数补全失败次数。
	EnrichFailuresTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "enrich_failures_total",
			Help: "Number of codexapp tool-call parameter enrichment failures.",
		},
		func() float64 { return float64(skillmetrics.EnrichFailures()) },
	)
)
