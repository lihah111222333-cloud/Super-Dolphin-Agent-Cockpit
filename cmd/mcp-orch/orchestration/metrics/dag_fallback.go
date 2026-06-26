package metrics

import "sync/atomic"

type DAGFallbackMetrics struct {
	LookupFailed      int64
	NoNode            int64
	IdempotentSkipped int64
	Failed            int64
	FailNodeErr       int64
}

type dagFallbackCounter struct {
	lookupFailed      atomic.Int64
	noNode            atomic.Int64
	idempotentSkipped atomic.Int64
	failed            atomic.Int64
	failNodeErr       atomic.Int64
}

func (c *dagFallbackCounter) snapshot() DAGFallbackMetrics {
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

var dagFallback = &dagFallbackCounter{}

// IncDAGFallbackLookupFailed 累加 DAG 兜底查询失败次数。
func IncDAGFallbackLookupFailed() { dagFallback.lookupFailed.Add(1) }

// IncDAGFallbackNoNode 记录 stopped 线程未关联到 DAG 节点的次数。
func IncDAGFallbackNoNode() { dagFallback.noNode.Add(1) }

// IncDAGFallbackIdempotentSkipped 记录兜底推进命中已终态节点而跳过的次数。
func IncDAGFallbackIdempotentSkipped() { dagFallback.idempotentSkipped.Add(1) }

// IncDAGFallbackFailed 记录兜底推进成功把节点标记为 failed 的次数。
func IncDAGFallbackFailed() { dagFallback.failed.Add(1) }

// IncDAGFallbackFailNodeErr 记录兜底推进调用 fail-node 持久化失败的次数。
func IncDAGFallbackFailNodeErr() { dagFallback.failNodeErr.Add(1) }

// DAGFallbackCounters 返回 stopped-thread DAG 兜底推进的计数快照。
func DAGFallbackCounters() DAGFallbackMetrics {
	return dagFallback.snapshot()
}
