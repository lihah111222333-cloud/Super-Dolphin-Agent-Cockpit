package metrics

import "sync/atomic"

type DispatchAgentRunningMetrics struct {
	Written                int64
	SkippedAlreadyTerminal int64
	WriteFailed            int64
}

type dispatchAgentRunningCounter struct {
	written                atomic.Int64
	skippedAlreadyTerminal atomic.Int64
	writeFailed            atomic.Int64
}

func (c *dispatchAgentRunningCounter) snapshot() DispatchAgentRunningMetrics {
	if c == nil {
		return DispatchAgentRunningMetrics{}
	}
	return DispatchAgentRunningMetrics{
		Written:                c.written.Load(),
		SkippedAlreadyTerminal: c.skippedAlreadyTerminal.Load(),
		WriteFailed:            c.writeFailed.Load(),
	}
}

var dispatchAgentRunning = &dispatchAgentRunningCounter{}

func IncDispatchAgentRunningWritten() { dispatchAgentRunning.written.Add(1) }
func IncDispatchAgentRunningSkippedAlreadyTerminal() {
	dispatchAgentRunning.skippedAlreadyTerminal.Add(1)
}
func IncDispatchAgentRunningWriteFailed() { dispatchAgentRunning.writeFailed.Add(1) }
func DispatchAgentRunningCounters() DispatchAgentRunningMetrics {
	return dispatchAgentRunning.snapshot()
}
