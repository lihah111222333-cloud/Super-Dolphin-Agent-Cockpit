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

func IncDAGFallbackLookupFailed()      { dagFallback.lookupFailed.Add(1) }
func IncDAGFallbackNoNode()            { dagFallback.noNode.Add(1) }
func IncDAGFallbackIdempotentSkipped() { dagFallback.idempotentSkipped.Add(1) }
func IncDAGFallbackFailed()            { dagFallback.failed.Add(1) }
func IncDAGFallbackFailNodeErr()       { dagFallback.failNodeErr.Add(1) }
func DAGFallbackCounters() DAGFallbackMetrics {
	return dagFallback.snapshot()
}
