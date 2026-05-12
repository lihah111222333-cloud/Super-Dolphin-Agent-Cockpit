package orchestration

import "sync/atomic"

// DAGFallbackMetrics 是 hookConsumer.handleThreadStopped 内 DAG fallback 分支
// （ADR-017 v1.2 §2.5 + §3.4）的只读快照。每个标签独立 counter，与
// stop_metric.go / dag_subscriber_metric.go 同款 atomic.Int64 范式
// （项目无 Prometheus collector — ADR-016 v1.2 §2.5）。
//
//   - dag_node_thread_stopped_fallback_lookup_failed_total
//   - dag_node_thread_stopped_fallback_no_node_total
//   - dag_node_thread_stopped_fallback_idempotent_skipped_total
//   - dag_node_thread_stopped_fallback_failed_total       成功把节点推 failed
//   - dag_node_thread_stopped_fallback_fail_node_err_total FailNodeAndCancelDownstream SQL 失败
type DAGFallbackMetrics struct {
	LookupFailed      int64
	NoNode            int64
	IdempotentSkipped int64
	Failed            int64
	FailNodeErr       int64
}

// dagFallbackCounter 是包私有 sink。Lock-free Inc + Snapshot.
type dagFallbackCounter struct {
	lookupFailed      atomic.Int64
	noNode            atomic.Int64
	idempotentSkipped atomic.Int64
	failed            atomic.Int64
	failNodeErr       atomic.Int64
}

func (c *dagFallbackCounter) IncLookupFailed() {
	if c == nil {
		return
	}
	c.lookupFailed.Add(1)
}

func (c *dagFallbackCounter) IncNoNode() {
	if c == nil {
		return
	}
	c.noNode.Add(1)
}

func (c *dagFallbackCounter) IncIdempotentSkipped() {
	if c == nil {
		return
	}
	c.idempotentSkipped.Add(1)
}

func (c *dagFallbackCounter) IncFailed() {
	if c == nil {
		return
	}
	c.failed.Add(1)
}

func (c *dagFallbackCounter) IncFailNodeErr() {
	if c == nil {
		return
	}
	c.failNodeErr.Add(1)
}

func (c *dagFallbackCounter) Snapshot() DAGFallbackMetrics {
	if c == nil {
		return DAGFallbackMetrics{}
	}
	return DAGFallbackMetrics{
		LookupFailed:      c.lookupFailed.Load(),
		NoNode:            c.noNode.Load(),
		IdempotentSkipped: c.idempotentSkipped.Load(),
		Failed:            c.failed.Load(),
		FailNodeErr:       c.failNodeErr.Load(),
	}
}

// dagFallbackMetrics 是包单例，由 hookConsumer.runThreadStoppedDAGFallback 写入。
// 外部观测通过 DAGFallbackCounters() 拿快照，命名与 DAGSubscriberCounters /
// StopSpawnedAgentCounters 对齐。
var dagFallbackMetrics = &dagFallbackCounter{}

// DAGFallbackCounters 返回 thread.stopped DAG fallback 当前 counter 快照。
func DAGFallbackCounters() DAGFallbackMetrics {
	return dagFallbackMetrics.Snapshot()
}
