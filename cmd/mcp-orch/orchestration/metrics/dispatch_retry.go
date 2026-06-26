package metrics

import "github.com/anthropic-ai/super-agent-v3/pkg/dagmetrics"

type DispatchRetryMetrics struct {
	DispatchFailedTotal       int64
	RetryCountPerNode         map[string]int64
	RetryCountPerNodeOverflow int64
	RetryAlertTotal           int64
}

// DispatchRetryCounters 从 dagmetrics 读取 dispatcher 重试计数快照。
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

// ResetDispatchRetryForTesting 重置全局 dispatcher 重试指标。
func ResetDispatchRetryForTesting() {
	dagmetrics.ResetForTesting()
}
