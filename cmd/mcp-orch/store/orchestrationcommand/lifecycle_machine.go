package orchestrationcommand

import (
	"context"
	"fmt"

	"github.com/qmuntal/stateless"
)

type phaseTransition struct {
	From    Phase
	Trigger Trigger
	To      Phase
}

// phaseTransitions 是唯一转换表；持久化和测试不能另造 unknown 重发或终态复活路径。
func phaseTransitions() []phaseTransition {
	return []phaseTransition{
		{PhaseCustodyPending, TriggerAccept, PhaseAccepted},
		{PhaseCustodyPending, TriggerAbortAccept, PhaseRejected},
		{PhaseAccepted, TriggerBeginDispatch, PhaseDispatchPending},
		{PhaseAccepted, TriggerRejectBeforeDispatch, PhaseRejected},
		{PhaseDispatchPending, TriggerReclaimUnsent, PhaseDispatchPending},
		{PhaseDispatchPending, TriggerConfirmDispatch, PhaseDispatched},
		{PhaseDispatchPending, TriggerAbortDispatch, PhaseDispatchAborted},
		{PhaseDispatchAborted, TriggerRejectBeforeDispatch, PhaseRejected},
		{PhaseDispatched, TriggerRedeemEffect, PhaseEffectPending},
		{PhaseEffectPending, TriggerIssueToken, PhaseTokenIssued},
		{PhaseEffectPending, TriggerAbortBeforeIssue, PhaseAbortedBeforeIssue},
		{PhaseTokenIssued, TriggerStartEffect, PhaseEffectStarted},
		{PhaseTokenIssued, TriggerAbortBeforeStart, PhaseAbortedBeforeStart},
		{PhaseAbortedBeforeIssue, TriggerBeginCleanup, PhaseCleanupPending},
		{PhaseAbortedBeforeStart, TriggerBeginCleanup, PhaseCleanupPending},
		{PhaseCleanupPending, TriggerFinishCleanup, PhaseRejected},
		{PhaseEffectStarted, TriggerProviderAck, PhaseProviderAcknowledged},
		{PhaseProviderAcknowledged, TriggerComplete, PhaseCompleted},
		{PhaseEffectStarted, TriggerFailEffect, PhaseFailed},
		{PhaseProviderAcknowledged, TriggerFailEffect, PhaseFailed},
		{PhaseTokenIssued, TriggerMarkUnknown, PhaseUnknown},
		{PhaseEffectStarted, TriggerMarkUnknown, PhaseUnknown},
		{PhaseProviderAcknowledged, TriggerMarkUnknown, PhaseUnknown},
		{PhaseUnknown, TriggerReconcileAck, PhaseProviderAcknowledged},
		{PhaseUnknown, TriggerReconcileComplete, PhaseCompleted},
		{PhaseUnknown, TriggerReconcileFailed, PhaseFailed},
		{PhaseUnknown, TriggerReconcileUnstarted, PhaseAbortedBeforeStart},
	}
}

// nextCommandPhase 通过 stateless 执行纯转换判断；数据库仍需以原 version 原子追加。
func nextCommandPhase(ctx context.Context, phase Phase, trigger Trigger) (Phase, error) {
	sm := stateless.NewStateMachineWithMode(phase, stateless.FiringQueued)
	for _, rule := range phaseTransitions() {
		state := sm.Configure(rule.From)
		if rule.From == rule.To {
			state.PermitReentry(rule.Trigger)
		} else {
			state.Permit(rule.Trigger, rule.To)
		}
	}
	if err := sm.FireCtx(ctx, trigger); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidTransition, err)
	}
	state, err := sm.State(ctx)
	if err != nil {
		return "", err
	}
	result, ok := state.(Phase)
	if !ok {
		return "", ErrInvalidTransition
	}
	return result, nil
}

func knownPhase(phase Phase) bool {
	for _, rule := range phaseTransitions() {
		if rule.From == phase || rule.To == phase {
			return true
		}
	}
	return false
}

func startsAttempt(trigger Trigger) bool {
	return trigger == TriggerBeginDispatch || trigger == TriggerReclaimUnsent
}

// validateAttemptFence 只有已证明未送出的 reclaim 可以换 attempt；unknown 永远不能复用派发路径。
func validateAttemptFence(current Snapshot, r TransitionRequest) error {
	if r.Trigger == TriggerReconcileUnstarted && current.UncertainPhase != PhaseTokenIssued {
		return ErrInvalidTransition
	}
	if startsAttempt(r.Trigger) {
		if r.AttemptID == "" || r.AttemptID == current.AttemptID {
			return ErrTransitionConflict
		}
		return nil
	}
	if r.AttemptID != current.AttemptID {
		return ErrTransitionConflict
	}
	return nil
}

// planTransition 只组合已验证的本地状态，不签发运行权限或把 accepted 当 provider acknowledgment。
func planTransition(ctx context.Context, current Snapshot, r TransitionRequest, digest string) (verifiedTransition, error) {
	if current.Version != r.ExpectedVersion {
		return verifiedTransition{}, ErrTransitionConflict
	}
	next, err := nextCommandPhase(ctx, current.Phase, r.Trigger)
	if err != nil {
		return verifiedTransition{}, err
	}
	if err := validateAttemptFence(current, r); err != nil {
		return verifiedTransition{}, err
	}
	plan := verifiedTransition{Request: r, Current: current, NextPhase: next,
		NextAcceptResolution: current.AcceptResolution, OperationDigest: digest, StartsAttempt: startsAttempt(r.Trigger)}
	switch r.Trigger {
	case TriggerAccept:
		plan.NextAcceptResolution = "accepted"
	case TriggerAbortAccept:
		plan.NextAcceptResolution = "aborted"
	case TriggerMarkUnknown:
		plan.UncertainPhase = current.Phase
	}
	return plan, nil
}
