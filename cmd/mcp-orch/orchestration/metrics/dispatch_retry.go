package metrics

import "github.com/anthropic-ai/super-agent-v3/pkg/dagmetrics"

type DispatchRetryMetrics struct {
	DispatchFailedTotal       int64
	RetryCountPerNode         map[string]int64
	RetryCountPerNodeOverflow int64
	RetryAlertTotal           int64
}

// DispatchRetryCounters 派发重试counters。
func DispatchRetryCounters() DispatchRetryMetrics {
	snap := dagmetrics.Read()
	perNode := make(map[string]int64, len(snap.RetryCountPerNode))
	for _, count := range snap.RetryCountPerNode {
		perNode[count.DagKey+"/"+count.NodeKey] = int64(count.Count)
	}
	return DispatchRetryMetrics{
		DispatchFailedTotal:       int64(snap.DispatchFailedTotal),
		RetryCountPerNode:         perNode,
		RetryCountPerNodeOverflow: int64(snap.RetryCountPerNodeOverflow),
		RetryAlertTotal:           int64(snap.RetryAlertTotal),
	}
}

// ResetDispatchRetryForTesting 为testing重置dispatch重试。
func ResetDispatchRetryForTesting() {
	dagmetrics.ResetForTesting()
}
