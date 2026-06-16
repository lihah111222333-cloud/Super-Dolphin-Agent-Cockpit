package metrics

import "sync/atomic"

// DispatchAgentRunningMetrics describes metrics integration data.
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

// IncDispatchAgentRunningWritten 累加dispatch代理runningwritten。
func IncDispatchAgentRunningWritten() { dispatchAgentRunning.written.Add(1) }

// IncDispatchAgentRunningSkippedAlreadyTerminal 累加dispatch代理runningskippedalreadyterminal。
func IncDispatchAgentRunningSkippedAlreadyTerminal() {
	dispatchAgentRunning.skippedAlreadyTerminal.Add(1)
}

// IncDispatchAgentRunningWriteFailed 累加运行中代理状态写入失败次数。
func IncDispatchAgentRunningWriteFailed() { dispatchAgentRunning.writeFailed.Add(1) }

// DispatchAgentRunningCounters 派发代理runningcounters。
func DispatchAgentRunningCounters() DispatchAgentRunningMetrics {
	return dispatchAgentRunning.snapshot()
}
