package metrics

import "sync/atomic"

// DispatchAgentRunningMetrics 是 agent 节点写入 running 状态的指标快照。
// 计数只观察 dispatcher 写库结果，不改变 DAG 节点状态机。
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

// IncDispatchAgentRunningWritten 记录 agent 节点成功写入 running 的次数。
func IncDispatchAgentRunningWritten() { dispatchAgentRunning.written.Add(1) }

// IncDispatchAgentRunningSkippedAlreadyTerminal 记录 running 写入遇到已终态节点而跳过的次数。
func IncDispatchAgentRunningSkippedAlreadyTerminal() {
	dispatchAgentRunning.skippedAlreadyTerminal.Add(1)
}

// IncDispatchAgentRunningWriteFailed 累加运行中代理状态写入失败次数。
func IncDispatchAgentRunningWriteFailed() { dispatchAgentRunning.writeFailed.Add(1) }

// DispatchAgentRunningCounters 返回 agent running 写入路径的计数快照。
func DispatchAgentRunningCounters() DispatchAgentRunningMetrics {
	return dispatchAgentRunning.snapshot()
}
