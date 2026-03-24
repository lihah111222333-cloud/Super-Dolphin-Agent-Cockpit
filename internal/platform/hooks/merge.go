package hooks

import (
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// MergeResult captures the merged hook decision plus fanout failure metadata.
type MergeResult[T any] struct {
	Decision       T
	PartialFailure bool
	FailedLeases   []mcp.LeaseKey
	LostLeases     []mcp.LeaseKey
}

type peerDecision[T any] struct {
	Lease               mcp.LeaseKey
	Decision            T
	Err                 error
	ConsecutiveFailures int
}

func MergeBefore(decisions []peerDecision[mcp.BeforeDecision]) MergeResult[mcp.BeforeDecision] {
	normalized, failed, lost := normalizeBeforeDecisions(decisions)
	merged := mergeBeforeDecision(normalized)
	merged.AllowedTools = mergeAllowedTools(normalized)
	merged.DeniedTools = mergeDeniedTools(normalized)
	return MergeResult[mcp.BeforeDecision]{
		Decision:       merged,
		PartialFailure: len(failed) > 0,
		FailedLeases:   failed,
		LostLeases:     lost,
	}
}

func normalizeBeforeDecisions(decisions []peerDecision[mcp.BeforeDecision]) ([]mcp.BeforeDecision, []mcp.LeaseKey, []mcp.LeaseKey) {
	normalized := make([]mcp.BeforeDecision, 0, len(decisions))
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
		allowedTools := cloneStrings(item.Decision.AllowedTools)
		if item.Decision.AllowedTools != nil && allowedTools == nil {
			allowedTools = []string{}
		}
		normalized = append(normalized, mcp.BeforeDecision{
			Decision:     normalizeBeforeDecision(item.Decision.Decision),
			Patch:        cloneRawMessage(item.Decision.Patch),
			Mutations:    cloneRawMessage(item.Decision.Mutations),
			AllowedTools: allowedTools,
			DeniedTools:  cloneStrings(item.Decision.DeniedTools),
			Mode:         strings.TrimSpace(item.Decision.Mode),
			RetryAfterMs: item.Decision.RetryAfterMs,
			Reason:       strings.TrimSpace(item.Decision.Reason),
		})
	}
	return normalized, failed, lost
}

func mergeBeforeDecision(decisions []mcp.BeforeDecision) mcp.BeforeDecision {
	if len(decisions) == 0 {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}
	}
	final := highestBeforeDecision(decisions)
	merged := mcp.BeforeDecision{
		Decision: final,
		Reason:   firstBeforeReason(decisions, final),
	}
	if final == mcp.HookDecisionWait {
		merged.Mode = firstBeforeMode(decisions)
		merged.RetryAfterMs = maxBeforeRetry(decisions)
	}
	if final == mcp.HookDecisionModify {
		candidate, ok := firstBeforeByDecision(decisions, final)
		if ok {
			merged.Patch = cloneRawMessage(candidate.Patch)
			merged.Mutations = cloneRawMessage(candidate.Mutations)
		}
	}
	return merged
}

func highestBeforeDecision(decisions []mcp.BeforeDecision) string {
	best := mcp.HookDecisionAllow
	bestRank := beforeRank(best)
	for _, item := range decisions {
		if rank := beforeRank(item.Decision); rank > bestRank {
			best = item.Decision
			bestRank = rank
		}
	}
	return best
}

func mergeAllowedTools(decisions []mcp.BeforeDecision) []string {
	var intersection map[string]struct{}
	initialized := false
	for _, item := range decisions {
		if item.AllowedTools == nil {
			continue
		}
		if !initialized {
			intersection = newToolSet(item.AllowedTools)
			initialized = true
			continue
		}
		intersection = intersectToolSet(intersection, item.AllowedTools)
	}
	if !initialized {
		return nil
	}
	return sortedTools(intersection)
}

func mergeDeniedTools(decisions []mcp.BeforeDecision) []string {
	union := make(map[string]struct{})
	for _, item := range decisions {
		for _, tool := range item.DeniedTools {
			if tool = strings.TrimSpace(tool); tool != "" {
				union[tool] = struct{}{}
			}
		}
	}
	if len(union) == 0 {
		return nil
	}
	return sortedTools(union)
}

func firstBeforeByDecision(decisions []mcp.BeforeDecision, want string) (mcp.BeforeDecision, bool) {
	for _, item := range decisions {
		if item.Decision == want {
			return item, true
		}
	}
	return mcp.BeforeDecision{}, false
}

func firstBeforeReason(decisions []mcp.BeforeDecision, want string) string {
	for _, item := range decisions {
		if item.Decision == want && item.Reason != "" {
			return item.Reason
		}
	}
	return ""
}

func firstBeforeMode(decisions []mcp.BeforeDecision) string {
	for _, item := range decisions {
		if item.Decision == mcp.HookDecisionWait && item.Mode != "" {
			return item.Mode
		}
	}
	return ""
}

func maxBeforeRetry(decisions []mcp.BeforeDecision) int64 {
	var retry int64
	for _, item := range decisions {
		if item.Decision == mcp.HookDecisionWait && item.RetryAfterMs > retry {
			retry = item.RetryAfterMs
		}
	}
	return retry
}

func normalizeBeforeDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case mcp.HookDecisionAllow, mcp.HookDecisionModify, mcp.HookDecisionWait, mcp.HookDecisionDeny:
		return strings.ToLower(strings.TrimSpace(decision))
	default:
		return mcp.HookDecisionDeny
	}
}

func beforeRank(decision string) int {
	switch decision {
	case mcp.HookDecisionDeny:
		return 3
	case mcp.HookDecisionWait:
		return 2
	case mcp.HookDecisionModify:
		return 1
	default:
		return 0
	}
}
