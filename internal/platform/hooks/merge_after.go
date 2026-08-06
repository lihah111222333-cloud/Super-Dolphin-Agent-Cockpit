package hooks

import (
	"strings"

	mcp "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// MergeAfter 合并 after 阶段多个 peer 的决策。
// escalate/reject 优先级高于 approve，失败 lease 会随结果返回给 Manager 清理。
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
	config := afterDecisionConfig()
	return normalizePeerDecisions(decisions, func(decision mcp.AfterDecision) mcp.AfterDecision {
		return mcp.AfterDecision{
			Decision:       normalizeDecision(decision.Decision, config),
			Patch:          shared.CloneRawMessage(decision.Patch),
			Mutations:      shared.CloneRawMessage(decision.Mutations),
			DispatchIntent: shared.CloneRawMessage(decision.DispatchIntent),
			TTLMs:          decision.TTLMs,
			Reason:         strings.TrimSpace(decision.Reason),
		}
	})
}

// mergeAfterDecision 选择 after 阶段最终决策并复制对应 peer 的 patch、mutation 和 dispatch intent。
func mergeAfterDecision(decisions []mcp.AfterDecision) mcp.AfterDecision {
	if len(decisions) == 0 {
		return mcp.AfterDecision{Decision: mcp.HookDecisionReject}
	}
	final := highestAfterDecision(decisions)
	merged := mcp.AfterDecision{
		Decision: final,
	}
	if item, ok := firstMatching(decisions, func(item mcp.AfterDecision) bool {
		return item.Decision == final && item.Reason != ""
	}); ok {
		merged.Reason = item.Reason
	}
	if final == mcp.HookDecisionEscalate || final == mcp.HookDecisionApprove {
		if candidate, ok := firstMatching(decisions, func(item mcp.AfterDecision) bool {
			return item.Decision == final
		}); ok {
			merged.Patch = shared.CloneRawMessage(candidate.Patch)
			merged.Mutations = shared.CloneRawMessage(candidate.Mutations)
			merged.DispatchIntent = shared.CloneRawMessage(candidate.DispatchIntent)
			merged.TTLMs = candidate.TTLMs
		}
	}
	return merged
}

func highestAfterDecision(decisions []mcp.AfterDecision) string {
	config := afterDecisionConfig()
	return chooseDecision(decisions, func(item mcp.AfterDecision) string {
		return item.Decision
	}, config)
}
