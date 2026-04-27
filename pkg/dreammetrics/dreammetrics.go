// Package dreammetrics provides atomic counters for DreamExecutor observability.
//
// 独立出 leaf 包的原因：
//   - 计数点集中在 internal/provider/unified/dream_executor.go dispatcher 层；
//   - 未来 claudecli/codexapp 真实现 provider 也可能直接埋点；
//   - 这些位置 import 方向各异，提升到 leaf pkg/dreammetrics 供上下层共用。
//
// 当前无 Prometheus 集成，这些 atomic counter 通过 Read() Snapshot 返回供未来
// exporter 读取；接入 Prometheus 时只改 exporter，调用点保持不变。
//
// 仿 pkg/skillmetrics 的 in-process counter 模式 (P20.1 同源)。
package dreammetrics

import "sync/atomic"

var (
	successTotal             atomic.Uint64
	providerSkippedTotal     atomic.Uint64
	providerFailedTotal      atomic.Uint64
	allNotConfiguredTotal    atomic.Uint64
	promptOversizeTotal      atomic.Uint64
)

// IncSuccess 单次 dream 蒸馏成功（dispatcher 命中某 provider 返回非 nil 结果）+1。
func IncSuccess() { successTotal.Add(1) }

// Success 读当前值。
func Success() uint64 { return successTotal.Load() }

// IncProviderSkipped 单个 provider 返回 ErrDreamExecutorNotConfigured 跳过（dispatcher 继续 failover）+1。
// failover 链路若多个 provider 都跳过，会累加多次。
func IncProviderSkipped() { providerSkippedTotal.Add(1) }

// ProviderSkipped 读当前值。
func ProviderSkipped() uint64 { return providerSkippedTotal.Load() }

// IncProviderFailed 单个 provider 返回非 NotConfigured 真错误（dispatcher 立即短路）+1。
func IncProviderFailed() { providerFailedTotal.Add(1) }

// ProviderFailed 读当前值。
func ProviderFailed() uint64 { return providerFailedTotal.Load() }

// IncAllNotConfigured failover 链路全部 provider 返回 NotConfigured，整轮 dream 失败 +1。
func IncAllNotConfigured() { allNotConfiguredTotal.Add(1) }

// AllNotConfigured 读当前值。
func AllNotConfigured() uint64 { return allNotConfiguredTotal.Load() }

// IncPromptOversize prompt 长度超过 dispatcher size cap 被 fail-fast 拒绝 +1。
func IncPromptOversize() { promptOversizeTotal.Add(1) }

// PromptOversize 读当前值。
func PromptOversize() uint64 { return promptOversizeTotal.Load() }

// Snapshot 一次性读 5 个 counter 的快照，顺序稳定，仅用于诊断 / 测试。
// 快照非原子——期间可能有并发自增，这是可接受的。
type Snapshot struct {
	SuccessTotal           uint64
	ProviderSkippedTotal   uint64
	ProviderFailedTotal    uint64
	AllNotConfiguredTotal  uint64
	PromptOversizeTotal    uint64
}

// Read 读当前 snapshot。
func Read() Snapshot {
	return Snapshot{
		SuccessTotal:          successTotal.Load(),
		ProviderSkippedTotal:  providerSkippedTotal.Load(),
		ProviderFailedTotal:   providerFailedTotal.Load(),
		AllNotConfiguredTotal: allNotConfiguredTotal.Load(),
		PromptOversizeTotal:   promptOversizeTotal.Load(),
	}
}

// ResetForTesting 仅测试用：全部 counter 归零。
func ResetForTesting() {
	successTotal.Store(0)
	providerSkippedTotal.Store(0)
	providerFailedTotal.Store(0)
	allNotConfiguredTotal.Store(0)
	promptOversizeTotal.Store(0)
}
