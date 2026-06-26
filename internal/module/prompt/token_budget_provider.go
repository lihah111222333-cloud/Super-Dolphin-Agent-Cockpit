package prompt

import (
	"context"
	"fmt"
	"math"
)

// tokenBudgetSectionText 告诉主代理把用户 token 目标当作最低工作预算。
const tokenBudgetSectionText = `Token budget:
- When the user sets a token target, treat it as a minimum work budget rather than a suggestion.
- Keep working until you approach the target with useful progress.
- If runtime tracking says you are still below roughly 90% of the target, expect a continuation nudge.
- Stop early only when repeated continuation turns are adding very little new value.`

const (
	tokenBudgetCompletionThreshold = 0.9
	tokenBudgetLowYieldThreshold   = 500
	tokenBudgetLowYieldLimit       = 3
)

var _ DynamicSectionProvider = TokenBudgetProvider{}

// TokenBudgetProvider 在 token budget feature gate 开启时注入预算执行提示。
type TokenBudgetProvider struct{}

// TokenBudgetBackstop 评估当前消耗是否需要继续自动推进。
type TokenBudgetBackstop interface {
	EvaluateTokenBudget(tracker *TokenBudgetTracker, budget, totalTokens int, childAgent bool) TokenBudgetDecision
}

// DefaultTokenBudgetBackstop 是生产默认的 token budget 评估器。
type DefaultTokenBudgetBackstop struct{}

// TokenBudgetTracker 记录连续推进次数和最近 token 增量，用于识别低收益续跑。
type TokenBudgetTracker struct {
	ContinuationCount int
	LowYieldStreak    int
	LastTotalTokens   int
}

// TokenBudgetDecision 描述一次预算评估的结果和需要发给代理的续跑提示。
type TokenBudgetDecision struct {
	Continue           bool
	ReachedTarget      bool
	DiminishingReturns bool
	ContinuationCount  int
	MinimumTarget      int
	Nudge              string
}

// SectionName 返回 token budget 动态 section 的注册名。
func (TokenBudgetProvider) SectionName() string {
	return DynamicSectionTokenBudget
}

// Resolve 在 feature gate 开启时返回预算提示，未开启时不注入额外指令。
func (TokenBudgetProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	if !tokenBudgetEnabled(input.BuildCtx) {
		return nil, nil
	}
	text := tokenBudgetSectionText
	return &text, nil
}

// EvaluateTokenBudget 委托默认评估逻辑，保留接口以便测试或后续替换策略。
func (DefaultTokenBudgetBackstop) EvaluateTokenBudget(tracker *TokenBudgetTracker, budget, totalTokens int, childAgent bool) TokenBudgetDecision {
	return evaluateTokenBudgetBackstop(tracker, budget, totalTokens, childAgent)
}

// evaluateTokenBudgetBackstop 判断主代理是否还没达到软目标且仍有有效增量。
// 子代理和无预算请求直接跳过，避免递归 continuation 失控。
func evaluateTokenBudgetBackstop(tracker *TokenBudgetTracker, budget, totalTokens int, childAgent bool) TokenBudgetDecision {
	if tracker == nil || childAgent || budget <= 0 {
		return TokenBudgetDecision{}
	}
	minimumTarget := minimumTokenBudgetTarget(budget)
	updateTokenBudgetTracker(tracker, totalTokens)
	decision := TokenBudgetDecision{
		ContinuationCount: tracker.ContinuationCount,
		MinimumTarget:     minimumTarget,
	}
	if totalTokens < minimumTarget && tracker.LowYieldStreak < tokenBudgetLowYieldLimit {
		tracker.ContinuationCount++
		decision.Continue = true
		decision.ContinuationCount = tracker.ContinuationCount
		decision.Nudge = buildTokenBudgetContinuationNudge(totalTokens, budget, minimumTarget)
		return decision
	}
	decision.ReachedTarget = totalTokens >= minimumTarget
	decision.DiminishingReturns = tracker.LowYieldStreak >= tokenBudgetLowYieldLimit && totalTokens < minimumTarget
	return decision
}

// tokenBudgetEnabled 兼容 session flag 和环境变量两种 feature gate。
func tokenBudgetEnabled(build BuildCtx) bool {
	return promptFeatureEnabled(build.SessionFlags, []string{"TOKEN_BUDGET"}, "token_budget", "tokenBudget")
}

// minimumTokenBudgetTarget 把用户预算转换成软完成线，默认需要达到 90%。
func minimumTokenBudgetTarget(budget int) int {
	return int(math.Ceil(float64(budget) * tokenBudgetCompletionThreshold))
}

// updateTokenBudgetTracker 更新累计 token 和低收益连续次数。
func updateTokenBudgetTracker(tracker *TokenBudgetTracker, totalTokens int) {
	delta := max(totalTokens-tracker.LastTotalTokens, 0)
	if tracker.LastTotalTokens > 0 {
		if delta < tokenBudgetLowYieldThreshold {
			tracker.LowYieldStreak++
		} else {
			tracker.LowYieldStreak = 0
		}
	}
	tracker.LastTotalTokens = totalTokens
}

// buildTokenBudgetContinuationNudge 生成给 continuation 回合的简短进度提示。
func buildTokenBudgetContinuationNudge(totalTokens, budget, minimumTarget int) string {
	pct := 0
	if budget > 0 {
		pct = int(math.Round((float64(totalTokens) / float64(budget)) * 100))
	}
	return fmt.Sprintf("Continue working toward the token budget. Progress: %d/%d tokens (~%d%%, soft floor %d).", totalTokens, budget, pct, minimumTarget)
}
