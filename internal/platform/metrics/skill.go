package metrics

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
	"github.com/prometheus/client_golang/prometheus"
)

// SkillCollector exports one explicit skill metrics registry.
type SkillCollector struct {
	registry *prometheus.Registry
}

// NewSkillCollector creates an isolated Prometheus exporter for source.
func NewSkillCollector(source *skillmetrics.Registry) (*SkillCollector, error) {
	if source == nil {
		return nil, fmt.Errorf("metrics: skill registry is required")
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "skill_trim_corruption_fallback_count", Help: "Number of skill block trim operations that fell back because paired fences were corrupt."}, func() float64 { return float64(source.Snapshot().TrimCorruptionFallbackCount) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "skill_artifact_approval_miss_total", Help: "Number of skill artifact approval cache misses."}, func() float64 { return float64(source.Snapshot().ArtifactApprovalMissTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "host_tool_calls_total", Help: "Number of codexapp host-direct skill tool calls labelled by outcome.", ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeOK}}, func() float64 { return float64(source.Snapshot().HostToolCallOKTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "host_tool_calls_total", Help: "Number of codexapp host-direct skill tool calls labelled by outcome.", ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeCWDMissing}}, func() float64 { return float64(source.Snapshot().HostToolCallCWDMissingTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "host_tool_calls_total", Help: "Number of codexapp host-direct skill tool calls labelled by outcome.", ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeApprovalRequired}}, func() float64 { return float64(source.Snapshot().HostToolCallApprovalReqTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "host_tool_calls_total", Help: "Number of codexapp host-direct skill tool calls labelled by outcome.", ConstLabels: prometheus.Labels{"outcome": skillmetrics.HostToolOutcomeError}}, func() float64 { return float64(source.Snapshot().HostToolCallErrorTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "enrich_failures_total", Help: "Number of codexapp tool-call parameter enrichment failures."}, func() float64 { return float64(source.Snapshot().EnrichFailuresTotal) }),
	)
	return &SkillCollector{registry: registry}, nil
}

// Gatherer returns only the skill registry's Prometheus series.
func (c *SkillCollector) Gatherer() prometheus.Gatherer { return c.registry }
