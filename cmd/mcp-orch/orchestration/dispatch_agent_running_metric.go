package orchestration

import (
	"context"

	orchmetrics "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/metrics"
	taskdag "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/anthropic-ai/super-agent-v3/pkg/dagmetrics"
)

type DispatchAgentRunningMetrics = orchmetrics.DispatchAgentRunningMetrics

func DispatchAgentRunningCounters() DispatchAgentRunningMetrics {
	return orchmetrics.DispatchAgentRunningCounters()
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
