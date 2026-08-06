package hooks

import (
	"strings"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
)

// MergeDuring 合并 check 阶段多个 peer 的决策。
// abort/warn/continue 按风险优先级收敛，失败 lease 会随结果返回给 Manager。
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
	config := checkDecisionConfig()
	return normalizePeerDecisions(decisions, func(decision mcp.CheckDecision) mcp.CheckDecision {
		return mcp.CheckDecision{
			Decision: normalizeDecision(decision.Decision, config),
			Severity: strings.TrimSpace(decision.Severity),
			Reason:   strings.TrimSpace(decision.Reason),
		}
	})
}

// mergeCheckDecision 选择 check 阶段最终决策，并保留同决策 peer 提供的 severity/reason。
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
	config := checkDecisionConfig()
	return chooseDecision(decisions, func(item mcp.CheckDecision) string {
		return item.Decision
	}, config)
}
