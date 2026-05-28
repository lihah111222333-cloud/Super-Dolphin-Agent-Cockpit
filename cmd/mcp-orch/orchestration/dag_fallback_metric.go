package orchestration

import "sync/atomic"

// DAGFallbackMetrics is the read-only snapshot of thread.stopped DAG fallback counters.
type DAGFallbackMetrics struct {
	LookupFailed      int64
	NoNode            int64
	IdempotentSkipped int64
	Failed            int64
	FailNodeErr       int64
}

// dagFallbackCounter is the package-private sink. Lock-free Inc + Snapshot.
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

var dagFallbackMetrics = &dagFallbackCounter{}

func DAGFallbackCounters() DAGFallbackMetrics {
	return dagFallbackMetrics.Snapshot()
}
