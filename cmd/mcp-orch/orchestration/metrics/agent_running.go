package metrics

import "sync/atomic"

// DispatchAgentRunningMetrics 是 agent 节点写入 running 状态的指标快照。
// 计数只观察 dispatcher 写库结果，不改变 DAG 节点状态机。
type DispatchAgentRunningMetrics struct {
	Written                int64
	SkippedAlreadyTerminal int64
	WriteFailed            int64
}

// DispatchAgentRunningOwner 保存单个 NodeExecutorRouter 的 running 写入指标。
// owner 必须由 router 构造时创建，不能在 package 范围内共享。
type DispatchAgentRunningOwner struct {
	written                atomic.Int64
	skippedAlreadyTerminal atomic.Int64
	writeFailed            atomic.Int64
}

// NewDispatchAgentRunningOwner 创建一个独立的 running 写入指标 owner。
func NewDispatchAgentRunningOwner() *DispatchAgentRunningOwner {
	return &DispatchAgentRunningOwner{}
}

// IncWritten 记录 agent 节点成功写入 running 的次数。
func (o *DispatchAgentRunningOwner) IncWritten() { o.written.Add(1) }

// IncSkippedAlreadyTerminal 记录 running 写入遇到已终态节点而跳过的次数。
func (o *DispatchAgentRunningOwner) IncSkippedAlreadyTerminal() {
	o.skippedAlreadyTerminal.Add(1)
}

// IncWriteFailed 累加运行中代理状态写入失败次数。
func (o *DispatchAgentRunningOwner) IncWriteFailed() { o.writeFailed.Add(1) }

// Snapshot 返回此 owner 的 agent running 写入路径计数快照。
func (o *DispatchAgentRunningOwner) Snapshot() DispatchAgentRunningMetrics {
	return DispatchAgentRunningMetrics{
		Written:                o.written.Load(),
		SkippedAlreadyTerminal: o.skippedAlreadyTerminal.Load(),
		WriteFailed:            o.writeFailed.Load(),
	}
}
