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

// ArtifactApprovalMiss 读当前值。
func ArtifactApprovalMiss() uint64 { return artifactApprovalMissTotal.Load() }

// IncSkillExpandInvoke ExpandBody / ReadResource 被调一次 +1（合并计数）。
// Rate 语义由上层从 Prometheus rate() 派生；这里只是原始 counter。
func IncSkillExpandInvoke() { skillExpandInvokeRate.Add(1) }

// SkillExpandInvoke 读当前值。
func SkillExpandInvoke() uint64 { return skillExpandInvokeRate.Load() }

// Snapshot 一次性读 5 个 counter 的快照，顺序稳定，仅用于诊断 / 测试。
// 快照非原子——期间可能有并发自增，这是可接受的。
type Snapshot struct {
	SkillInvalidModeTotal           uint64
	UntrustedManifestRedactionTotal uint64
	TrimCorruptionFallbackCount     uint64
	ArtifactApprovalMissTotal       uint64
	SkillExpandInvokeRate           uint64
}

// Read 读当前 snapshot。
func Read() Snapshot {
	return Snapshot{
		SkillInvalidModeTotal:           skillInvalidModeTotal.Load(),
		UntrustedManifestRedactionTotal: untrustedManifestRedactionTotal.Load(),
		TrimCorruptionFallbackCount:     trimCorruptionFallbackCount.Load(),
		ArtifactApprovalMissTotal:       artifactApprovalMissTotal.Load(),
		SkillExpandInvokeRate:           skillExpandInvokeRate.Load(),
	}
}

// ResetForTesting 仅测试用：全部 counter 归零。
func ResetForTesting() {
	skillInvalidModeTotal.Store(0)
	untrustedManifestRedactionTotal.Store(0)
	trimCorruptionFallbackCount.Store(0)
	artifactApprovalMissTotal.Store(0)
	skillExpandInvokeRate.Store(0)
}
