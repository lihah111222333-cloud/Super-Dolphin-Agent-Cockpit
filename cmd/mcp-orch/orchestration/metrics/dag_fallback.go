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

// IncDAGFallbackNoNode 累加DAG兜底no节点。
func IncDAGFallbackNoNode() { dagFallback.noNode.Add(1) }

// IncDAGFallbackIdempotentSkipped 累加DAG兜底idempotentskipped。
func IncDAGFallbackIdempotentSkipped() { dagFallback.idempotentSkipped.Add(1) }

// IncDAGFallbackFailed 累加DAG兜底failed。
func IncDAGFallbackFailed() { dagFallback.failed.Add(1) }

// IncDAGFallbackFailNodeErr 累加DAG兜底fail节点err。
func IncDAGFallbackFailNodeErr() { dagFallback.failNodeErr.Add(1) }

// DAGFallbackCounters 处理DAG兜底counters。
func DAGFallbackCounters() DAGFallbackMetrics {
	return dagFallback.snapshot()
}
