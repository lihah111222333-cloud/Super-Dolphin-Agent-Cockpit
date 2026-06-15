package hooks

import (
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// MergeDuring 合并during。
func MergeDuring(decisions []peerDecision[mcp.CheckDecision]) MergeResult[mcp.CheckDecision] {
	normalized, failed, lost := normalizeCheckDecisions(decisions)
	return MergeResult[mcp.CheckDecision]{
		Decision:       mergeCheckDecision(normalized),
		PartialFailure: len(failed) > 0,
		FailedLeases:   failed,
		LostLeases:     lost,
	}
}

func normalizeCheckDecisions(decisions []peerDecision[mcp.CheckDecision]) ([]mcp.CheckDecision, []mcp.LeaseKey, []mcp.LeaseKey) {
	return normalizePeerDecisions(decisions, func(decision mcp.CheckDecision) mcp.CheckDecision {
		return mcp.CheckDecision{
			Decision: normalizeDecision(decision.Decision, checkDecisionConfig),
			Severity: strings.TrimSpace(decision.Severity),
			Reason:   strings.TrimSpace(decision.Reason),
		}
	})
}

// mergeCheckDecision 合并checkdecision。
func mergeCheckDecision(decisions []mcp.CheckDecision) mcp.CheckDecision {
	if len(decisions) == 0 {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}
	}
	final := highestCheckDecision(decisions)
	merged := mcp.CheckDecision{Decision: final}
	if item, ok := firstMatching(decisions, func(item mcp.CheckDecision) bool {
		return item.Decision == final && item.Severity != ""
	}); ok {
		merged.Severity = item.Severity
	}
	if item, ok := firstMatching(decisions, func(item mcp.CheckDecision) bool {
		return item.Decision == final && item.Reason != ""
	}); ok {
		merged.Reason = item.Reason
	}
	return merged
}

func highestCheckDecision(decisions []mcp.CheckDecision) string {
	return chooseDecision(decisions, func(item mcp.CheckDecision) string {
		return item.Decision
	}, checkDecisionConfig)
}
