package metrics

import (
	"github.com/anthropic-ai/super-agent-v3/pkg/dagmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// DAG dispatch/retry 指标直接读取 dagmetrics 快照，避免在热路径重复注册 label 维度。
	DAGDispatchFailedTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "dispatch_failed_total",
			Help: "Number of wakeup dispatch attempts that were committed as failed.",
		},
		func() float64 { return float64(dagmetrics.Read().DispatchFailedTotal) },
	)

	DAGRetryAlertTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "dispatch_retry_alert_total",
			Help: "Number of DAG node retry threshold alerts triggered.",
		},
		func() float64 { return float64(dagmetrics.Read().RetryAlertTotal) },
	)

	DAGRetryCountPerNodeOverflowTotal = promauto.NewCounterFunc(
		prometheus.CounterOpts{
			Name: "retry_count_per_node_overflow_total",
			Help: "Number of DAG node retry observations not exported with per-node labels after the in-process series cap.",
		},
		func() float64 { return float64(dagmetrics.Read().RetryCountPerNodeOverflow) },
	)

	dagRetryCountPerNodeDesc = prometheus.NewDesc(
		"retry_count_per_node",
		"Highest committed wakeup retry attempt observed per DAG node, capped by in-process series budget.",
		[]string{"dag_key", "node_key"},
		nil,
	)

	// 自定义 collector 需要在包初始化时注册；helper 返回 bool 只是为了能放在 var 初始化中。
	_ = registerDAGRetryCountPerNodeCollector()
)

// registerDAGRetryCountPerNodeCollector 注册自定义 collector，并通过 bool 返回值适配 var 初始化。
func registerDAGRetryCountPerNodeCollector() bool {
	prometheus.MustRegister(dagRetryCountPerNodeCollector{})
	return true
}

// dagRetryCountPerNodeCollector 导出 per-node retry 计数，series 数量由 dagmetrics 内部预算控制。
type dagRetryCountPerNodeCollector struct{}

// Describe 向 Prometheus 声明 retry_count_per_node 的 descriptor。
func (dagRetryCountPerNodeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- dagRetryCountPerNodeDesc
}

// Collect 读取当前 DAG retry 快照并按 dag_key/node_key 输出计数。
func (dagRetryCountPerNodeCollector) Collect(ch chan<- prometheus.Metric) {
	for _, count := range dagmetrics.Read().RetryCountPerNode {
		ch <- prometheus.MustNewConstMetric(
			dagRetryCountPerNodeDesc,
			prometheus.CounterValue,
			float64(count.Count),
			count.DagKey,
			count.NodeKey,
		)
	}
}
