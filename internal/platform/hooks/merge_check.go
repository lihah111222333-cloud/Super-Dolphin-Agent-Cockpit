package hooks

import (
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

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
	normalized := make([]mcp.CheckDecision, 0, len(decisions))
	failed := make([]mcp.LeaseKey, 0)
	lost := make([]mcp.LeaseKey, 0)
	failedSeen := make(map[mcp.LeaseKey]struct{}, len(decisions))
	lostSeen := make(map[mcp.LeaseKey]struct{}, len(decisions))
	for _, item := range decisions {
		if item.Err != nil {
			failed = appendUniqueLease(failed, failedSeen, item.Lease)
			if item.ConsecutiveFailures >= 3 {
				lost = appendUniqueLease(lost, lostSeen, item.Lease)
			}
			continue
		}
		normalized = append(normalized, mcp.CheckDecision{
			Decision: normalizeCheckDecision(item.Decision.Decision),
			Severity: strings.TrimSpace(item.Decision.Severity),
			Reason:   strings.TrimSpace(item.Decision.Reason),
		})
	}
	return normalized, failed, lost
}

func mergeCheckDecision(decisions []mcp.CheckDecision) mcp.CheckDecision {
	if len(decisions) == 0 {
		return mcp.CheckDecision{Decision: mcp.HookDecisionContinue}
	}
	final := highestCheckDecision(decisions)
	return mcp.CheckDecision{
		Decision: final,
		Severity: firstCheckSeverity(decisions, final),
		Reason:   firstCheckReason(decisions, final),
	}
}

func highestCheckDecision(decisions []mcp.CheckDecision) string {
	best := mcp.HookDecisionContinue
	bestRank := checkRank(best)
	for _, item := range decisions {
		if rank := checkRank(item.Decision); rank > bestRank {
			best = item.Decision
			bestRank = rank
		}
	}
	return best
}

func firstCheckSeverity(decisions []mcp.CheckDecision, want string) string {
	for _, item := range decisions {
		if item.Decision == want && item.Severity != "" {
			return item.Severity
		}
	}
	return ""
}

func firstCheckReason(decisions []mcp.CheckDecision, want string) string {
	for _, item := range decisions {
		if item.Decision == want && item.Reason != "" {
			return item.Reason
		}
	}
	return ""
}

func normalizeCheckDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case mcp.HookDecisionContinue, mcp.HookDecisionWarn, mcp.HookDecisionAbort:
		return strings.ToLower(strings.TrimSpace(decision))
	default:
		return mcp.HookDecisionContinue
	}
}

func checkRank(decision string) int {
	switch decision {
	case mcp.HookDecisionAbort:
		return 2
	case mcp.HookDecisionWarn:
		return 1
	default:
		return 0
	}
}
