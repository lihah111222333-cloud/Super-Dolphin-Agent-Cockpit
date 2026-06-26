// Package skillmetrics 提供 skill 运行路径的进程内观测计数器。
// 计数点分布在 skill 模块、toolbridge 和 Codex provider，放在 leaf 包可让上下层共享而不制造反向依赖。
package skillmetrics

import "sync/atomic"

var (
	artifactApprovalMissTotal    atomic.Uint64
	trimCorruptionFallbackCount  atomic.Uint64
	hostToolCallOKTotal          atomic.Uint64
	hostToolCallCWDMissingTotal  atomic.Uint64
	hostToolCallApprovalReqTotal atomic.Uint64
	hostToolCallErrorTotal       atomic.Uint64
	enrichFailuresTotal          atomic.Uint64
)

// IncArtifactApprovalMiss LookupArtifact 对已建 artifact key 查询返回未审批时 +1。
func IncArtifactApprovalMiss() { artifactApprovalMissTotal.Add(1) }

// IncSkillArtifactApprovalMiss 是 module/skill/approval.go 使用的显式别名。
// 保留别名可让调用点表达业务含义，而不暴露底层 artifact 指标命名。
func IncSkillArtifactApprovalMiss() { IncArtifactApprovalMiss() }

// ArtifactApprovalMiss 返回 artifact key 未审批命中的累计数。
func ArtifactApprovalMiss() uint64 { return artifactApprovalMissTotal.Load() }

// IncTrimCorruptionFallback pair-fenced trim 找不到成对 footer 回落 legacy 时 +1。
func IncTrimCorruptionFallback() { trimCorruptionFallbackCount.Add(1) }

// TrimCorruptionFallback 返回 trim 损坏回退累计数。
func TrimCorruptionFallback() uint64 { return trimCorruptionFallbackCount.Load() }

const (
	// HostToolOutcome* 是 IncHostToolCallOutcome 的合法 outcome 字面量。
	HostToolOutcomeOK               = "ok"
	HostToolOutcomeCWDMissing       = "cwd_missing"
	HostToolOutcomeApprovalRequired = "approval_required"
	HostToolOutcomeError            = "error"
)

// IncHostToolCallOutcome 记录一次 host-direct tool 调用的最终 outcome。未知
// outcome 保守计为 error。
func IncHostToolCallOutcome(outcome string) {
	switch outcome {
	case HostToolOutcomeOK:
		hostToolCallOKTotal.Add(1)
	case HostToolOutcomeCWDMissing:
		hostToolCallCWDMissingTotal.Add(1)
	case HostToolOutcomeApprovalRequired:
		hostToolCallApprovalReqTotal.Add(1)
	default:
		hostToolCallErrorTotal.Add(1)
	}
}

// HostToolCallOK 返回 host-direct tool 调用成功累计数。
func HostToolCallOK() uint64 { return hostToolCallOKTotal.Load() }

// HostToolCallCWDMissing 返回 host-direct tool 缺少 cwd 的累计数。
func HostToolCallCWDMissing() uint64 { return hostToolCallCWDMissingTotal.Load() }

// HostToolCallApprovalRequired 返回 host-direct tool 需要审批的累计数。
func HostToolCallApprovalRequired() uint64 { return hostToolCallApprovalReqTotal.Load() }

// HostToolCallError 返回 host-direct tool 错误累计数。
func HostToolCallError() uint64 { return hostToolCallErrorTotal.Load() }

// IncEnrichFailure 记录 codexapp tool-call params enrich fail-soft 失败一次。
// 导出层目标名：enrich_failures_total。
func IncEnrichFailure() { enrichFailuresTotal.Add(1) }

// EnrichFailures 返回 codexapp tool-call 参数 enrich 失败累计数。
func EnrichFailures() uint64 { return enrichFailuresTotal.Load() }

// Snapshot 是 skill 指标的一次读取快照。
// 快照非原子，并发自增可能出现在下一次读取中。
type Snapshot struct {
	ArtifactApprovalMissTotal    uint64 // artifact key 未审批命中次数。
	TrimCorruptionFallbackCount  uint64 // trim 损坏回退次数。
	HostToolCallOKTotal          uint64 // host-direct tool 成功次数。
	HostToolCallCWDMissingTotal  uint64 // host-direct tool 缺少 cwd 次数。
	HostToolCallApprovalReqTotal uint64 // host-direct tool 需要审批次数。
	HostToolCallErrorTotal       uint64 // host-direct tool 错误次数。
	EnrichFailuresTotal          uint64 // codexapp enrich 失败次数。
}

// Read 返回当前 skill 指标快照。
func Read() Snapshot {
	return Snapshot{
		ArtifactApprovalMissTotal:    artifactApprovalMissTotal.Load(),
		TrimCorruptionFallbackCount:  trimCorruptionFallbackCount.Load(),
		HostToolCallOKTotal:          hostToolCallOKTotal.Load(),
		HostToolCallCWDMissingTotal:  hostToolCallCWDMissingTotal.Load(),
		HostToolCallApprovalReqTotal: hostToolCallApprovalReqTotal.Load(),
		HostToolCallErrorTotal:       hostToolCallErrorTotal.Load(),
		EnrichFailuresTotal:          enrichFailuresTotal.Load(),
	}
}

// ResetForTesting 仅测试用：全部 counter 归零。
func ResetForTesting() {
	artifactApprovalMissTotal.Store(0)
	trimCorruptionFallbackCount.Store(0)
	hostToolCallOKTotal.Store(0)
	hostToolCallCWDMissingTotal.Store(0)
	hostToolCallApprovalReqTotal.Store(0)
	hostToolCallErrorTotal.Store(0)
	enrichFailuresTotal.Store(0)
}
