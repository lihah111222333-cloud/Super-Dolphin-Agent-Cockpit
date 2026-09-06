package waitoperation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
)

// validateShape 校验计划请求的身份、源 fence、deadline 和目标集合。
func (request PlanRequest) validateShape() error {
	if err := validatePlanIdentity(request); err != nil {
		return err
	}
	if err := request.Source.ValidateBound(); err != nil {
		return invalidOperation("source fence: %v", err)
	}
	if request.DeadlineAt.IsZero() || request.DeadlineAt.Location() != time.UTC {
		return invalidOperation("deadline_at must be a UTC timestamp")
	}
	if len(request.Targets) == 0 || int64(len(request.Targets)) > MaxTargetCount {
		return invalidOperation("target count must be between 1 and 32")
	}
	return validatePlanTargets(request)
}

func validatePlanIdentity(request PlanRequest) error {
	if err := validateText("operation_key", request.OperationKey, MaxOperationKey, false); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", request.OperationDigest); err != nil {
		return err
	}
	if err := validateText("caller_principal_ref", request.CallerPrincipalRef, 512, false); err != nil {
		return err
	}
	return validateText("scope_ref", request.ScopeRef, 512, false)
}

func validatePlanTargets(request PlanRequest) error {
	seen := make(map[string]struct{}, len(request.Targets))
	for index, target := range request.Targets {
		if err := target.validateShape(request.Source, index+1); err != nil {
			return err
		}
		if _, exists := seen[target.Key]; exists {
			return invalidOperation("target keys must be unique")
		}
		seen[target.Key] = struct{}{}
	}
	return nil
}

// validateShape 校验单个目标 fence、选择器、游标和引用字段。
func (target TargetPlan) validateShape(source accesswait.FenceSnapshot, index int) error {
	if err := validateText("target_key", target.Key, MaxTargetKey, false); err != nil {
		return err
	}
	if err := target.Target.ValidateBound(); err != nil {
		return invalidOperation("target %q fence: %v", target.Key, err)
	}
	if source.AgentID == target.Target.AgentID {
		return invalidOperation("source and target agents must differ")
	}
	if err := validateTargetSelector(target, source); err != nil {
		return err
	}
	if err := validateTargetPlanCursor(target); err != nil {
		return err
	}
	return validateTargetReferences(target, index)
}

// validateTargetSelector 校验目标选择器与源/目标 fence 的绑定。
func validateTargetSelector(target TargetPlan, source accesswait.FenceSnapshot) error {
	if err := target.Selector.Validate(); err != nil {
		return invalidOperation("target %q selector: %v", target.Key, err)
	}
	if target.Selector.TargetAgentID != target.Target.AgentID {
		return invalidOperation("target %q selector target does not match fence", target.Key)
	}
	sourceSelectorMatches := target.Selector.SourceID == "task/"+source.AgentID && target.Selector.SourceEpoch == source.IncarnationID
	targetSelectorMatches := target.Selector.SourceID == "task/"+target.Target.AgentID && target.Selector.SourceEpoch == target.Target.IncarnationID
	if !sourceSelectorMatches && !targetSelectorMatches {
		return invalidOperation("target %q selector source does not match source fence", target.Key)
	}
	return nil
}

func validateTargetPlanCursor(target TargetPlan) error {
	if target.AfterSequence < 0 {
		return invalidOperation("target %q after sequence cannot be negative", target.Key)
	}
	wantCursor := ""
	if target.AfterSequence > 0 {
		var err error
		wantCursor, err = eventstore.CanonicalCursor(target.Selector.SourceID, target.Selector.SourceEpoch, target.Selector.Lane, target.AfterSequence)
		if err != nil {
			return invalidOperation("target %q after cursor: %v", target.Key, err)
		}
	}
	if target.AfterCursor != wantCursor {
		return invalidOperation("target %q after cursor does not match source sequence", target.Key)
	}
	return nil
}

// validateTargetReferences 校验可选的 grant/wait 幂等引用字段。
func validateTargetReferences(target TargetPlan, index int) error {
	if err := validateOptionalText("original_grant_id", target.OriginalGrantID, 128); err != nil {
		return err
	}
	if err := validateOptionalText("grant_namespace", target.GrantNamespace, 256); err != nil {
		return err
	}
	if err := validateOptionalDigest("grant_key_digest", target.GrantKeyDigest); err != nil {
		return err
	}
	if err := validateOptionalDigest("grant_request_digest", target.GrantRequestDigest); err != nil {
		return err
	}
	if err := validateOptionalText("wait_namespace", target.WaitNamespace, 256); err != nil {
		return err
	}
	if err := validateOptionalDigest("wait_key_digest", target.WaitKeyDigest); err != nil {
		return err
	}
	if err := validateOptionalDigest("wait_request_digest", target.WaitRequestDigest); err != nil {
		return err
	}
	if index < 1 || index > int(MaxTargetCount) {
		return invalidOperation("target index is out of range")
	}
	return nil
}

// validateShape 校验目标转换请求的 CAS、状态和结果字段。
func (request TargetTransitionRequest) validateShape() error {
	if err := validateTransitionIdentity(request); err != nil {
		return err
	}
	if err := validateTransitionPredecessor(request); err != nil {
		return err
	}
	if err := validateTransitionRecordDigest(request); err != nil {
		return err
	}
	if err := validateTransitionNext(request); err != nil {
		return err
	}
	if err := validateTransitionStatuses(request); err != nil {
		return err
	}
	if err := validateTransitionOutcome(request); err != nil {
		return err
	}
	if !request.RecordedAt.IsZero() || request.ResolvedAt != nil {
		return invalidOperation("recorded_at and resolved_at are owner-controlled")
	}
	return nil
}

func validateTransitionOutcome(request TargetTransitionRequest) error {
	if err := validateOptionalID("resolved_grant_id", request.ResolvedGrantID); err != nil {
		return err
	}
	if err := validateOptionalID("resolved_wait_handle_id", request.ResolvedWaitHandleID); err != nil {
		return err
	}
	if err := validateText("error_code", request.ErrorCode, 256, true); err != nil {
		return err
	}
	if err := validateText("error_ref", request.ErrorRef, 512, true); err != nil {
		return err
	}
	return validateClearedTransition(request)
}

func validateClearedTransition(request TargetTransitionRequest) error {
	if request.NextPhase != "cleanup_cleared" {
		return nil
	}
	if err := validateClearedGrant(request); err != nil {
		return err
	}
	return validateClearedWait(request)
}

// validateClearedGrant 校验 cleanup_cleared 的 grant 终态或空态。
func validateClearedGrant(request TargetTransitionRequest) error {
	if request.ResolvedGrantID == nil {
		if (request.GrantStatus == "absent" || request.GrantStatus == "planned") && request.GrantRevision == 0 {
			return nil
		}
		return invalidOperation("cleanup_cleared grant state is not empty")
	}
	if (request.GrantStatus == "revoked" || request.GrantStatus == "expired") && request.GrantRevision > 0 {
		return nil
	}
	return invalidOperation("cleanup_cleared grant is not terminal")
}

// validateClearedWait 校验 cleanup_cleared 的 wait handle 终态或空态。
func validateClearedWait(request TargetTransitionRequest) error {
	if request.ResolvedWaitHandleID == nil {
		if request.WaitStatus == "absent" && request.WaitRevision == 0 {
			return nil
		}
		return invalidOperation("cleanup_cleared wait state is not empty")
	}
	if isTerminalWaitStatus(request.WaitStatus) && request.WaitRevision > 0 {
		return nil
	}
	return invalidOperation("cleanup_cleared wait is not terminal")
}

func validateTransitionPredecessor(request TargetTransitionRequest) error {
	if request.ExpectedVersion <= 0 {
		return invalidOperation("expected target version must be positive")
	}
	if err := validateTargetPhase(request.ExpectedPhase); err != nil {
		return invalidOperation("expected phase: %v", err)
	}
	return validateDigest("expected_record_digest", request.ExpectedRecordDigest)
}

func validateTransitionNext(request TargetTransitionRequest) error {
	if err := validateTargetPhase(request.NextPhase); err != nil {
		return invalidOperation("next phase: %v", err)
	}
	if !validUncertainPhase(request.UncertainPhase) {
		return invalidOperation("uncertain phase is unsupported")
	}
	return nil
}

func validateTransitionIdentity(request TargetTransitionRequest) error {
	if err := validateText("operation_key", request.OperationKey, MaxOperationKey, false); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", request.OperationDigest); err != nil {
		return err
	}
	return validateText("target_key", request.TargetKey, MaxTargetKey, false)
}

func validateTransitionRecordDigest(request TargetTransitionRequest) error {
	if request.RecordDigest == "" {
		return nil
	}
	if err := validateDigest("record_digest", request.RecordDigest); err != nil {
		return err
	}
	if request.RecordDigest == request.ExpectedRecordDigest {
		return invalidOperation("record_digest must differ from predecessor")
	}
	return nil
}

// validateTransitionStatuses 校验 grant/wait 状态、版本和清理状态。
func validateTransitionStatuses(request TargetTransitionRequest) error {
	if !validGrantStatus(request.GrantStatus) || !validWaitStatus(request.WaitStatus) {
		return invalidOperation("grant or wait status is unsupported")
	}
	if request.GrantRevision < 0 || request.WaitRevision < 0 {
		return invalidOperation("grant and wait revisions cannot be negative")
	}
	if !validCleanupState(request.GrantCleanupState) || !validCleanupState(request.WaitCleanupState) {
		return invalidOperation("cleanup state is unsupported")
	}
	return nil
}

func validateTargetPhase(phase string) error {
	switch phase {
	case "planned", "grant_pending", "wait_pending", "waiting", "satisfied", "timed_out", "cancelled", "failed", "outcome_unknown", "cleanup_pending", "cleanup_cleared":
		return nil
	default:
		return errors.New("phase is unsupported")
	}
}

func validTargetPhase(phase string) bool { return validateTargetPhase(phase) == nil }

func validUncertainPhase(phase string) bool {
	switch phase {
	case "", "operation_create", "target_transition", "grant_create", "wait_create", "cleanup":
		return true
	default:
		return false
	}
}

func validGrantStatus(status string) bool {
	switch status {
	case "absent", "planned", "active", "revoked", "expired", "unknown":
		return true
	default:
		return false
	}
}

func validWaitStatus(status string) bool {
	switch status {
	case "absent", "planned", "pending", "satisfied", "timed_out", "cancelled", "revoked", "expired", "unknown":
		return true
	default:
		return false
	}
}

func validCleanupState(state string) bool {
	return state == "none" || state == "pending" || state == "cleared"
}

// validateText 校验边界、UTF-8、空字节和首尾空白约束。
func validateText(name, value string, limit int, allowEmpty bool) error {
	if (!allowEmpty && value == "") || len(value) > limit || strings.TrimSpace(value) != value || strings.IndexByte(value, 0) >= 0 || !utf8.ValidString(value) {
		return invalidOperation("%s is not valid bounded text", name)
	}
	return nil
}

func validateOptionalText(name, value string, limit int) error {
	if value == "" {
		return nil
	}
	return validateText(name, value, limit, false)
}

func validateDigest(name, value string) error {
	if len(value) != sha256.Size*2 || value != strings.ToLower(value) {
		return invalidOperation("%s must be a lowercase SHA-256 digest", name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return invalidOperation("%s must be a lowercase SHA-256 digest", name)
	}
	return nil
}

func validateOptionalDigest(name, value string) error {
	if value == "" {
		return nil
	}
	return validateDigest(name, value)
}

func validateOptionalID(name string, value *string) error {
	if value == nil {
		return nil
	}
	return validateText(name, *value, 128, false)
}

func invalidOperation(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidOperation, fmt.Sprintf(format, args...))
}
