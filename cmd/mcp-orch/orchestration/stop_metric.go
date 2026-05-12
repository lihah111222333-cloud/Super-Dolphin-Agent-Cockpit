package orchestration

import "sync/atomic"

// StopSpawnedAgentMetrics is a read-only snapshot of the
// dag_node_stop_spawned_agent_total counter, broken down by
// {result} label. The struct mirrors notify/subscribers.go's
// Metrics range — that file is the project's reference pattern
// for subscriber-side counters (atomic.Int64 + Metrics() accessor),
// since cmd/mcp-orch has no Prometheus collector / dispatcher metric
// store (ADR-016 v1.2 §2.5 揭出 — F15.1 不是通用框架).
//
// Field names use Go CamelCase; the wire labels for
// dag_node_stop_spawned_agent_total are the StopResult string values
// (snake_case) — kept in sync via stopSpawnedAgentCounter.Snapshot.
type StopSpawnedAgentMetrics struct {
	Success                int64
	SkippedAlreadyStopped  int64
	SkippedAlreadyArchived int64
	SkippedBindingMissing  int64
	SkippedNoThreadID      int64
	SkippedLookupFailed    int64
	Failed                 int64
}

// stopSpawnedAgentCounter implements stopSpawnedAgentSink with
// atomic.Int64 per result label. Lock-free Inc + lock-free Snapshot
// so it is safe to read from /metrics-style endpoints concurrently
// with subscriber stop calls.
//
// No {provider} / {reason} labels per ADR-016 §2.5 (those would be
// constant for C3 — recorded as dimension drift in the ADR).
type stopSpawnedAgentCounter struct {
	success                atomic.Int64
	skippedAlreadyStopped  atomic.Int64
	skippedAlreadyArchived atomic.Int64
	skippedBindingMissing  atomic.Int64
	skippedNoThreadID      atomic.Int64
	skippedLookupFailed    atomic.Int64
	failed                 atomic.Int64
}

// Inc satisfies stopSpawnedAgentSink. Unknown StopResult values
// (defensive: should be unreachable) are dropped silently so future
// label additions in a separate commit cannot panic the subscriber.
func (c *stopSpawnedAgentCounter) Inc(result StopResult) {
	if c == nil {
		return
	}
	switch result {
	case StopResultSuccess:
		c.success.Add(1)
	case StopResultSkippedAlreadyStopped:
		c.skippedAlreadyStopped.Add(1)
	case StopResultSkippedAlreadyArchived:
		c.skippedAlreadyArchived.Add(1)
	case StopResultSkippedBindingMissing:
		c.skippedBindingMissing.Add(1)
	case StopResultSkippedNoThreadID:
		c.skippedNoThreadID.Add(1)
	case StopResultSkippedLookupFailed:
		c.skippedLookupFailed.Add(1)
	case StopResultFailed:
		c.failed.Add(1)
	}
}

// Snapshot returns a copy of every counter value. Safe to call
// concurrently with Inc.
func (c *stopSpawnedAgentCounter) Snapshot() StopSpawnedAgentMetrics {
	if c == nil {
		return StopSpawnedAgentMetrics{}
	}
	return StopSpawnedAgentMetrics{
		Success:                c.success.Load(),
		SkippedAlreadyStopped:  c.skippedAlreadyStopped.Load(),
		SkippedAlreadyArchived: c.skippedAlreadyArchived.Load(),
		SkippedBindingMissing:  c.skippedBindingMissing.Load(),
		SkippedNoThreadID:      c.skippedNoThreadID.Load(),
		SkippedLookupFailed:    c.skippedLookupFailed.Load(),
		Failed:                 c.failed.Load(),
	}
}

// defaultStopSpawnedAgentCounter is the package-wide singleton bound
// to stopSpawnedAgentMetrics by init(). Kept unexported so the only
// way to read it is StopSpawnedAgentCounters() — gives us a stable
// API surface (the singleton may grow finer labels later).
var defaultStopSpawnedAgentCounter = &stopSpawnedAgentCounter{}

func init() {
	stopSpawnedAgentMetrics = defaultStopSpawnedAgentCounter
}

// StopSpawnedAgentCounters returns a snapshot of the
// dag_node_stop_spawned_agent_total counter for observability /
// debug dashboards. Mirrors notify.DAGNotifier.Metrics() naming.
//
// No fx Provide wiring needed — counter is a package singleton with
// atomic.Int64 fields, identical to notify/subscribers.go pattern.
// Callers that need a per-instance counter can construct a private
// *stopSpawnedAgentCounter via test-only helpers.
func StopSpawnedAgentCounters() StopSpawnedAgentMetrics {
	return defaultStopSpawnedAgentCounter.Snapshot()
}
