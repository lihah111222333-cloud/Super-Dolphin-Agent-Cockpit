package metrics

import (
	"github.com/anthropic-ai/super-agent-v3/pkg/dagmetrics"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
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

	// Register the custom collector at package init time via a var
	// assignment. The helper returns a bool so we can use it in a var
	// declaration (prometheus.MustRegister itself returns nothing).
	_ = registerDAGRetryCountPerNodeCollector()
)

func registerDAGRetryCountPerNodeCollector() bool {
	prometheus.MustRegister(dagRetryCountPerNodeCollector{})
	return true
}

type dagRetryCountPerNodeCollector struct{}

func (dagRetryCountPerNodeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- dagRetryCountPerNodeDesc
}

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
