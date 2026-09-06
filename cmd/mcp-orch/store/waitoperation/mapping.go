package waitoperation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

func operationFromRow(row storedb.OrchestrationWaitOperation) (Operation, error) {
	if err := validateOperationRow(row); err != nil {
		return Operation{}, err
	}
	source, err := operationSourceFromRow(row)
	if err != nil {
		return Operation{}, err
	}
	issued, deadline, created, updated, resolved, err := operationTimesFromRow(row)
	if err != nil {
		return Operation{}, err
	}
	return Operation{
		OperationKey: row.OperationKey, OperationDigest: row.OperationDigest, TargetSetDigest: row.TargetSetDigest,
		CallerPrincipalRef: row.CallerPrincipalRef, ScopeRef: row.ScopeRef, Source: source,
		IssuedAt: issued, DeadlineAt: deadline, TargetCount: row.TargetCount, Revision: row.Revision,
		Status: row.Status, UncertainPhase: row.UncertainPhase, CleanupRequestDigest: row.CleanupRequestDigest,
		CleanupReasonRef: row.CleanupReasonRef, ResolvedAt: resolved, CreatedAt: created, UpdatedAt: updated,
		SchemaVersion: row.SchemaVersion,
	}, nil
}

// validateOperationRow 校验持久化操作行的版本、身份和生命周期字段。
func validateOperationRow(row storedb.OrchestrationWaitOperation) error {
	if row.SchemaVersion != SchemaVersion || row.Revision <= 0 || row.TargetCount < 1 || row.TargetCount > MaxTargetCount {
		return fmt.Errorf("%w: stored operation version or count is invalid", ErrInvalidOperation)
	}
	if !validOperationStatus(row.Status) || !validUncertainPhase(row.UncertainPhase) {
		return fmt.Errorf("%w: stored operation status is invalid", ErrInvalidOperation)
	}
	if err := validateDigest("operation_digest", row.OperationDigest); err != nil {
		return err
	}
	if err := validateDigest("target_set_digest", row.TargetSetDigest); err != nil {
		return err
	}
	if err := validateOperationIdentity(row); err != nil {
		return fmt.Errorf("%w: stored operation identity is incomplete", ErrInvalidOperation)
	}
	return validateOperationResolution(row)
}

func validateOperationIdentity(row storedb.OrchestrationWaitOperation) error {
	if row.OperationKey == "" || row.CallerPrincipalRef == "" || row.ScopeRef == "" {
		return errors.New("identity incomplete")
	}
	return nil
}

func validateOperationResolution(row storedb.OrchestrationWaitOperation) error {
	if row.Status == "outcome_unknown" && row.ResolvedAt != "" {
		return fmt.Errorf("%w: outcome_unknown operation is resolved", ErrInvalidOperation)
	}
	return nil
}

func operationSourceFromRow(row storedb.OrchestrationWaitOperation) (accesswait.FenceSnapshot, error) {
	source := accesswait.FenceSnapshot{AgentID: row.SourceAgentID, ReservationID: row.SourceReservationID, ReservationGeneration: row.SourceReservationGeneration, IncarnationID: row.SourceIncarnationID, IncarnationGeneration: row.SourceIncarnationGeneration, BindingVersion: row.SourceBindingVersion, BindingDigest: row.SourceBindingDigest, SourceFenceRef: row.SourceFenceRef}
	if err := source.ValidateBound(); err != nil {
		return accesswait.FenceSnapshot{}, fmt.Errorf("%w: stored source fence: %v", ErrInvalidOperation, err)
	}
	return source, nil
}

// operationTimesFromRow 解析并校验操作行中的规范 UTC 时间戳。
func operationTimesFromRow(row storedb.OrchestrationWaitOperation) (time.Time, time.Time, time.Time, time.Time, *time.Time, error) {
	issued, err := parseUTC("issued_at", row.IssuedAt)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, nil, err
	}
	deadline, err := parseUTC("deadline_at", row.DeadlineAt)
	if err != nil || !deadline.After(issued) {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, nil, fmt.Errorf("%w: stored deadline_at is invalid", ErrInvalidOperation)
	}
	created, err := parseUTC("created_at", row.CreatedAt)
	if err != nil {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, nil, err
	}
	updated, err := parseUTC("updated_at", row.UpdatedAt)
	if err != nil || updated.Before(created) {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, nil, fmt.Errorf("%w: stored updated_at is invalid", ErrInvalidOperation)
	}
	resolved, err := parseOptionalUTC("resolved_at", row.ResolvedAt)
	return issued, deadline, created, updated, resolved, err
}

// targetFromRow 将目标日志行映射为经过完整完整性校验的目标状态。
func targetFromRow(row storedb.OrchestrationWaitOperationTargetJournal) (TargetState, error) {
	if err := validateTargetRowBasics(row); err != nil {
		return TargetState{}, err
	}
	if err := validateTargetRowDigests(row); err != nil {
		return TargetState{}, err
	}
	if err := validateText("target_key", row.TargetKey, MaxTargetKey, false); err != nil {
		return TargetState{}, err
	}
	target, err := targetSnapshotFromRow(row)
	if err != nil {
		return TargetState{}, err
	}
	selector, err := selectorFromRow(row)
	if err != nil {
		return TargetState{}, err
	}
	if err := validateTargetCanonical(row, target, selector); err != nil {
		return TargetState{}, err
	}
	deadline, recorded, resolved, err := parseTargetTimes(row)
	if err != nil {
		return TargetState{}, err
	}
	return TargetState{
		OperationKey: row.OperationKey, OperationDigest: row.OperationDigest, Key: row.TargetKey,
		TargetIndex: row.TargetIndex, Version: row.Version, TargetDigest: row.TargetDigest,
		RecordDigest: row.RecordDigest, PreviousRecordDigest: row.PreviousRecordDigest, Target: target,
		Selector: selector, AfterSequence: row.AfterSourceSequence, AfterCursor: row.AfterCursor,
		DeadlineAt: deadline, OriginalGrantID: row.OriginalGrantID, GrantNamespace: row.GrantNamespace,
		GrantKeyDigest: row.GrantKeyDigest, GrantRequestDigest: row.GrantRequestDigest,
		ResolvedGrantID: cloneString(row.ResolvedGrantID), GrantRevision: row.GrantRevision, GrantStatus: row.GrantStatus,
		WaitNamespace: row.WaitNamespace, WaitKeyDigest: row.WaitKeyDigest, WaitRequestDigest: row.WaitRequestDigest,
		ResolvedWaitHandleID: cloneString(row.ResolvedWaitHandleID), WaitRevision: row.WaitRevision, WaitStatus: row.WaitStatus,
		GrantCleanupState: row.GrantCleanupState, WaitCleanupState: row.WaitCleanupState, Phase: row.Phase,
		UncertainPhase: row.UncertainPhase, ErrorCode: row.ErrorCode, ErrorRef: row.ErrorRef, ResolvedAt: resolved,
		RecordedAt: recorded, SchemaVersion: row.SchemaVersion,
	}, nil
}

func validateTargetRowBasics(row storedb.OrchestrationWaitOperationTargetJournal) error {
	if row.SchemaVersion != SchemaVersion || row.Version <= 0 || row.TargetIndex <= 0 || row.TargetIndex > MaxTargetCount {
		return fmt.Errorf("%w: stored target version or index is invalid", ErrInvalidOperation)
	}
	return nil
}

func validateTargetRowDigests(row storedb.OrchestrationWaitOperationTargetJournal) error {
	for name, value := range map[string]string{
		"target_digest": row.TargetDigest, "record_digest": row.RecordDigest,
		"grant_key_digest": row.GrantKeyDigest, "grant_request_digest": row.GrantRequestDigest,
		"wait_key_digest": row.WaitKeyDigest, "wait_request_digest": row.WaitRequestDigest,
	} {
		if err := validateDigest(name, value); err != nil {
			return err
		}
	}
	return nil
}

func targetSnapshotFromRow(row storedb.OrchestrationWaitOperationTargetJournal) (accesswait.FenceSnapshot, error) {
	target := accesswait.FenceSnapshot{
		AgentID: row.TargetAgentID, ReservationID: row.TargetReservationID,
		ReservationGeneration: row.TargetReservationGeneration, IncarnationID: row.TargetIncarnationID,
		IncarnationGeneration: row.TargetIncarnationGeneration, BindingVersion: row.TargetBindingVersion,
		BindingDigest: row.TargetBindingDigest, SourceFenceRef: row.TargetFenceRef,
	}
	if err := target.ValidateBound(); err != nil {
		return accesswait.FenceSnapshot{}, fmt.Errorf("%w: stored target fence: %v", ErrInvalidOperation, err)
	}
	return target, nil
}

// validateTargetCanonical 校验目标摘要、生命周期和游标的规范表示。
func validateTargetCanonical(row storedb.OrchestrationWaitOperationTargetJournal, target accesswait.FenceSnapshot, selector accesswait.EventSelector) error {
	expectedTargetDigest, err := computeTargetDigest(row.TargetKey, target, selector, row.AfterSourceSequence, row.AfterCursor)
	if err != nil || expectedTargetDigest != row.TargetDigest {
		return fmt.Errorf("%w: stored target digest is not canonical", ErrInvalidOperation)
	}
	if row.Version == 1 && row.RecordDigest != initialTargetRecordDigest(row.OperationKey, row.OperationDigest, row.TargetDigest) {
		return fmt.Errorf("%w: stored initial target record digest is not canonical", ErrInvalidOperation)
	}
	if err := validateTargetLifecycle(row); err != nil {
		return err
	}
	return validateTargetCursor(row)
}

// validateTargetLifecycle 校验目标阶段及 grant/wait 清理状态。
func validateTargetLifecycle(row storedb.OrchestrationWaitOperationTargetJournal) error {
	if !validTargetPhase(row.Phase) || !validUncertainPhase(row.UncertainPhase) || !validGrantStatus(row.GrantStatus) {
		return fmt.Errorf("%w: stored target lifecycle state is invalid", ErrInvalidOperation)
	}
	if !validWaitStatus(row.WaitStatus) || !validCleanupState(row.GrantCleanupState) || !validCleanupState(row.WaitCleanupState) {
		return fmt.Errorf("%w: stored target lifecycle state is invalid", ErrInvalidOperation)
	}
	if row.Phase == "cleanup_cleared" && !validClearedResources(row) {
		return fmt.Errorf("%w: cleanup_cleared target still has live resources", ErrInvalidOperation)
	}
	return nil
}

// validClearedResources 校验 cleanup_cleared head 没有遗留 live resource。
func validClearedResources(row storedb.OrchestrationWaitOperationTargetJournal) bool {
	return validClearedGrant(row) && validClearedWait(row)
}

// validClearedGrant 校验 grant 引用为空或已进入 revoked/expired 终态。
func validClearedGrant(row storedb.OrchestrationWaitOperationTargetJournal) bool {
	if row.ResolvedGrantID == nil {
		return (row.GrantStatus == "planned" || row.GrantStatus == "absent") && row.GrantRevision == 0
	}
	return (row.GrantStatus == "revoked" || row.GrantStatus == "expired") && row.GrantRevision > 0
}

// validClearedWait 校验 wait 引用为空或已进入不可变终态。
func validClearedWait(row storedb.OrchestrationWaitOperationTargetJournal) bool {
	if row.ResolvedWaitHandleID == nil {
		return row.WaitStatus == "absent" && row.WaitRevision == 0
	}
	return isTerminalWaitStatus(row.WaitStatus) && row.WaitRevision > 0
}

func isTerminalWaitStatus(status string) bool {
	switch status {
	case "satisfied", "timed_out", "cancelled", "revoked", "expired":
		return true
	default:
		return false
	}
}

// validateTargetCursor 校验目标源序列与规范游标的一致性。
func validateTargetCursor(row storedb.OrchestrationWaitOperationTargetJournal) error {
	if row.AfterSourceSequence < 0 || (row.AfterSourceSequence == 0 && row.AfterCursor != "") || (row.AfterSourceSequence > 0 && row.AfterCursor == "") {
		return fmt.Errorf("%w: stored target cursor is invalid", ErrInvalidOperation)
	}
	if row.AfterSourceSequence == 0 {
		return nil
	}
	cursor, err := eventstore.CanonicalCursor(row.SourceID, row.SourceEpoch, eventstore.Lane(row.Lane), row.AfterSourceSequence)
	if err != nil || cursor != row.AfterCursor {
		return fmt.Errorf("%w: stored target cursor is not canonical", ErrInvalidOperation)
	}
	return nil
}

func parseTargetTimes(row storedb.OrchestrationWaitOperationTargetJournal) (time.Time, time.Time, *time.Time, error) {
	deadline, err := parseUTC("target deadline_at", row.DeadlineAt)
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	recorded, err := parseUTC("target recorded_at", row.RecordedAt)
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	resolved, err := parseOptionalUTC("target resolved_at", row.ResolvedAt)
	return deadline, recorded, resolved, err
}

// selectorFromRow 解码并校验目标选择器的列映射和摘要。
func selectorFromRow(row storedb.OrchestrationWaitOperationTargetJournal) (accesswait.EventSelector, error) {
	var selector accesswait.EventSelector
	if err := decodeStrictJSON([]byte(row.SelectorJson), &selector); err != nil {
		return accesswait.EventSelector{}, fmt.Errorf("%w: decode target selector: %v", ErrInvalidOperation, err)
	}
	var eventTypes []string
	if err := decodeStrictJSON([]byte(row.EventTypesJson), &eventTypes); err != nil {
		return accesswait.EventSelector{}, fmt.Errorf("%w: decode target event types: %v", ErrInvalidOperation, err)
	}
	selector.EventTypes = eventTypes
	if err := selector.Validate(); err != nil {
		return accesswait.EventSelector{}, fmt.Errorf("%w: target selector: %v", ErrInvalidOperation, err)
	}
	if err := validateSelectorColumns(selector, row); err != nil {
		return accesswait.EventSelector{}, err
	}
	if err := validateSelectorCanonical(selector, row); err != nil {
		return accesswait.EventSelector{}, err
	}
	return selector, nil
}

// validateSelectorColumns 校验选择器字段与日志列的一致性。
func validateSelectorColumns(selector accesswait.EventSelector, row storedb.OrchestrationWaitOperationTargetJournal) error {
	if selector.Version != row.SelectorVersion || selector.SourceID != row.SourceID || selector.SourceEpoch != row.SourceEpoch {
		return fmt.Errorf("%w: target selector columns disagree", ErrInvalidOperation)
	}
	if string(selector.Lane) != row.Lane || selector.TargetAgentID != row.TargetAgentID {
		return fmt.Errorf("%w: target selector columns disagree", ErrInvalidOperation)
	}
	return nil
}

// validateSelectorCanonical 校验选择器 JSON、事件列表和摘要的规范编码。
func validateSelectorCanonical(selector accesswait.EventSelector, row storedb.OrchestrationWaitOperationTargetJournal) error {
	canonical, err := selectorStorageJSON(selector)
	if err != nil || string(canonical) != row.SelectorJson {
		return fmt.Errorf("%w: target selector JSON is not canonical", ErrInvalidOperation)
	}
	digest, err := selector.Digest()
	if err != nil || digest != row.SelectorDigest {
		return fmt.Errorf("%w: target selector digest is not canonical", ErrInvalidOperation)
	}
	eventTypesJSON, err := json.Marshal(selector.EventTypes)
	if err != nil || string(eventTypesJSON) != row.EventTypesJson {
		return fmt.Errorf("%w: target event types JSON is not canonical", ErrInvalidOperation)
	}
	return nil
}

func selectorStorageJSON(selector accesswait.EventSelector) ([]byte, error) {
	copySelector := selector
	copySelector.EventTypes = nil
	return json.Marshal(copySelector)
}

func decodeStrictJSON(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func parseUTC(name, value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil || parsed.IsZero() || parsed.Location() != time.UTC || parsed.UTC().Format(time.RFC3339Nano) != value {
		return time.Time{}, fmt.Errorf("%w: %s is not canonical UTC", ErrInvalidOperation, name)
	}
	return parsed.UTC(), nil
}

func parseOptionalUTC(name, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	parsed, err := parseUTC(name, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func transitionRequestFromRow(row storedb.OrchestrationWaitOperationTargetJournal) TargetTransitionRequest {
	return TargetTransitionRequest{
		OperationKey: row.OperationKey, OperationDigest: row.OperationDigest, TargetKey: row.TargetKey,
		ExpectedVersion: row.Version - 1, ExpectedPhase: "", ExpectedRecordDigest: row.PreviousRecordDigest,
		RecordDigest: row.RecordDigest, ResolvedGrantID: cloneString(row.ResolvedGrantID), GrantRevision: row.GrantRevision,
		GrantStatus: row.GrantStatus, ResolvedWaitHandleID: cloneString(row.ResolvedWaitHandleID), WaitRevision: row.WaitRevision,
		WaitStatus: row.WaitStatus, GrantCleanupState: row.GrantCleanupState, WaitCleanupState: row.WaitCleanupState,
		NextPhase: row.Phase, UncertainPhase: row.UncertainPhase, ErrorCode: row.ErrorCode, ErrorRef: row.ErrorRef,
	}
}

func validOperationStatus(status string) bool {
	switch status {
	case "provisioning", "ready", "partial", "outcome_unknown", "cleanup_pending", "cleanup_cleared":
		return true
	default:
		return false
	}
}
