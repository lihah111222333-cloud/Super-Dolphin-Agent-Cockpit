package prompt

import (
	"context"
	"fmt"
	"math"
)

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

type TokenBudgetProvider struct{}

type TokenBudgetBackstop interface {
	EvaluateTokenBudget(tracker *TokenBudgetTracker, budget, totalTokens int, childAgent bool) TokenBudgetDecision
}

type DefaultTokenBudgetBackstop struct{}

type TokenBudgetTracker struct {
	ContinuationCount int
	LowYieldStreak    int
	LastTotalTokens   int
}

type TokenBudgetDecision struct {
	Continue           bool
	ReachedTarget      bool
	DiminishingReturns bool
	ContinuationCount  int
	MinimumTarget      int
	Nudge              string
}

// SectionName 处理section名称。
func (TokenBudgetProvider) SectionName() string {
	return DynamicSectionTokenBudget
}

// Resolve 解析prompt。
func (TokenBudgetProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	if !tokenBudgetEnabled(input.BuildCtx) {
		return nil, nil
	}
	text := tokenBudgetSectionText
	return &text, nil
}

// EvaluateTokenBudget 处理evaluate令牌budget。
func (DefaultTokenBudgetBackstop) EvaluateTokenBudget(tracker *TokenBudgetTracker, budget, totalTokens int, childAgent bool) TokenBudgetDecision {
	return evaluateTokenBudgetBackstop(tracker, budget, totalTokens, childAgent)
}

// evaluateTokenBudgetBackstop 处理evaluate令牌budgetbackstop。
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

func tokenBudgetEnabled(build BuildCtx) bool {
	return promptFeatureEnabled(build.SessionFlags, []string{"TOKEN_BUDGET"}, "token_budget", "tokenBudget")
}

func minimumTokenBudgetTarget(budget int) int {
	return int(math.Ceil(float64(budget) * tokenBudgetCompletionThreshold))
}

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

func buildTokenBudgetContinuationNudge(totalTokens, budget, minimumTarget int) string {
	pct := 0
	if budget > 0 {
		pct = int(math.Round((float64(totalTokens) / float64(budget)) * 100))
	}
	return fmt.Sprintf("Continue working toward the token budget. Progress: %d/%d tokens (~%d%%, soft floor %d).", totalTokens, budget, pct, minimumTarget)
}
