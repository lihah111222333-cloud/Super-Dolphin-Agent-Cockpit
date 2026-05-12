package orchestration

import "sync/atomic"

// DAGSubscriberMetrics is a read-only snapshot of the
// dag_turn_completed_subscriber_* counters set by
// RegisterDAGTurnCompletedSubscriber (ADR-017 v1.2 §2.1 / §2.6 / §2.7).
//
// Wire labels (snake_case) — every label is a stand-alone counter; not
// emitted as Prometheus labels because the project ships no collector
// (ADR-016 v1.2 §2.5).
//
//   - dag_node_complete_done_total              §2.8 happy path - done
//   - dag_node_complete_failed_total            §2.8 happy path - failed
//   - dag_node_status_idempotent_skipped_total  §2.6 race C / 重复 TurnCompleted
//   - dag_node_lookup_no_node_total             §2.2 反查空
//   - dag_node_lookup_dirty_data_total          §2.2 N>1 dirty data
//   - dag_node_lookup_failed_total              §2.2 反查 DB 错
//   - dag_node_complete_size_cap_exceeded_total §2.7 result 超 4KB cap
//   - dag_node_complete_result_empty_total      §3.1 ev.Result 仍空报警
//
// Mirrors stop_metric.go / dispatch_agent_running_metric.go atomic.Int64
// pattern (project has no Prometheus collector — local atomic snapshot is
// the metric store).
type DAGSubscriberMetrics struct {
	CompleteDone               int64
	CompleteFailed             int64
	IdempotentSkipped          int64
	LookupNoNode               int64
	LookupDirtyData            int64
	LookupFailed               int64
	CompleteSizeCapExceeded    int64
	CompleteResultEmpty        int64
}

// dagSubscriberCounter is the package-private sink. Lock-free Inc + Snapshot.
type dagSubscriberCounter struct {
	completeDone            atomic.Int64
	completeFailed          atomic.Int64
	idempotentSkipped       atomic.Int64
	lookupNoNode            atomic.Int64
	lookupDirtyData         atomic.Int64
	lookupFailed            atomic.Int64
	completeSizeCapExceeded atomic.Int64
	completeResultEmpty     atomic.Int64
}

func (c *dagSubscriberCounter) IncCompleteDone() {
	if c == nil {
		return
	}
	c.completeDone.Add(1)
}

func (c *dagSubscriberCounter) IncCompleteFailed() {
	if c == nil {
		return
	}
	c.completeFailed.Add(1)
}

func (c *dagSubscriberCounter) IncIdempotentSkipped() {
	if c == nil {
		return
	}
	c.idempotentSkipped.Add(1)
}

func (c *dagSubscriberCounter) IncLookupNoNode() {
	if c == nil {
		return
	}
	c.lookupNoNode.Add(1)
}

func (c *dagSubscriberCounter) IncLookupDirtyData() {
	if c == nil {
		return
	}
	c.lookupDirtyData.Add(1)
}

func (c *dagSubscriberCounter) IncLookupFailed() {
	if c == nil {
		return
	}
	c.lookupFailed.Add(1)
}

func (c *dagSubscriberCounter) IncCompleteSizeCapExceeded() {
	if c == nil {
		return
	}
	c.completeSizeCapExceeded.Add(1)
}

func (c *dagSubscriberCounter) IncCompleteResultEmpty() {
	if c == nil {
		return
	}
	c.completeResultEmpty.Add(1)
}

func (c *dagSubscriberCounter) Snapshot() DAGSubscriberMetrics {
	if c == nil {
		return DAGSubscriberMetrics{}
	}
	return DAGSubscriberMetrics{
		CompleteDone:            c.completeDone.Load(),
		CompleteFailed:          c.completeFailed.Load(),
		IdempotentSkipped:       c.idempotentSkipped.Load(),
		LookupNoNode:            c.lookupNoNode.Load(),
		LookupDirtyData:         c.lookupDirtyData.Load(),
		LookupFailed:            c.lookupFailed.Load(),
		CompleteSizeCapExceeded: c.completeSizeCapExceeded.Load(),
		CompleteResultEmpty:     c.completeResultEmpty.Load(),
	}
}

// dagSubscriberMetrics is the package singleton consulted by the subscriber.
// Public access goes through DAGSubscriberCounters() — stable API surface.
var dagSubscriberMetrics = &dagSubscriberCounter{}

// DAGSubscriberCounters returns a snapshot of the
// dag_turn_completed_subscriber_* counters for observability / debug
// dashboards. Mirrors StopSpawnedAgentCounters naming.
func DAGSubscriberCounters() DAGSubscriberMetrics {
	return dagSubscriberMetrics.Snapshot()
}
