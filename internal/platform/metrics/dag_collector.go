package metrics

import (
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dagmetrics"
	"github.com/prometheus/client_golang/prometheus"
)

var dagRetryCountPerNodeDesc = prometheus.NewDesc(
	"retry_count_per_node",
	"Highest committed wakeup retry attempt observed per DAG node, capped by in-process series budget.",
	[]string{"dag_key", "node_key"},
	nil,
)

type dagRetryCountPerNodeCollector struct{ source *dagmetrics.Registry }

func (dagRetryCountPerNodeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- dagRetryCountPerNodeDesc
}

func (c dagRetryCountPerNodeCollector) Collect(ch chan<- prometheus.Metric) {
	for _, count := range c.source.Read().RetryCountPerNode {
		ch <- prometheus.MustNewConstMetric(dagRetryCountPerNodeDesc, prometheus.CounterValue, float64(count.Count), count.DagKey, count.NodeKey)
	}
}

// DAGCollector owns one DAG metrics registry and its isolated Prometheus export.
type DAGCollector struct {
	source   *dagmetrics.Registry
	registry *prometheus.Registry
}

// NewDAGCollector creates the process-local DAG metrics owner used by mcp-orch.
func NewDAGCollector() *DAGCollector {
	collector, err := NewDAGCollectorFor(dagmetrics.NewRegistry())
	if err != nil {
		panic(fmt.Sprintf("metrics: create DAG collector: %v", err))
	}
	return collector
}

// NewDAGCollectorFor exports a supplied DAG registry without using DefaultRegisterer.
func NewDAGCollectorFor(source *dagmetrics.Registry) (*DAGCollector, error) {
	if source == nil {
		return nil, fmt.Errorf("metrics: DAG registry is required")
	}
	registry := prometheus.NewRegistry()
	registry.MustRegister(
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dispatch_failed_total", Help: "Number of wakeup dispatch attempts that were committed as failed."}, func() float64 { return float64(source.Read().DispatchFailedTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "dispatch_retry_alert_total", Help: "Number of DAG node retry threshold alerts triggered."}, func() float64 { return float64(source.Read().RetryAlertTotal) }),
		prometheus.NewCounterFunc(prometheus.CounterOpts{Name: "retry_count_per_node_overflow_total", Help: "Number of DAG node retry observations not exported with per-node labels after the in-process series cap."}, func() float64 { return float64(source.Read().RetryCountPerNodeOverflow) }),
		dagRetryCountPerNodeCollector{source: source},
	)
	return &DAGCollector{source: source, registry: registry}, nil
}

// Registry returns the explicit mutable DAG metrics source for mcp-orch emission.
func (c *DAGCollector) Registry() *dagmetrics.Registry { return c.source }

// Gatherer returns the collector's isolated Prometheus gatherer.
func (c *DAGCollector) Gatherer() prometheus.Gatherer { return c.registry }
