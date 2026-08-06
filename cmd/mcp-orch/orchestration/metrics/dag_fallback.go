package metrics

import "sync/atomic"

// DAGFallbackMetrics 是 stopped-thread 兜底推进 DAG 节点的指标快照。
// 它只暴露观测计数，真实节点终态仍由 orchestration 持久化流程写入。
type DAGFallbackMetrics struct {
	LookupFailed      int64
	NoNode            int64
	IdempotentSkipped int64
	Failed            int64
	FailNodeErr       int64
}

// DAGFallbackOwner 保存单个 hookConsumer 的 stopped-thread DAG fallback 指标。
// owner 必须由 hook consumer 构造时创建，不能在 package 范围内共享。
type DAGFallbackOwner struct {
	lookupFailed      atomic.Int64
	noNode            atomic.Int64
	idempotentSkipped atomic.Int64
	failed            atomic.Int64
	failNodeErr       atomic.Int64
}

// NewDAGFallbackOwner 创建一个独立的 stopped-thread DAG fallback 指标 owner。
func NewDAGFallbackOwner() *DAGFallbackOwner {
	return &DAGFallbackOwner{}
}

// IncLookupFailed 累加 DAG 兜底查询失败次数。
func (o *DAGFallbackOwner) IncLookupFailed() { o.lookupFailed.Add(1) }

// IncNoNode 记录 stopped 线程未关联到 DAG 节点的次数。
func (o *DAGFallbackOwner) IncNoNode() { o.noNode.Add(1) }

// IncIdempotentSkipped 记录兜底推进命中已终态节点而跳过的次数。
func (o *DAGFallbackOwner) IncIdempotentSkipped() { o.idempotentSkipped.Add(1) }

// IncFailed 记录兜底推进成功把节点标记为 failed 的次数。
func (o *DAGFallbackOwner) IncFailed() { o.failed.Add(1) }

// IncFailNodeErr 记录兜底推进调用 fail-node 持久化失败的次数。
func (o *DAGFallbackOwner) IncFailNodeErr() { o.failNodeErr.Add(1) }

// Snapshot 返回此 owner 的 stopped-thread DAG fallback 计数快照。
func (o *DAGFallbackOwner) Snapshot() DAGFallbackMetrics {
	return DAGFallbackMetrics{
		LookupFailed:      o.lookupFailed.Load(),
		NoNode:            o.noNode.Load(),
		IdempotentSkipped: o.idempotentSkipped.Load(),
		Failed:            o.failed.Load(),
		FailNodeErr:       o.failNodeErr.Load(),
	}
}
