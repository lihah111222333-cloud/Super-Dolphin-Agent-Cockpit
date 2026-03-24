package hooks

import (
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

func MergeAfter(decisions []peerDecision[mcp.AfterDecision]) MergeResult[mcp.AfterDecision] {
	normalized, failed, lost := normalizeAfterDecisions(decisions)
	return MergeResult[mcp.AfterDecision]{
		Decision:       mergeAfterDecision(normalized),
		PartialFailure: len(failed) > 0,
		FailedLeases:   failed,
		LostLeases:     lost,
	}
}

func normalizeAfterDecisions(decisions []peerDecision[mcp.AfterDecision]) ([]mcp.AfterDecision, []mcp.LeaseKey, []mcp.LeaseKey) {
	normalized := make([]mcp.AfterDecision, 0, len(decisions))
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
		normalized = append(normalized, mcp.AfterDecision{
			Decision:       normalizeAfterDecision(item.Decision.Decision),
			Patch:          cloneRawMessage(item.Decision.Patch),
			Mutations:      cloneRawMessage(item.Decision.Mutations),
			DispatchIntent: cloneRawMessage(item.Decision.DispatchIntent),
			TTLMs:          item.Decision.TTLMs,
			Reason:         strings.TrimSpace(item.Decision.Reason),
		})
	}
	return normalized, failed, lost
}

func mergeAfterDecision(decisions []mcp.AfterDecision) mcp.AfterDecision {
	if len(decisions) == 0 {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}
	}
	final := highestAfterDecision(decisions)
	merged := mcp.AfterDecision{
		Decision: final,
		Reason:   firstAfterReason(decisions, final),
	}
	if final == mcp.HookDecisionEscalate || final == mcp.HookDecisionApprove {
		candidate, ok := firstAfterByDecision(decisions, final)
		if ok {
			merged.Patch = cloneRawMessage(candidate.Patch)
			merged.Mutations = cloneRawMessage(candidate.Mutations)
			merged.DispatchIntent = cloneRawMessage(candidate.DispatchIntent)
			merged.TTLMs = candidate.TTLMs
		}
	}
	return merged
}

func highestAfterDecision(decisions []mcp.AfterDecision) string {
	best := mcp.HookDecisionApprove
	bestRank := afterRank(best)
	for _, item := range decisions {
		if rank := afterRank(item.Decision); rank > bestRank {
			best = item.Decision
			bestRank = rank
		}
	}
	return best
}

func firstAfterByDecision(decisions []mcp.AfterDecision, want string) (mcp.AfterDecision, bool) {
	for _, item := range decisions {
		if item.Decision == want {
			return item, true
		}
	}
	return mcp.AfterDecision{}, false
}

func firstAfterReason(decisions []mcp.AfterDecision, want string) string {
	for _, item := range decisions {
		if item.Decision == want && item.Reason != "" {
			return item.Reason
		}
	}
	return ""
}

func normalizeAfterDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case mcp.HookDecisionApprove, mcp.HookDecisionEscalate, mcp.HookDecisionReject:
		return strings.ToLower(strings.TrimSpace(decision))
	default:
		return mcp.HookDecisionReject
	}
}

func afterRank(decision string) int {
	switch decision {
	case mcp.HookDecisionReject:
		return 2
	case mcp.HookDecisionEscalate:
		return 1
	default:
		return 0
	}
}
