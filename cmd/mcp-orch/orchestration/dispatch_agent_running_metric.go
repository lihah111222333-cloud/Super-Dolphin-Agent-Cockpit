package orchestration

import (
	"context"
	"sync/atomic"

	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/pkg/dagmetrics"
)

// DispatchAgentRunningMetrics is a read-only snapshot of the
// dag_node_running_* counters set by NodeExecutorRouter.dispatchAgent after
// the ready→running write (ADR-017 v1.2 §2.4).
//
// Wire labels (snake_case):
//   - dag_node_running_written_total
//   - dag_node_running_skipped_already_terminal_total (race window D)
//   - dag_node_running_write_failed_total
//
// Mirrors notify/subscribers.go + stop_metric.go atomic.Int64 pattern; the
// project has no Prometheus collector so this is the local "metric store".
type DispatchAgentRunningMetrics struct {
	Written                int64
	SkippedAlreadyTerminal int64
	WriteFailed            int64
}

// dispatchAgentRunningCounter implements the package-private sink. Lock-free
// Inc + Snapshot, safe to read concurrently with subscriber writes.
type dispatchAgentRunningCounter struct {
	written                atomic.Int64
	skippedAlreadyTerminal atomic.Int64
	writeFailed            atomic.Int64
}

func (c *dispatchAgentRunningCounter) IncWritten() {
	if c == nil {
		return
	}
	c.written.Add(1)
}

func (c *dispatchAgentRunningCounter) IncSkippedAlreadyTerminal() {
	if c == nil {
		return
	}
	c.skippedAlreadyTerminal.Add(1)
}

func (c *dispatchAgentRunningCounter) IncWriteFailed() {
	if c == nil {
		return
	}
	c.writeFailed.Add(1)
}

func (c *dispatchAgentRunningCounter) Snapshot() DispatchAgentRunningMetrics {
	if c == nil {
		return DispatchAgentRunningMetrics{}
	}
	return DispatchAgentRunningMetrics{
		Written:                c.written.Load(),
		SkippedAlreadyTerminal: c.skippedAlreadyTerminal.Load(),
		WriteFailed:            c.writeFailed.Load(),
	}
}

// dispatchAgentRunningMetrics is the package singleton consulted by
// NodeExecutorRouter.advanceAgentNodeToRunning. Public access goes through
// DispatchAgentRunningCounters() — gives us a stable API surface.
var dispatchAgentRunningMetrics = &dispatchAgentRunningCounter{}

// DispatchAgentRunningCounters returns a snapshot of the
// dag_node_running_{written,skipped_already_terminal,write_failed}_total
// counters for observability / debug dashboards. Mirrors
// StopSpawnedAgentCounters naming.
func DispatchAgentRunningCounters() DispatchAgentRunningMetrics {
	return dispatchAgentRunningMetrics.Snapshot()
}

type DispatchRetryMetrics struct {
	DispatchFailedTotal       int64
	RetryCountPerNode         map[string]int64
	RetryCountPerNodeOverflow int64
	RetryAlertTotal           int64
}

type DispatchRetryAlert struct {
	DagKey        string
	NodeKey       string
	TargetAgentID string
	WakeupID      int64
	AttemptCount  int32
	RetryCount    int64
	LastError     string
}

type DispatchRetryAlertSink interface {
	AlertDispatchRetry(ctx context.Context, alert DispatchRetryAlert) error
}

func DispatchRetryCounters() DispatchRetryMetrics {
	snap := dagmetrics.Read()
	perNode := make(map[string]int64, len(snap.RetryCountPerNode))
	for _, count := range snap.RetryCountPerNode {
		perNode[count.DagKey+"/"+count.NodeKey] = int64(count.Count)
	}
	return DispatchRetryMetrics{
		DispatchFailedTotal:       int64(snap.DispatchFailedTotal),
		RetryCountPerNode:         perNode,
		RetryCountPerNodeOverflow: int64(snap.RetryCountPerNodeOverflow),
		RetryAlertTotal:           int64(snap.RetryAlertTotal),
	}
}

func recordDispatchFailedMetric() {
	dagmetrics.IncDispatchFailed()
}

func recordDispatchRetryMetric(w *taskdag.Wakeup, lastErr string) (DispatchRetryAlert, bool) {
	if w == nil {
		return DispatchRetryAlert{}, false
	}
	attemptCount := w.AttemptCount
	if attemptCount < 1 {
		attemptCount = 1
	}
	record := dagmetrics.RecordRetry(w.DagKey, w.NodeKey, attemptCount)
	if record.DagKey == "" || record.NodeKey == "" {
		return DispatchRetryAlert{}, false
	}
	return DispatchRetryAlert{
		DagKey:        record.DagKey,
		NodeKey:       record.NodeKey,
		TargetAgentID: w.TargetAgentID,
		WakeupID:      w.ID,
		AttemptCount:  record.AttemptCount,
		RetryCount:    int64(record.Count),
		LastError:     lastErr,
	}, record.ShouldAlert
}

func resetDispatchRetryMetricsForTesting() {
	dagmetrics.ResetForTesting()
}
