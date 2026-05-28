package orchestration

import orchmetrics "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/metrics"

type DAGFallbackMetrics = orchmetrics.DAGFallbackMetrics

func DAGFallbackCounters() DAGFallbackMetrics {
	return orchmetrics.DAGFallbackCounters()
}
