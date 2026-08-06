// Package dreammetrics 提供 DreamExecutor 的进程内观测计数器。
// 计数点分布在 provider dispatcher 和适配层，放在 leaf 包可让上下层共享而不引入反向依赖。
package dreammetrics

import "sync/atomic"

// Registry 保存 DreamExecutor 的八组可变观测指标状态。
// 每个运行时必须由显式 owner 创建并共享同一 Registry。
type Registry struct {
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
}

// NewRegistry 创建独立 DreamExecutor 指标 registry。
func NewRegistry() *Registry {
	return &Registry{}
}

// IncSuccess 记录一次 dream 蒸馏成功。
func (r *Registry) IncSuccess() { r.successTotal.Add(1) }

// Success 返回 dream 蒸馏成功累计数。
func (r *Registry) Success() uint64 { return r.successTotal.Load() }

// IncProviderSkipped 单个 provider 返回 ErrDreamExecutorNotConfigured 跳过（dispatcher 继续 failover）+1。
// failover 链路若多个 provider 都跳过，会累加多次。
func (r *Registry) IncProviderSkipped() { r.providerSkippedTotal.Add(1) }

// ProviderSkipped 返回 provider 未配置跳过累计数。
func (r *Registry) ProviderSkipped() uint64 { return r.providerSkippedTotal.Load() }

// IncProviderFailed 单个 provider 返回非 NotConfigured 真错误（dispatcher 立即短路）+1。
func (r *Registry) IncProviderFailed() { r.providerFailedTotal.Add(1) }

// ProviderFailed 返回 provider 真错误累计数。
func (r *Registry) ProviderFailed() uint64 { return r.providerFailedTotal.Load() }

// IncAllNotConfigured failover 链路全部 provider 返回 NotConfigured，整轮 dream 失败 +1。
func (r *Registry) IncAllNotConfigured() { r.allNotConfiguredTotal.Add(1) }

// AllNotConfigured 返回整条 failover 链均未配置的累计数。
func (r *Registry) AllNotConfigured() uint64 { return r.allNotConfiguredTotal.Load() }

// IncPromptOversize prompt 长度超过 dispatcher size cap 被 fail-fast 拒绝 +1。
func (r *Registry) IncPromptOversize() { r.promptOversizeTotal.Add(1) }

// PromptOversize 返回 prompt 过大被拒绝的累计数。
func (r *Registry) PromptOversize() uint64 { return r.promptOversizeTotal.Load() }

// AddTokens 累加单次 dream 成功的 token usage。
// 聚合 API（而非分 3 个 Inc）：input/output/cacheRead 是一次 LLM 调用同时产出，
// 分开上报容易在 provider 解析路径上遗漏对齐。0 值参数隐式 no-op。
func (r *Registry) AddTokens(input, output, cacheRead uint64) {
	r.tokensInputTotal.Add(input)
	r.tokensOutputTotal.Add(output)
	r.tokensCacheReadTotal.Add(cacheRead)
}

// TokensInput 返回输入 token 累计数。
func (r *Registry) TokensInput() uint64 { return r.tokensInputTotal.Load() }

// TokensOutput 返回输出 token 累计数。
func (r *Registry) TokensOutput() uint64 { return r.tokensOutputTotal.Load() }

// TokensCacheRead 返回 cache read token 累计数。
func (r *Registry) TokensCacheRead() uint64 { return r.tokensCacheReadTotal.Load() }

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
func (r *Registry) Read() Snapshot {
	return Snapshot{
		SuccessTotal:          r.successTotal.Load(),
		ProviderSkippedTotal:  r.providerSkippedTotal.Load(),
		ProviderFailedTotal:   r.providerFailedTotal.Load(),
		AllNotConfiguredTotal: r.allNotConfiguredTotal.Load(),
		PromptOversizeTotal:   r.promptOversizeTotal.Load(),
		TokensInputTotal:      r.tokensInputTotal.Load(),
		TokensOutputTotal:     r.tokensOutputTotal.Load(),
		TokensCacheReadTotal:  r.tokensCacheReadTotal.Load(),
	}
}

// ResetForTesting 仅测试用：全部 counter 归零。
func (r *Registry) ResetForTesting() {
	r.successTotal.Store(0)
	r.providerSkippedTotal.Store(0)
	r.providerFailedTotal.Store(0)
	r.allNotConfiguredTotal.Store(0)
	r.promptOversizeTotal.Store(0)
	r.tokensInputTotal.Store(0)
	r.tokensOutputTotal.Store(0)
	r.tokensCacheReadTotal.Store(0)
}
