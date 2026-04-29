// Package skillmetrics provides atomic counters for P20.1 skill observability.
//
// 独立出 leaf 包的原因：
//   - 计数点分散在 dto/provider（SkillMode.Effective）、internal/module/skill
//     （ApprovalCache / ExpandBody / ReadResource）、internal/module/prompt
//     （SkillCatalogProvider 的 Redacted 计数）以及 rollout_markers；
//   - 这些位置不可以反向 import internal/module/skill（dto 层会成环）；
//   - 因此把纯观测性 counter 提升到 leaf pkg/skillmetrics 供上下两层共用。
//
// 当前无 Prometheus 集成，这些 atomic counter 通过 Snapshot() 返回供未来
// exporter 读取；接入 Prometheus 时只改 exporter，调用点保持不变。
package skillmetrics

import "sync/atomic"

var (
	skillInvalidModeTotal           atomic.Uint64
	untrustedManifestRedactionTotal atomic.Uint64
	trimCorruptionFallbackCount     atomic.Uint64
	artifactApprovalMissTotal       atomic.Uint64
	skillExpandInvokeRate           atomic.Uint64
	skillMCPToolCallTotal           atomic.Uint64
	skillMCPToolSuccessTotal        atomic.Uint64
	skillMCPToolErrorTotal          atomic.Uint64
	skillMCPApprovalRequiredTotal   atomic.Uint64
	hostToolCallOKTotal             atomic.Uint64
	hostToolCallCWDMissingTotal     atomic.Uint64
	hostToolCallApprovalReqTotal    atomic.Uint64
	hostToolCallErrorTotal          atomic.Uint64
	enrichFailuresTotal             atomic.Uint64
)

// IncSkillInvalidMode dto.SkillMode.Effective() 遇到未知值降级到 None 时 +1。
func IncSkillInvalidMode() { skillInvalidModeTotal.Add(1) }

// SkillInvalidMode 读当前值。
func SkillInvalidMode() uint64 { return skillInvalidModeTotal.Load() }

// IncUntrustedManifestRedaction SkillCatalogProvider 为 Redacted 分组每新增一条 +1。
func IncUntrustedManifestRedaction() { untrustedManifestRedactionTotal.Add(1) }

// UntrustedManifestRedaction 读当前值。
func UntrustedManifestRedaction() uint64 { return untrustedManifestRedactionTotal.Load() }

// IncTrimCorruptionFallback pair-fenced trim 找不到成对 footer 回落 legacy 时 +1。
func IncTrimCorruptionFallback() { trimCorruptionFallbackCount.Add(1) }

// TrimCorruptionFallback 读当前值。
func TrimCorruptionFallback() uint64 { return trimCorruptionFallbackCount.Load() }

// IncArtifactApprovalMiss LookupArtifact 对已建 artifact key 查询返回未审批时 +1。
func IncArtifactApprovalMiss() { artifactApprovalMissTotal.Add(1) }

// IncSkillArtifactApprovalMiss is the explicit B-1 counter name for artifact
// approval misses. Kept as an alias so older callers and metrics remain stable.
func IncSkillArtifactApprovalMiss() { IncArtifactApprovalMiss() }

// ArtifactApprovalMiss 读当前值。
func ArtifactApprovalMiss() uint64 { return artifactApprovalMissTotal.Load() }

// IncSkillExpandInvoke ExpandBody / ReadResource 被调一次 +1（合并计数）。
// Rate 语义由上层从 Prometheus rate() 派生；这里只是原始 counter。
func IncSkillExpandInvoke() { skillExpandInvokeRate.Add(1) }

// SkillExpandInvoke 读当前值。
func SkillExpandInvoke() uint64 { return skillExpandInvokeRate.Load() }

// IncSkillMCPToolCall same-binary skill MCP child 收到一次 tools/call 时 +1。
func IncSkillMCPToolCall() { skillMCPToolCallTotal.Add(1) }

// SkillMCPToolCall 读当前值。
func SkillMCPToolCall() uint64 { return skillMCPToolCallTotal.Load() }

// IncSkillMCPToolSuccess same-binary skill MCP child 成功返回 host RPC result 时 +1。
func IncSkillMCPToolSuccess() { skillMCPToolSuccessTotal.Add(1) }

// SkillMCPToolSuccess 读当前值。
func SkillMCPToolSuccess() uint64 { return skillMCPToolSuccessTotal.Load() }

// IncSkillMCPToolError same-binary skill MCP child 遇到非 approval_required 错误时 +1。
func IncSkillMCPToolError() { skillMCPToolErrorTotal.Add(1) }

// SkillMCPToolError 读当前值。
func SkillMCPToolError() uint64 { return skillMCPToolErrorTotal.Load() }

// IncSkillMCPApprovalRequired same-binary skill MCP child 收到 approval_required envelope 时 +1。
func IncSkillMCPApprovalRequired() { skillMCPApprovalRequiredTotal.Add(1) }

// SkillMCPApprovalRequired 读当前值。
func SkillMCPApprovalRequired() uint64 { return skillMCPApprovalRequiredTotal.Load() }

const (
	HostToolOutcomeOK               = "ok"
	HostToolOutcomeCWDMissing       = "cwd_missing"
	HostToolOutcomeApprovalRequired = "approval_required"
	HostToolOutcomeError            = "error"
)

// IncHostToolCallOutcome 记录 codexapp host-direct skill tool 调用结果。
// Go 内部用固定 counter，导出层可映射为 host_tool_calls_total{outcome=...}。
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

func HostToolCallOK() uint64 { return hostToolCallOKTotal.Load() }

func HostToolCallCWDMissing() uint64 { return hostToolCallCWDMissingTotal.Load() }

func HostToolCallApprovalRequired() uint64 { return hostToolCallApprovalReqTotal.Load() }

func HostToolCallError() uint64 { return hostToolCallErrorTotal.Load() }

// IncEnrichFailure 记录 codexapp tool-call params enrich fail-soft 失败一次。
// 导出层目标名：enrich_failures_total。
func IncEnrichFailure() { enrichFailuresTotal.Add(1) }

func EnrichFailures() uint64 { return enrichFailuresTotal.Load() }

// Snapshot 一次性读 counter 快照，顺序稳定，仅用于诊断 / 测试。
// 快照非原子——期间可能有并发自增，这是可接受的。
type Snapshot struct {
	SkillInvalidModeTotal           uint64
	UntrustedManifestRedactionTotal uint64
	TrimCorruptionFallbackCount     uint64
	ArtifactApprovalMissTotal       uint64
	SkillExpandInvokeRate           uint64
	SkillMCPToolCallTotal           uint64
	SkillMCPToolSuccessTotal        uint64
	SkillMCPToolErrorTotal          uint64
	SkillMCPApprovalRequiredTotal   uint64
	HostToolCallOKTotal             uint64
	HostToolCallCWDMissingTotal     uint64
	HostToolCallApprovalReqTotal    uint64
	HostToolCallErrorTotal          uint64
	EnrichFailuresTotal             uint64
}

// Read 读当前 snapshot。
func Read() Snapshot {
	return Snapshot{
		SkillInvalidModeTotal:           skillInvalidModeTotal.Load(),
		UntrustedManifestRedactionTotal: untrustedManifestRedactionTotal.Load(),
		TrimCorruptionFallbackCount:     trimCorruptionFallbackCount.Load(),
		ArtifactApprovalMissTotal:       artifactApprovalMissTotal.Load(),
		SkillExpandInvokeRate:           skillExpandInvokeRate.Load(),
		SkillMCPToolCallTotal:           skillMCPToolCallTotal.Load(),
		SkillMCPToolSuccessTotal:        skillMCPToolSuccessTotal.Load(),
		SkillMCPToolErrorTotal:          skillMCPToolErrorTotal.Load(),
		SkillMCPApprovalRequiredTotal:   skillMCPApprovalRequiredTotal.Load(),
		HostToolCallOKTotal:             hostToolCallOKTotal.Load(),
		HostToolCallCWDMissingTotal:     hostToolCallCWDMissingTotal.Load(),
		HostToolCallApprovalReqTotal:    hostToolCallApprovalReqTotal.Load(),
		HostToolCallErrorTotal:          hostToolCallErrorTotal.Load(),
		EnrichFailuresTotal:             enrichFailuresTotal.Load(),
	}
}

// ResetForTesting 仅测试用：全部 counter 归零。
func ResetForTesting() {
	skillInvalidModeTotal.Store(0)
	untrustedManifestRedactionTotal.Store(0)
	trimCorruptionFallbackCount.Store(0)
	artifactApprovalMissTotal.Store(0)
	skillExpandInvokeRate.Store(0)
	skillMCPToolCallTotal.Store(0)
	skillMCPToolSuccessTotal.Store(0)
	skillMCPToolErrorTotal.Store(0)
	skillMCPApprovalRequiredTotal.Store(0)
	hostToolCallOKTotal.Store(0)
	hostToolCallCWDMissingTotal.Store(0)
	hostToolCallApprovalReqTotal.Store(0)
	hostToolCallErrorTotal.Store(0)
	enrichFailuresTotal.Store(0)
}
