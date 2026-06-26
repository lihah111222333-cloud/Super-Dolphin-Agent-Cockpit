// Package metrics 声明 super-agent-v3 进程级 Prometheus 指标。
// 本包是 bootstrap、skill 和 DAG 指标的统一注册点，指标名称和 label 维度由 archtest 反向校验。
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// BootstrapHeartbeatFailures 统计 bootstrap heartbeat 非 nil 错误，label 与日志锚点保持一致。
	BootstrapHeartbeatFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bootstrap_heartbeat_failures_total",
			Help: "Number of bootstrap heartbeat failures, labelled by binary_name and client_kind.",
		},
		[]string{"binary_name", "client_kind"},
	)

	// BootstrapReportQueueDropped 统计 durable report 因客户端队列满而被拒绝的入队尝试。
	BootstrapReportQueueDropped = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "bootstrap_report_queue_dropped_total",
			Help: "Number of bootstrap report enqueue attempts dropped because the durable queue was full.",
		},
	)

	// BootstrapReconnectAttempts 统计 bootstrap 重连循环次数，outcome 只允许 success 或 fail。
	BootstrapReconnectAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "bootstrap_reconnect_attempts_total",
			Help: "Number of bootstrap reconnect attempts, labelled by outcome (success|fail).",
		},
		[]string{"outcome"},
	)
)
