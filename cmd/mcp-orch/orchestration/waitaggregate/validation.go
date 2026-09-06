package waitaggregate

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxTargets 是单次聚合允许的最大目标数。
	MaxTargets = 32
	// MinTimeoutMS 是 wait deadline 允许的最短窗口。过短的窗口会在
	// caller deadline 捕获与 owner 校验之间发生竞态，导致请求尚未进入
	// owner 就已经过期。
	MinTimeoutMS = 10
	// MaxTimeoutMS 是 wait deadline 允许的最长窗口。
	MaxTimeoutMS     = 60000
	maxTargets       = MaxTargets
	minWaitWindow    = time.Duration(MinTimeoutMS) * time.Millisecond
	maxWaitWindow    = time.Duration(MaxTimeoutMS) * time.Millisecond
	maxTargetKeySize = 128
)

// validate 检查目标数量、条件、typed predicate、身份范围和截止时间。
func (r Request) validate(now time.Time) error {
	if len(r.Targets) < 1 || len(r.Targets) > maxTargets {
		return invalid("targets must contain 1..32 items")
	}
	if err := validateCondition(r); err != nil {
		return err
	}
	if err := validateDeadline(r.DeadlineAt, now); err != nil {
		return err
	}
	if err := validatePredicate(r); err != nil {
		return err
	}
	return validateTargets(r.Targets)
}

// validateCondition 校验聚合模式与 quorum_count 的判别联合。
func validateCondition(r Request) error {
	switch r.Condition {
	case ConditionAny, ConditionAll, ConditionFirstSuccess:
		if r.QuorumCount != 0 {
			return invalid("quorum_count is only valid for quorum")
		}
	case ConditionQuorum:
		if r.QuorumCount < 1 || r.QuorumCount > len(r.Targets) {
			return invalid("quorum_count must be within target count")
		}
	default:
		return invalid("condition must be any, all, quorum, or first_success")
	}
	return nil
}

func validateDeadline(deadline, now time.Time) error {
	if deadline.IsZero() {
		return invalid("deadline_at is required")
	}
	if deadline.Location() != time.UTC {
		return invalid("deadline_at must be UTC")
	}
	if !deadline.After(now) {
		return invalid("deadline_at must be in the future")
	}
	if deadline.Sub(now) < minWaitWindow {
		return invalid("deadline_at is below the 10 millisecond minimum window")
	}
	if deadline.Sub(now) > maxWaitWindow {
		return invalid("deadline_at exceeds 60 second bound")
	}
	return nil
}

// validatePredicate 校验受限事件 predicate，不执行任意表达式或正文读取。
func validatePredicate(r Request) error {
	p := r.Predicate
	hasPredicate := p.RequiredEventType != "" || p.RequireNonEmptyMetadata
	if r.Condition == ConditionFirstSuccess && p.RequiredEventType == "" {
		return invalid("first_success requires required_event_type predicate")
	}
	if !hasPredicate {
		return nil
	}
	if p.RequiredEventType == "" || strings.TrimSpace(p.RequiredEventType) != p.RequiredEventType || !utf8.ValidString(p.RequiredEventType) {
		return invalid("predicate required_event_type is required canonical text")
	}
	if len(p.RequiredEventType) > 128 {
		return invalid("predicate required_event_type is too long")
	}
	return nil
}

// validateTargets 校验 target key 唯一性、poll 合同和 caller/scope 同质性。
func validateTargets(targets []Target) error {
	caller, scope := targets[0].Poll.CallerPrincipalRef, targets[0].Poll.ScopeRef
	seen := make(map[string]struct{}, len(targets))
	for i, target := range targets {
		if err := validateTargetKey(target.Key, i, seen); err != nil {
			return err
		}
		seen[target.Key] = struct{}{}
		if err := validateTargetPoll(target, i, caller, scope); err != nil {
			return err
		}
	}
	return nil
}

// validateTargetKey 校验单个 target key 的 canonical 文本和重复性。
func validateTargetKey(key string, index int, seen map[string]struct{}) error {
	if key == "" || strings.TrimSpace(key) != key || len(key) > maxTargetKeySize || !utf8.ValidString(key) || strings.ContainsRune(key, 0) {
		return invalid(fmt.Sprintf("target[%d] key is invalid", index))
	}
	if _, exists := seen[key]; exists {
		return invalid("target keys must be unique")
	}
	return nil
}

func validateTargetPoll(target Target, index int, caller, scope string) error {
	if err := target.Poll.Validate(); err != nil {
		return invalid(fmt.Sprintf("target[%d] poll: %v", index, err))
	}
	if target.Poll.CallerPrincipalRef != caller || target.Poll.ScopeRef != scope {
		return invalid("all targets must use the same caller and scope")
	}
	return nil
}

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalidRequest, message) }
