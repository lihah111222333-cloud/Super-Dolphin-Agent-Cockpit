// Package skillmetrics 提供 skill 运行路径的进程内观测计数器。
//
// Registry 是计数器的唯一 owner。调用方必须通过依赖注入持有它；本包没有
// 默认实例或进程级回退，避免不同 app/test 图无意共享观测状态。
package skillmetrics

import "sync/atomic"

// ApprovalMissWriter 记录 artifact 审批未命中。
type ApprovalMissWriter interface {
	IncArtifactApprovalMiss()
}

// TrimCorruptionFallbackWriter 记录 skill block footer 缺失时的裁剪回退。
type TrimCorruptionFallbackWriter interface {
	IncTrimCorruptionFallback()
}

// HostToolCallWriter 记录 host-direct 工具调用 outcome。
type HostToolCallWriter interface {
	IncHostToolCallOutcome(outcome string)
}

// EnrichFailureWriter 记录 Codex 工具调用参数 enrich 失败。
type EnrichFailureWriter interface {
	IncEnrichFailure()
}

// Writer 是完整 skill 指标写入契约。
type Writer interface {
	ApprovalMissWriter
	TrimCorruptionFallbackWriter
	HostToolCallWriter
	EnrichFailureWriter
}

// Registry 是一组显式拥有的并发安全计数器。
type Registry struct {
	artifactApprovalMissTotal    atomic.Uint64
	trimCorruptionFallbackCount  atomic.Uint64
	hostToolCallOKTotal          atomic.Uint64
	hostToolCallCWDMissingTotal  atomic.Uint64
	hostToolCallApprovalReqTotal atomic.Uint64
	hostToolCallErrorTotal       atomic.Uint64
	enrichFailuresTotal          atomic.Uint64
}

// NewRegistry 创建独立的指标 owner。
func NewRegistry() *Registry { return &Registry{} }

// IncArtifactApprovalMiss 对已建 artifact key 查询返回未审批时 +1。
func (r *Registry) IncArtifactApprovalMiss() { requireRegistry(r).artifactApprovalMissTotal.Add(1) }

// IncSkillArtifactApprovalMiss 是审批调用点的业务语义别名。
func (r *Registry) IncSkillArtifactApprovalMiss() { r.IncArtifactApprovalMiss() }

// IncTrimCorruptionFallback pair-fenced trim 找不到成对 footer 回落 legacy 时 +1。
func (r *Registry) IncTrimCorruptionFallback() { requireRegistry(r).trimCorruptionFallbackCount.Add(1) }

const (
	// HostToolOutcome* 是 IncHostToolCallOutcome 的合法 outcome 字面量。
	HostToolOutcomeOK               = "ok"
	HostToolOutcomeCWDMissing       = "cwd_missing"
	HostToolOutcomeApprovalRequired = "approval_required"
	HostToolOutcomeError            = "error"
)

// IncHostToolCallOutcome 记录一次 host-direct tool 调用的最终 outcome。未知
// outcome 保守计为 error。
func (r *Registry) IncHostToolCallOutcome(outcome string) {
	switch outcome {
	case HostToolOutcomeOK:
		requireRegistry(r).hostToolCallOKTotal.Add(1)
	case HostToolOutcomeCWDMissing:
		requireRegistry(r).hostToolCallCWDMissingTotal.Add(1)
	case HostToolOutcomeApprovalRequired:
		requireRegistry(r).hostToolCallApprovalReqTotal.Add(1)
	default:
		requireRegistry(r).hostToolCallErrorTotal.Add(1)
	}
}

// IncEnrichFailure 记录 codexapp tool-call params enrich fail-soft 失败一次。
func (r *Registry) IncEnrichFailure() { requireRegistry(r).enrichFailuresTotal.Add(1) }

// Snapshot 是 skill 指标的一次读取快照；字段顺序固定为原有七条 series。
type Snapshot struct {
	ArtifactApprovalMissTotal    uint64
	TrimCorruptionFallbackCount  uint64
	HostToolCallOKTotal          uint64
	HostToolCallCWDMissingTotal  uint64
	HostToolCallApprovalReqTotal uint64
	HostToolCallErrorTotal       uint64
	EnrichFailuresTotal          uint64
}

// Snapshot 返回当前 owner 的七条 skill 指标快照。
func (r *Registry) Snapshot() Snapshot {
	r = requireRegistry(r)
	return Snapshot{
		ArtifactApprovalMissTotal:    r.artifactApprovalMissTotal.Load(),
		TrimCorruptionFallbackCount:  r.trimCorruptionFallbackCount.Load(),
		HostToolCallOKTotal:          r.hostToolCallOKTotal.Load(),
		HostToolCallCWDMissingTotal:  r.hostToolCallCWDMissingTotal.Load(),
		HostToolCallApprovalReqTotal: r.hostToolCallApprovalReqTotal.Load(),
		HostToolCallErrorTotal:       r.hostToolCallErrorTotal.Load(),
		EnrichFailuresTotal:          r.enrichFailuresTotal.Load(),
	}
}

func requireRegistry(r *Registry) *Registry {
	if r == nil {
		panic("skillmetrics: registry is required")
	}
	return r
}
