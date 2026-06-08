package orchestration

import "sync/atomic"

type StopSpawnedAgentMetrics struct {
	Success                int64
	SkippedAlreadyStopped  int64
	SkippedAlreadyArchived int64
	SkippedBindingMissing  int64
	SkippedNoThreadID      int64
	SkippedLookupFailed    int64
	Failed                 int64
}

type stopSpawnedAgentCounter struct {
	success                atomic.Int64
	skippedAlreadyStopped  atomic.Int64
	skippedAlreadyArchived atomic.Int64
	skippedBindingMissing  atomic.Int64
	skippedNoThreadID      atomic.Int64
	skippedLookupFailed    atomic.Int64
	failed                 atomic.Int64
}

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

var defaultStopSpawnedAgentCounter = &stopSpawnedAgentCounter{}

func recordStopSpawnedAgentMetric(result StopResult) {
	defaultStopSpawnedAgentCounter.Inc(result)
}

func StopSpawnedAgentCounters() StopSpawnedAgentMetrics {
	return defaultStopSpawnedAgentCounter.Snapshot()
}
