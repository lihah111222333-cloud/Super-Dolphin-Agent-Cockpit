// Package skillmetrics provides atomic counters for skill observability.
//
// 独立出 leaf 包的原因：计数点分散在 internal/module/skill (rollout_markers
// trim 的 corruption fallback)、internal/platform/toolbridge (host-direct
// tool 调用 outcome) 与 internal/provider/codexapp (enrich 失败) 三处，
// 它们没有共同上游可放置纯观测计数；leaf pkg 让上下两层共用。
//
// 接入 Prometheus 由 internal/platform/metrics/skill.go 一侧 wrap CounterFunc。
//
// 历史背景（已删 counter）：先后删过若干 P20/P21 时期的 counter。这些 counter
// 对应的源头服务方法（ExpandBody / ReadResource / SkillMCP child / SkillCatalogProvider
// Redacted）已在 P3/P4 cutover 中删除（spec §11），counter 永远停在 0；P5f
// 同步清理避免误导仪表盘。剩下的活 counter 都有真生产消费方。
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

// IncSkillArtifactApprovalMiss 是 module/skill/approval.go 用的显式 alias
// （命名 self-documenting，未来 internal/module/skill 重命名包时无需改 caller）。
func IncSkillArtifactApprovalMiss() { IncArtifactApprovalMiss() }

// ArtifactApprovalMiss 读当前值。
func ArtifactApprovalMiss() uint64 { return artifactApprovalMissTotal.Load() }

// IncTrimCorruptionFallback pair-fenced trim 找不到成对 footer 回落 legacy 时 +1。
func IncTrimCorruptionFallback() { trimCorruptionFallbackCount.Add(1) }

// TrimCorruptionFallback 读当前值。
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

// HostToolCallOK 处理host工具callok。
func HostToolCallOK() uint64 { return hostToolCallOKTotal.Load() }

// HostToolCallCWDMissing 处理host工具call工作目录missing。
func HostToolCallCWDMissing() uint64 { return hostToolCallCWDMissingTotal.Load() }

// HostToolCallApprovalRequired 处理host工具call审批必需。
func HostToolCallApprovalRequired() uint64 { return hostToolCallApprovalReqTotal.Load() }

// HostToolCallError 处理host工具call错误。
func HostToolCallError() uint64 { return hostToolCallErrorTotal.Load() }

// IncEnrichFailure 记录 codexapp tool-call params enrich fail-soft 失败一次。
// 导出层目标名：enrich_failures_total。
func IncEnrichFailure() { enrichFailuresTotal.Add(1) }

// EnrichFailures 补充failures。
func EnrichFailures() uint64 { return enrichFailuresTotal.Load() }

// Snapshot 一次性读 counter 快照，顺序稳定，仅用于诊断 / 测试。
// 快照非原子——期间可能有并发自增，这是可接受的。
type Snapshot struct {
	ArtifactApprovalMissTotal    uint64
	TrimCorruptionFallbackCount  uint64
	HostToolCallOKTotal          uint64
	HostToolCallCWDMissingTotal  uint64
	HostToolCallApprovalReqTotal uint64
	HostToolCallErrorTotal       uint64
	EnrichFailuresTotal          uint64
}

// Read 读当前 snapshot。
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
