package metrics

import "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dagmetrics"

// DispatchRetryMetrics 是 dispatcher 重试告警的只读快照。
// 数据来源于 metrics owner 的 DAG 计数器，调用方不得据此反推或修改调度状态。
type DispatchRetryMetrics struct {
	DispatchFailedTotal       int64
	RetryCountPerNode         map[string]int64
	RetryCountPerNodeOverflow int64
	RetryAlertTotal           int64
}

// DispatchRetryCounters 从显式 metrics owner 读取 dispatcher 重试计数快照。
func DispatchRetryCounters(source *dagmetrics.Registry) DispatchRetryMetrics {
	if source == nil {
		panic("dispatch retry metrics registry is required")
	}
	snap := source.Read()
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

// ResetDispatchRetryForTesting 重置调用方显式持有的 dispatcher 重试指标。
func ResetDispatchRetryForTesting(source *dagmetrics.Registry) {
	if source == nil {
		panic("dispatch retry metrics registry is required")
	}
	source.ResetForTesting()
}
