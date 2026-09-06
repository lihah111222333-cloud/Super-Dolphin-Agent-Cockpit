package waitoperation

import (
	"errors"
	"fmt"
	"reflect"
	"time"
)

// validateReadTargetTransition mirrors migration 136's transition and
// resource-continuity guards for rows loaded from an append-only journal.
func validateReadTargetTransition(previous, next TargetState) error {
	if err := validateReadTargetIdentity(previous, next); err != nil {
		return err
	}
	if targetLifecycleEqual(previous, next) {
		return nil
	}
	if !allowedReadTargetTransition(previous, next) {
		return errors.Join(ErrInvalidOperation, fmt.Errorf("target phase transition %s/%s -> %s/%s is invalid", previous.Phase, previous.UncertainPhase, next.Phase, next.UncertainPhase))
	}
	if err := validateReadResourceContinuity(previous, next); err != nil {
		return err
	}
	return nil
}

// validateReadTargetIdentity 校验恢复读取时不可改变的目标身份和引用字段。
func validateReadTargetIdentity(previous, next TargetState) error {
	if targetTransitionIdentityEqual(previous, next) {
		return nil
	}
	return errors.Join(ErrInvalidOperation, errors.New("target transition identity changed"))
}

// targetTransitionIdentityEqual 比较目标日志跨版本必须保持不变的字段。
func targetTransitionIdentityEqual(previous, next TargetState) bool {
	return targetOperationIdentityEqual(previous, next) && targetFenceIdentityEqual(previous, next) && targetReferenceIdentityEqual(previous, next)
}

// targetOperationIdentityEqual 比较操作、目标键和规范摘要身份。
func targetOperationIdentityEqual(previous, next TargetState) bool {
	return previous.OperationKey == next.OperationKey && previous.OperationDigest == next.OperationDigest &&
		previous.Key == next.Key && previous.TargetIndex == next.TargetIndex &&
		previous.TargetDigest == next.TargetDigest && previous.SchemaVersion == next.SchemaVersion
}

// targetFenceIdentityEqual 比较目标 fence、选择器和来源游标。
func targetFenceIdentityEqual(previous, next TargetState) bool {
	return reflect.DeepEqual(previous.Target, next.Target) && reflect.DeepEqual(previous.Selector, next.Selector) &&
		previous.AfterSequence == next.AfterSequence && previous.AfterCursor == next.AfterCursor &&
		previous.DeadlineAt.Equal(next.DeadlineAt)
}

// targetReferenceIdentityEqual 比较 grant/wait 幂等命名空间和摘要。
func targetReferenceIdentityEqual(previous, next TargetState) bool {
	return previous.OriginalGrantID == next.OriginalGrantID && previous.GrantNamespace == next.GrantNamespace &&
		previous.GrantKeyDigest == next.GrantKeyDigest && previous.GrantRequestDigest == next.GrantRequestDigest &&
		previous.WaitNamespace == next.WaitNamespace && previous.WaitKeyDigest == next.WaitKeyDigest &&
		previous.WaitRequestDigest == next.WaitRequestDigest
}

// targetLifecycleEqual 比较允许阶段转换携带的全部资源和错误字段。
func targetLifecycleEqual(previous, next TargetState) bool {
	return targetGrantLifecycleEqual(previous, next) && targetWaitLifecycleEqual(previous, next) && targetPhaseOutcomeEqual(previous, next)
}

// targetGrantLifecycleEqual 比较 grant 引用、版本、状态和清理标记。
func targetGrantLifecycleEqual(previous, next TargetState) bool {
	return optionalStringEqual(previous.ResolvedGrantID, next.ResolvedGrantID) &&
		previous.GrantRevision == next.GrantRevision && previous.GrantStatus == next.GrantStatus &&
		previous.GrantCleanupState == next.GrantCleanupState
}

// targetWaitLifecycleEqual 比较 wait 引用、版本、状态和清理标记。
func targetWaitLifecycleEqual(previous, next TargetState) bool {
	return optionalStringEqual(previous.ResolvedWaitHandleID, next.ResolvedWaitHandleID) &&
		previous.WaitRevision == next.WaitRevision && previous.WaitStatus == next.WaitStatus &&
		previous.WaitCleanupState == next.WaitCleanupState
}

// targetPhaseOutcomeEqual 比较阶段、错误投影和解析时间。
func targetPhaseOutcomeEqual(previous, next TargetState) bool {
	return previous.Phase == next.Phase && previous.UncertainPhase == next.UncertainPhase &&
		previous.ErrorCode == next.ErrorCode && previous.ErrorRef == next.ErrorRef &&
		optionalTimeEqual(previous.ResolvedAt, next.ResolvedAt)
}

// allowedReadTargetTransition 返回 migration 136 允许的阶段边。
func allowedReadTargetTransition(previous, next TargetState) bool {
	switch previous.Phase {
	case "planned", "grant_pending", "wait_pending":
		return allowedReadProvisioningTransition(previous.Phase, next.Phase)
	case "waiting":
		return allowedReadWaitingTransition(next.Phase)
	case "satisfied", "timed_out", "cancelled", "failed":
		return allowedReadTerminalTransition(next.Phase)
	case "outcome_unknown":
		return allowedReadUnknownTransition(previous, next)
	case "cleanup_pending":
		return next.Phase == "cleanup_cleared"
	default:
		return false
	}
}

// allowedReadProvisioningTransition 校验 planned/grant_pending/wait_pending 的前进边。
func allowedReadProvisioningTransition(previous, next string) bool {
	switch previous {
	case "planned":
		return next == "grant_pending" || next == "outcome_unknown" || next == "failed"
	case "grant_pending":
		return next == "wait_pending" || next == "outcome_unknown" || next == "failed"
	case "wait_pending":
		return next == "waiting" || next == "outcome_unknown" || next == "failed"
	default:
		return false
	}
}

// allowedReadWaitingTransition 校验 waiting 的终态、未知和清理边。
func allowedReadWaitingTransition(next string) bool {
	return next == "satisfied" || next == "timed_out" || next == "cancelled" || next == "outcome_unknown" || next == "cleanup_pending"
}

// allowedReadTerminalTransition 校验终态仅能进入清理或未知分支。
func allowedReadTerminalTransition(next string) bool {
	return next == "cleanup_pending" || next == "outcome_unknown"
}

// allowedReadUnknownTransition 校验各类 outcome_unknown 的恢复边。
func allowedReadUnknownTransition(previous, next TargetState) bool {
	switch previous.UncertainPhase {
	case "grant_create":
		return next.Phase == "grant_pending" || next.Phase == "wait_pending" || next.Phase == "failed"
	case "wait_create":
		return next.Phase == "wait_pending" || next.Phase == "waiting" || next.Phase == "failed"
	case "target_transition":
		return allowedReadUnknownTargetTransition(previous.GrantStatus, previous.WaitStatus, next.Phase)
	}
	return false
}

// allowedReadUnknownTargetTransition 校验 target_transition 未知状态的资源分支。
func allowedReadUnknownTargetTransition(grantStatus, waitStatus, next string) bool {
	if grantStatus == "planned" && waitStatus == "absent" {
		return next == "grant_pending" || next == "failed"
	}
	if grantStatus == "active" && waitStatus == "planned" {
		return allowedReadUnknownGrantReady(next)
	}
	if grantStatus == "active" && waitStatus == "pending" {
		return allowedReadUnknownWaitReady(next)
	}
	return false
}

// allowedReadUnknownGrantReady 校验已建 grant、尚未建 wait 的未知恢复边。
func allowedReadUnknownGrantReady(next string) bool {
	return next == "wait_pending" || next == "waiting" || next == "failed" || next == "cleanup_pending"
}

// allowedReadUnknownWaitReady 校验已建 wait 的未知恢复边。
func allowedReadUnknownWaitReady(next string) bool {
	return next == "waiting" || next == "satisfied" || next == "timed_out" || next == "cancelled" || next == "cleanup_pending"
}

// validateReadResourceContinuity 校验未知和清理转换不丢失 durable 资源。
func validateReadResourceContinuity(previous, next TargetState) error {
	if err := validateReadUnknownContinuity(previous, next); err != nil {
		return err
	}
	return validateReadCleanupContinuity(previous, next)
}

// validateReadUnknownContinuity 校验非 cleanup 未知状态保留已知资源终态。
func validateReadUnknownContinuity(previous, next TargetState) error {
	if next.Phase != "outcome_unknown" || next.UncertainPhase == "cleanup" {
		return nil
	}
	if !sameResolvedResources(previous, next) {
		return errors.Join(ErrInvalidOperation, errors.New("unknown target transition dropped a resolved resource"))
	}
	if terminalGrantReopened(previous, next) {
		return errors.Join(ErrInvalidOperation, errors.New("unknown target transition reopened a terminal grant"))
	}
	if terminalWaitReopened(previous, next) {
		return errors.Join(ErrInvalidOperation, errors.New("unknown target transition reopened a terminal wait"))
	}
	return nil
}

// validateReadCleanupContinuity 校验 cleanup 分支只递增资源 revision、不清空引用。
func validateReadCleanupContinuity(previous, next TargetState) error {
	if !isReadCleanupTransition(next) {
		return nil
	}
	if !sameResolvedResources(previous, next) {
		return errors.Join(ErrInvalidOperation, errors.New("cleanup transition changed a resolved resource"))
	}
	if clearedReadResource(previous, next) {
		return errors.Join(ErrInvalidOperation, errors.New("cleanup transition erased a resolved resource"))
	}
	if previous.ResolvedGrantID != nil && next.GrantRevision < previous.GrantRevision {
		return errors.Join(ErrInvalidOperation, errors.New("cleanup transition decreased grant revision"))
	}
	if previous.ResolvedWaitHandleID != nil && next.WaitRevision < previous.WaitRevision {
		return errors.Join(ErrInvalidOperation, errors.New("cleanup transition decreased wait revision"))
	}
	return nil
}

// isReadCleanupTransition identifies cleanup pending/cleared and cleanup unknown rows.
func isReadCleanupTransition(next TargetState) bool {
	return next.Phase == "cleanup_pending" || next.Phase == "cleanup_cleared" ||
		(next.Phase == "outcome_unknown" && next.UncertainPhase == "cleanup")
}

// sameResolvedResources 检查 grant 和 wait 的 durable identity 是否连续。
func sameResolvedResources(previous, next TargetState) bool {
	return optionalStringEqual(previous.ResolvedGrantID, next.ResolvedGrantID) && optionalStringEqual(previous.ResolvedWaitHandleID, next.ResolvedWaitHandleID)
}

// terminalGrantReopened 检查未知转换是否把终态 grant 改回可变状态。
func terminalGrantReopened(previous, next TargetState) bool {
	return (previous.GrantStatus == "revoked" || previous.GrantStatus == "expired") && previous.GrantStatus != next.GrantStatus
}

// terminalWaitReopened 检查未知转换是否把终态 wait 改回可变状态。
func terminalWaitReopened(previous, next TargetState) bool {
	return isTerminalWaitStatus(previous.WaitStatus) && previous.WaitStatus != next.WaitStatus
}

// clearedReadResource 检查清理转换是否把已解析资源改成空态或未知态。
func clearedReadResource(previous, next TargetState) bool {
	return (previous.ResolvedGrantID != nil && clearedGrantStatus(next.GrantStatus)) ||
		(previous.ResolvedWaitHandleID != nil && clearedWaitStatus(next.WaitStatus))
}

// clearedGrantStatus 返回清理中不允许的 grant 状态。
func clearedGrantStatus(status string) bool {
	return status == "absent" || status == "planned" || status == "unknown"
}

// clearedWaitStatus 返回清理中不允许的 wait 状态。
func clearedWaitStatus(status string) bool {
	return status == "absent" || status == "planned" || status == "unknown"
}

func optionalStringEqual(previous, next *string) bool {
	if previous == nil || next == nil {
		return previous == nil && next == nil
	}
	return *previous == *next
}

func optionalTimeEqual(previous, next *time.Time) bool {
	if previous == nil || next == nil {
		return previous == nil && next == nil
	}
	return previous.Equal(*next)
}
