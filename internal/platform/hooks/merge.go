package hooks

import (
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
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

// MergeBefore 合并before。
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
	return normalizePeerDecisions(decisions, func(decision mcp.BeforeDecision) mcp.BeforeDecision {
		allowedTools := shared.CloneStrings(decision.AllowedTools)
		if decision.AllowedTools != nil && allowedTools == nil {
			allowedTools = []string{}
		}
		return mcp.BeforeDecision{
			Decision:     normalizeDecision(decision.Decision, beforeDecisionConfig),
			Patch:        shared.CloneRawMessage(decision.Patch),
			Mutations:    shared.CloneRawMessage(decision.Mutations),
			AllowedTools: allowedTools,
			DeniedTools:  shared.CloneStrings(decision.DeniedTools),
			Mode:         strings.TrimSpace(decision.Mode),
			RetryAfterMs: decision.RetryAfterMs,
			Reason:       strings.TrimSpace(decision.Reason),
		}
	})
}

// mergeBeforeDecision 合并beforedecision。
func mergeBeforeDecision(decisions []mcp.BeforeDecision) mcp.BeforeDecision {
	if len(decisions) == 0 {
		return mcp.BeforeDecision{Decision: mcp.HookDecisionDeny}
	}
	final := highestBeforeDecision(decisions)
	merged := mcp.BeforeDecision{
		Decision: final,
	}
	if item, ok := firstMatching(decisions, func(item mcp.BeforeDecision) bool {
		return item.Decision == final && item.Reason != ""
	}); ok {
		merged.Reason = item.Reason
	}
	if final == mcp.HookDecisionWait {
		if item, ok := firstMatching(decisions, func(item mcp.BeforeDecision) bool {
			return item.Decision == mcp.HookDecisionWait && item.Mode != ""
		}); ok {
			merged.Mode = item.Mode
		}
		merged.RetryAfterMs = maxBeforeRetry(decisions)
	}
	if final == mcp.HookDecisionModify {
		if candidate, ok := firstMatching(decisions, func(item mcp.BeforeDecision) bool {
			return item.Decision == final
		}); ok {
			merged.Patch = shared.CloneRawMessage(candidate.Patch)
			merged.Mutations = shared.CloneRawMessage(candidate.Mutations)
		}
	}
	return merged
}

func highestBeforeDecision(decisions []mcp.BeforeDecision) string {
	return chooseDecision(decisions, func(item mcp.BeforeDecision) string {
		return item.Decision
	}, beforeDecisionConfig)
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

// mergeDeniedTools 合并denied工具。
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

func maxBeforeRetry(decisions []mcp.BeforeDecision) int64 {
	var retry int64
	for _, item := range decisions {
		if item.Decision == mcp.HookDecisionWait && item.RetryAfterMs > retry {
			retry = item.RetryAfterMs
		}
	}
	return retry
}
