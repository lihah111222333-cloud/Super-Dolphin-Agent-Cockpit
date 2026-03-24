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

func MergeDuring(decisions []peerDecision[mcp.CheckDecision]) MergeResult[mcp.CheckDecision] {
	normalized, failed, lost := normalizeCheckDecisions(decisions)
	return MergeResult[mcp.CheckDecision]{
		Decision:       mergeCheckDecision(normalized),
		PartialFailure: len(failed) > 0,
		FailedLeases:   failed,
		LostLeases:     lost,
	}
}

func MergeAfter(decisions []peerDecision[mcp.AfterDecision]) MergeResult[mcp.AfterDecision] {
	normalized, failed, lost := normalizeAfterDecisions(decisions)
	return MergeResult[mcp.AfterDecision]{
		Decision:       mergeAfterDecision(normalized),
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
			Reason:         strings.TrimSpace(item.Decision.Reason),
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

func firstAfterByDecision(decisions []mcp.AfterDecision, want string) (mcp.AfterDecision, bool) {
	for _, item := range decisions {
		if item.Decision == want {
			return item, true
		}
	}
	return mcp.AfterDecision{}, false
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

func firstAfterReason(decisions []mcp.AfterDecision, want string) string {
	for _, item := range decisions {
		if item.Decision == want && item.Reason != "" {
			return item.Reason
		}
	}
	return ""
}

func normalizeBeforeDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case mcp.HookDecisionAllow, mcp.HookDecisionModify, mcp.HookDecisionWait, mcp.HookDecisionDeny:
		return strings.ToLower(strings.TrimSpace(decision))
	default:
		return mcp.HookDecisionDeny
	}
}

func normalizeCheckDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case mcp.HookDecisionContinue, mcp.HookDecisionWarn, mcp.HookDecisionAbort:
		return strings.ToLower(strings.TrimSpace(decision))
	default:
		return mcp.HookDecisionContinue
	}
}

func normalizeAfterDecision(decision string) string {
	switch strings.ToLower(strings.TrimSpace(decision)) {
	case mcp.HookDecisionApprove, mcp.HookDecisionEscalate, mcp.HookDecisionReject:
		return strings.ToLower(strings.TrimSpace(decision))
	default:
		return mcp.HookDecisionReject
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

func appendUniqueLease(leases []mcp.LeaseKey, seen map[mcp.LeaseKey]struct{}, lease mcp.LeaseKey) []mcp.LeaseKey {
	if lease == (mcp.LeaseKey{}) {
		return leases
	}
	if _, ok := seen[lease]; ok {
		return leases
	}
	seen[lease] = struct{}{}
	return append(leases, lease)
}
