package waitaggregate

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

func pendingItems(targets []Target) []Item {
	items := make([]Item, len(targets))
	for index, target := range targets {
		items[index] = Item{TargetKey: target.Key, Outcome: OutcomePending}
	}
	return items
}

// classify 将单目标等待结果映射为保守的聚合 outcome；未知错误和状态保持 indeterminate。
func classify(predicate Predicate, result accesswait.WaitResult, err error) ItemOutcome {
	if err != nil {
		return classifyError(err)
	}
	return classifyResult(predicate, result)
}

// classifyError 将等待端口错误归类；未知错误不能进入 aggregate failure。
func classifyError(err error) ItemOutcome {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return OutcomePending
	case errors.Is(err, accesswait.ErrWaitGap),
		errors.Is(err, accesswait.ErrWaitQuarantined),
		errors.Is(err, accesswait.ErrWaitScanLimit):
		return OutcomeIndeterminate
	case isDefinitiveAccessError(err):
		return OutcomeDefinitiveFailure
	default:
		return OutcomeIndeterminate
	}
}

// isDefinitiveAccessError 识别已由授权或句柄终态确定的失败。
func isDefinitiveAccessError(err error) bool {
	return errors.Is(err, accesswait.ErrWaitNotFound) ||
		errors.Is(err, accesswait.ErrWaitTerminal) ||
		errors.Is(err, accesswait.ErrAccessDenied) ||
		errors.Is(err, accesswait.ErrAccessExpired) ||
		errors.Is(err, accesswait.ErrAccessRevoked) ||
		errors.Is(err, accesswait.ErrAccessConflict)
}

// classifyResult 将无错误的 durable 状态映射为 predicate outcome。
func classifyResult(predicate Predicate, result accesswait.WaitResult) ItemOutcome {
	switch result.Status {
	case "pending":
		return OutcomePending
	case "satisfied":
		if result.IsError {
			return OutcomeDefinitiveFailure
		}
		if predicateMatches(predicate, result) {
			return OutcomeSuccess
		}
		return OutcomeDefinitiveFailure
	case "timed_out", "cancelled", "revoked", "expired":
		return OutcomeDefinitiveFailure
	case "indeterminate":
		return OutcomeIndeterminate
	default:
		return OutcomeIndeterminate
	}
}

// predicateMatches 按受限 predicate 检查 durable wait 的 canonical 事件摘要。
func predicateMatches(predicate Predicate, result accesswait.WaitResult) bool {
	if result.Status != "satisfied" {
		return false
	}
	event := result.Event
	if predicate.RequiredEventType != "" {
		if event == nil || event.Envelope.EventType != predicate.RequiredEventType {
			return false
		}
	}
	if predicate.RequireNonEmptyMetadata && !eventHasMetadata(event) {
		return false
	}
	return true
}

// eventHasMetadata 只检查 envelope 元数据，不读取或拼接正文。
func eventHasMetadata(event *eventstore.AgentEvent) bool {
	if event == nil {
		return false
	}
	envelope := event.Envelope
	return envelope.ContentRole != "" && envelope.ContentRole != "none" && envelope.ContentRef != "" && envelope.ContentSize > 0 && envelope.MediaType != ""
}

// conditionReachedByItems 判断当前成功项是否已满足 any/all/quorum 条件。
func conditionReachedByItems(request Request, items []Item) bool {
	successCount := 0
	for _, item := range items {
		if item.Outcome == OutcomeSuccess {
			successCount++
		}
	}
	switch request.Condition {
	case ConditionAny, ConditionFirstSuccess:
		return successCount > 0
	case ConditionAll:
		return len(items) > 0 && successCount == len(items)
	case ConditionQuorum:
		return request.QuorumCount > 0 && successCount >= request.QuorumCount
	default:
		return false
	}
}

// finalize 组装稳定顺序的聚合结果，并区分满足、超时、失败和不确定。
func finalize(request Request, items []Item, reached, indeterminateReturn bool) Result {
	result := Result{Condition: request.Condition, Items: items}
	for _, item := range items {
		if item.Outcome == OutcomeSuccess {
			result.SuccessCount++
			if result.WinnerKey == "" {
				result.WinnerKey = item.TargetKey
			}
		}
	}
	if reached {
		result.Status = StatusSatisfied
		return result
	}
	if indeterminateReturn || hasOutcome(items, OutcomeIndeterminate) {
		result.Status = StatusIndeterminate
		return result
	}
	if allDefinitiveFailure(items) {
		result.Status = StatusAggregateFailure
		result.IsError = true
		return result
	}
	result.Status = StatusTimeout
	return result
}

func hasOutcome(items []Item, want ItemOutcome) bool {
	for _, item := range items {
		if item.Outcome == want {
			return true
		}
	}
	return false
}

func allDefinitiveFailure(items []Item) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.Outcome != OutcomeDefinitiveFailure {
			return false
		}
	}
	return true
}
