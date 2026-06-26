// Package dreammetrics 提供 DreamExecutor 的进程内观测计数器。
// 计数点分布在 provider dispatcher 和适配层，放在 leaf 包可让上下层共享而不引入反向依赖。
package dreammetrics

import "sync/atomic"

var (
	successTotal          atomic.Uint64
	providerSkippedTotal  atomic.Uint64
	providerFailedTotal   atomic.Uint64
	allNotConfiguredTotal atomic.Uint64
	promptOversizeTotal   atomic.Uint64

	// token 计数由 provider 解析出 usage 后通过 AddTokens 上报。
	// input 已包含 cacheCreation；cacheRead 单列用于观察缓存命中带来的成本和时延差异。
	tokensInputTotal     atomic.Uint64
	tokensOutputTotal    atomic.Uint64
	tokensCacheReadTotal atomic.Uint64
)

// IncSuccess 记录一次 dream 蒸馏成功。
func IncSuccess() { successTotal.Add(1) }

// Success 返回 dream 蒸馏成功累计数。
func Success() uint64 { return successTotal.Load() }

// IncProviderSkipped 单个 provider 返回 ErrDreamExecutorNotConfigured 跳过（dispatcher 继续 failover）+1。
// failover 链路若多个 provider 都跳过，会累加多次。
func IncProviderSkipped() { providerSkippedTotal.Add(1) }

// ProviderSkipped 返回 provider 未配置跳过累计数。
func ProviderSkipped() uint64 { return providerSkippedTotal.Load() }

// IncProviderFailed 单个 provider 返回非 NotConfigured 真错误（dispatcher 立即短路）+1。
func IncProviderFailed() { providerFailedTotal.Add(1) }

// ProviderFailed 返回 provider 真错误累计数。
func ProviderFailed() uint64 { return providerFailedTotal.Load() }

// IncAllNotConfigured failover 链路全部 provider 返回 NotConfigured，整轮 dream 失败 +1。
func IncAllNotConfigured() { allNotConfiguredTotal.Add(1) }

// AllNotConfigured 返回整条 failover 链均未配置的累计数。
func AllNotConfigured() uint64 { return allNotConfiguredTotal.Load() }

// IncPromptOversize prompt 长度超过 dispatcher size cap 被 fail-fast 拒绝 +1。
func IncPromptOversize() { promptOversizeTotal.Add(1) }

// PromptOversize 返回 prompt 过大被拒绝的累计数。
func PromptOversize() uint64 { return promptOversizeTotal.Load() }

// AddTokens 累加单次 dream 成功的 token usage。
// 聚合 API（而非分 3 个 Inc）：input/output/cacheRead 是一次 LLM 调用同时产出，
// 分开上报容易在 provider 解析路径上遗漏对齐。0 值参数隐式 no-op。
func AddTokens(input, output, cacheRead uint64) {
	tokensInputTotal.Add(input)
	tokensOutputTotal.Add(output)
	tokensCacheReadTotal.Add(cacheRead)
}

// TokensInput 返回输入 token 累计数。
func TokensInput() uint64 { return tokensInputTotal.Load() }

// TokensOutput 返回输出 token 累计数。
func TokensOutput() uint64 { return tokensOutputTotal.Load() }

// TokensCacheRead 返回 cache read token 累计数。
func TokensCacheRead() uint64 { return tokensCacheReadTotal.Load() }

// Snapshot 是 DreamExecutor 指标的一次读取快照。
// 快照非原子，并发自增可能出现在下一次读取中。
type Snapshot struct {
	SuccessTotal          uint64 // dream 蒸馏成功次数。
	ProviderSkippedTotal  uint64 // provider 未配置而跳过的次数。
	ProviderFailedTotal   uint64 // provider 返回真错误的次数。
	AllNotConfiguredTotal uint64 // failover 链全部未配置的次数。
	PromptOversizeTotal   uint64 // prompt 超过大小限制被拒绝的次数。
	TokensInputTotal      uint64 // 输入 token 累计值。
	TokensOutputTotal     uint64 // 输出 token 累计值。
	TokensCacheReadTotal  uint64 // cache read token 累计值。
}

// Read 返回当前 DreamExecutor 指标快照。
func Read() Snapshot {
	return Snapshot{
		SuccessTotal:          successTotal.Load(),
		ProviderSkippedTotal:  providerSkippedTotal.Load(),
		ProviderFailedTotal:   providerFailedTotal.Load(),
		AllNotConfiguredTotal: allNotConfiguredTotal.Load(),
		PromptOversizeTotal:   promptOversizeTotal.Load(),
		TokensInputTotal:      tokensInputTotal.Load(),
		TokensOutputTotal:     tokensOutputTotal.Load(),
		TokensCacheReadTotal:  tokensCacheReadTotal.Load(),
	}
}

// ResetForTesting 仅测试用：全部 counter 归零。
func ResetForTesting() {
	successTotal.Store(0)
	providerSkippedTotal.Store(0)
	providerFailedTotal.Store(0)
	allNotConfiguredTotal.Store(0)
	promptOversizeTotal.Store(0)
	tokensInputTotal.Store(0)
	tokensOutputTotal.Store(0)
	tokensCacheReadTotal.Store(0)
}
