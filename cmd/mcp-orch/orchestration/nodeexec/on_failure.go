package nodeexec

// 本文件集中处理 FailureClass 到 OnFailureStrategy 的查表规则。
// dispatcher 拿到 NodeOutcome.FailureClass 后调用这里决定 retry、升级模型、跳过或 fail-fast。

// ResolveOnFailureStrategy 根据 FailureClass 查 OnFailureConfig.ByClass，
// 未命中走 Default，Default 也未配置走 OnFailureRetry（保守兜底）。
//
// 调用方契约：
//   - cfg=nil（节点未配 on_failure）→ 默认 retry
//   - class=""（未分类失败）→ 走 Default 或 retry 兜底
//   - 未知 class（schema 之外）→ 走 Default 或 retry 兜底
//   - 命中 ByClass → 返回该策略
func ResolveOnFailureStrategy(cfg *OnFailureConfig, class FailureClass) OnFailureStrategy {
	if cfg == nil {
		return OnFailureRetry
	}
	if class != "" {
		if strategy, ok := cfg.ByClass[class]; ok && strategy != "" {
			return strategy
		}
	}
	if cfg.Default != "" {
		return cfg.Default
	}
	return OnFailureRetry
}

// MaxAttemptsFor 返回节点的总尝试次数上限（含首发）。
// nil 或 cfg.MaxAttempts<=0 → 1（只跑一次即终态）。
func MaxAttemptsFor(cfg *OnFailureConfig) int {
	if cfg == nil || cfg.MaxAttempts <= 0 {
		return 1
	}
	return cfg.MaxAttempts
}

// EscalationModelFor 返回 escalate_model 策略下"下一个尝试用的 model"。
// 给定 currentModel，从 EscalationChain 找下一档；找到了返回 (next, true)。
// 找不到 / chain 已穷尽 / cfg=nil → 返回 ("", false)，调用方退化到其他策略。
func EscalationModelFor(cfg *OnFailureConfig, currentModel string) (string, bool) {
	if cfg == nil || len(cfg.EscalationChain) == 0 {
		return "", false
	}
	// 找到当前 model 在 chain 中的位置，返回下一个
	for i, m := range cfg.EscalationChain {
		if m == currentModel && i+1 < len(cfg.EscalationChain) {
			return cfg.EscalationChain[i+1], true
		}
	}
	// 当前 model 不在 chain 里 → 返回链首（让 escalate 至少有一档可走）
	if currentModel == "" || !inChain(cfg.EscalationChain, currentModel) {
		return cfg.EscalationChain[0], true
	}
	return "", false
}

func inChain(chain []string, model string) bool {
	for _, m := range chain {
		if m == model {
			return true
		}
	}
	return false
}
