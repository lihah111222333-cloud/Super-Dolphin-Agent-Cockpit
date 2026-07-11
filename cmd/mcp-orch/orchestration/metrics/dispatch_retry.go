package metrics

import "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dagmetrics"

// DispatchRetryMetrics 是 dispatcher 重试告警的只读快照。
// 数据来源于 dagmetrics 全局计数器，调用方不得据此反推或修改调度状态。
type DispatchRetryMetrics struct {
	DispatchFailedTotal       int64
	RetryCountPerNode         map[string]int64
	RetryCountPerNodeOverflow int64
	RetryAlertTotal           int64
}

// DispatchRetryCounters 从 dagmetrics 读取 dispatcher 重试计数快照。
func DispatchRetryCounters() DispatchRetryMetrics {
	snap := dagmetrics.DefaultRegistry().Read()
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
	dagmetrics.DefaultRegistry().ResetForTesting()
}
