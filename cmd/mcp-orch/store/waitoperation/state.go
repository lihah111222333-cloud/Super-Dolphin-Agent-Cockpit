package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// AdvanceState 执行一次带 fence 的 operation-head CAS，时间戳始终由 owner clock 生成。
func (s *Store) AdvanceState(ctx context.Context, request AdvanceStateRequest) (AdvanceStateResult, error) {
	if err := s.ready(ctx, false); err != nil {
		return Operation{}, err
	}
	if err := validateOperationStateRequest(request); err != nil {
		return Operation{}, err
	}
	now, err := s.nowUTC()
	if err != nil {
		return Operation{}, err
	}
	if err := s.CheckSchema(ctx); err != nil {
		return Operation{}, err
	}
	var result Operation
	if s.commitMarkers == nil {
		return s.advanceStateWithoutMarker(ctx, request, now, &result)
	}
	return s.advanceStateWithMarker(ctx, request, now, &result)
}

func (s *Store) advanceStateWithoutMarker(ctx context.Context, request AdvanceStateRequest, now time.Time, result *Operation) (Operation, error) {
	err := s.withImmediateTx(ctx, func(q *storedb.Queries, _ storedb.DBTX) error {
		return s.advanceStateTx(ctx, q, request, now, result)
	})
	return *result, err
}

// advanceStateWithMarker 以 durable marker 保护 operation-head 状态 CAS。
func (s *Store) advanceStateWithMarker(ctx context.Context, request AdvanceStateRequest, now time.Time, result *Operation) (Operation, error) {
	predecessorRow, err := loadOperationStateRow(ctx, s.queries, request)
	if err != nil {
		return Operation{}, err
	}
	predecessor, err := operationFromRow(predecessorRow)
	if err != nil {
		return Operation{}, err
	}
	descriptor, err := NewMutationDescriptor(MutationStateAdvance, request.OperationKey, request.OperationDigest, "", request, operationStateDigest(predecessor))
	if err != nil {
		return Operation{}, err
	}
	marker, markerErr := s.commitMarkers.load(ctx, descriptor.MarkerKey())
	if markerErr == nil && marker.State == "resolved" {
		return rejectResolvedStateMarker(marker, descriptor)
	}
	if markerErr != nil && !errors.Is(markerErr, ErrOperationNotFound) {
		return Operation{}, markerErr
	}
	err = s.withImmediateMutation(ctx, &descriptor, func(q *storedb.Queries, _ storedb.DBTX) error {
		if err := s.advanceStateTx(ctx, q, request, now, result, descriptor.PreStateDigest); err != nil {
			return err
		}
		descriptor.PostStateDigest = operationStateDigest(*result)
		return nil
	})
	if errors.Is(err, ErrCommitMarkerResolved) {
		return Operation{}, ErrOperationStateConflict
	}
	return *result, err
}

func rejectResolvedStateMarker(marker CommitMarker, descriptor CommitMarkerDescriptor) (Operation, error) {
	if !markerMatchesDescriptor(marker, descriptor) {
		return Operation{}, ErrCommitMarkerConflict
	}
	return Operation{}, ErrOperationStateConflict
}

// UpdateState 是 AdvanceState 的 owner-local 兼容别名。
func (s *Store) UpdateState(ctx context.Context, request UpdateStateRequest) (UpdateStateResult, error) {
	return s.AdvanceState(ctx, request)
}

// validateOperationStateRequest 校验状态 CAS 的身份、两端状态和 cleanup 摘要。
func validateOperationStateRequest(request OperationStateRequest) error {
	if err := validateStateIdentity(request); err != nil {
		return err
	}
	if request.ExpectedRevision <= 0 {
		return invalidOperation("expected operation revision must be positive")
	}
	if err := validateStateEndpoints(request); err != nil {
		return err
	}
	if err := validateStateTransition(request); err != nil {
		return err
	}
	return validateStateCleanupRequest(request)
}

func validateStateIdentity(request OperationStateRequest) error {
	if err := validateText("operation_key", request.OperationKey, MaxOperationKey, false); err != nil {
		return err
	}
	return validateDigest("operation_digest", request.OperationDigest)
}

func validateStateEndpoints(request OperationStateRequest) error {
	if err := validateOperationStateFields("expected", request.ExpectedStatus, request.ExpectedUncertainPhase, request.ExpectedCleanupRequestDigest, request.ExpectedCleanupReasonRef, request.ExpectedResolvedAt, false); err != nil {
		return err
	}
	return validateOperationStateFields("next", request.NextStatus, request.NextUncertainPhase, request.NextCleanupRequestDigest, request.NextCleanupReasonRef, "", true)
}

func validateStateTransition(request OperationStateRequest) error {
	if request.ExpectedStatus == request.NextStatus {
		return invalidOperation("operation state transition must advance status")
	}
	if !validOperationTransition(request.ExpectedStatus, request.ExpectedUncertainPhase, request.NextStatus) {
		return invalidOperation("operation state transition is not allowed")
	}
	return nil
}

// validateStateCleanupRequest 校验 cleanup transition 的 canonical reason/digest 与连续性。
func validateStateCleanupRequest(request OperationStateRequest) error {
	if !requiresCleanupMetadata(request.NextStatus, request.NextUncertainPhase) {
		return nil
	}
	if request.ExpectedStatus == "outcome_unknown" && request.ExpectedUncertainPhase == "cleanup" && request.NextStatus == "cleanup_pending" && (request.NextCleanupRequestDigest != request.ExpectedCleanupRequestDigest || request.NextCleanupReasonRef != request.ExpectedCleanupReasonRef) {
		return errors.Join(ErrReplayConflict, invalidOperation("cleanup metadata must be preserved"))
	}
	if request.NextCleanupReasonRef == "" {
		return invalidOperation("cleanup reason is required")
	}
	want := cleanupRequestDigest(request.OperationKey, request.OperationDigest, request.NextCleanupReasonRef)
	if request.NextCleanupRequestDigest != want {
		return errors.Join(ErrReplayConflict, invalidOperation("cleanup request digest is not canonical"))
	}
	return nil
}

func requiresCleanupMetadata(status, uncertain string) bool {
	return status == "cleanup_pending" || status == "cleanup_cleared" || (status == "outcome_unknown" && uncertain == "cleanup")
}

// validateOperationStateFields 校验状态字段的格式、时间和 one-hot metadata。
func validateOperationStateFields(prefix, status, uncertain, cleanupDigest, cleanupReason, resolved string, allowDerivedResolved bool) error {
	if err := validateStateFieldSyntax(prefix, status, uncertain, cleanupDigest, cleanupReason, resolved); err != nil {
		return err
	}
	return validateStateFieldShape(prefix, status, uncertain, cleanupDigest, cleanupReason, resolved, allowDerivedResolved)
}

// validateStateFieldSyntax 校验状态枚举、摘要文本和规范 UTC 时间。
func validateStateFieldSyntax(prefix, status, uncertain, cleanupDigest, cleanupReason, resolved string) error {
	if !validOperationStatus(status) {
		return invalidOperation("%s status is unsupported", prefix)
	}
	if !validUncertainPhase(uncertain) {
		return invalidOperation("%s uncertain phase is unsupported", prefix)
	}
	if err := validateOptionalDigest(prefix+" cleanup_request_digest", cleanupDigest); err != nil {
		return err
	}
	if err := validateOptionalText(prefix+" cleanup_reason_ref", cleanupReason, 512); err != nil {
		return err
	}
	if resolved != "" {
		_, err := parseUTC(prefix+" resolved_at", resolved)
		return err
	}
	return nil
}

// validateStateFieldShape 校验不同 operation 状态的 one-hot metadata 形状。
func validateStateFieldShape(prefix, status, uncertain, cleanupDigest, cleanupReason, resolved string, allowDerivedResolved bool) error {
	switch status {
	case "outcome_unknown":
		return validateUnknownStateShape(uncertain, cleanupDigest, cleanupReason, resolved)
	case "cleanup_pending":
		return validateCleanupPendingShape(uncertain, cleanupDigest, cleanupReason, resolved)
	case "cleanup_cleared":
		return validateCleanupClearedShape(uncertain, cleanupDigest, cleanupReason, resolved, allowDerivedResolved)
	case "provisioning", "ready", "partial":
		return validateStableStateShape(prefix, uncertain, cleanupDigest, cleanupReason, resolved)
	}
	return nil
}

func validateCleanupPendingShape(uncertain, cleanupDigest, cleanupReason, resolved string) error {
	if uncertain == "cleanup" && cleanupDigest != "" && cleanupReason != "" && resolved == "" {
		return nil
	}
	return invalidOperation("cleanup_pending metadata is inconsistent")
}

// validateCleanupClearedShape 校验 cleanup_cleared 的摘要与 owner 派生时间字段。
func validateCleanupClearedShape(uncertain, cleanupDigest, cleanupReason, resolved string, allowDerivedResolved bool) error {
	if uncertain != "cleanup" || cleanupDigest == "" || cleanupReason == "" {
		return invalidOperation("cleanup_cleared metadata is inconsistent")
	}
	if allowDerivedResolved || resolved != "" {
		return nil
	}
	return invalidOperation("cleanup_cleared metadata is inconsistent")
}

func validateStableStateShape(prefix, uncertain, cleanupDigest, cleanupReason, resolved string) error {
	if uncertain == "" && cleanupDigest == "" && cleanupReason == "" && resolved == "" {
		return nil
	}
	return invalidOperation("%s state must not carry uncertainty or cleanup metadata", prefix)
}

// validateUnknownStateShape 校验 outcome_unknown 的 cleanup/non-cleanup metadata 分支。
func validateUnknownStateShape(uncertain, cleanupDigest, cleanupReason, resolved string) error {
	if uncertain == "" {
		return invalidOperation("outcome_unknown uncertain phase is required")
	}
	if uncertain != "cleanup" && (cleanupDigest != "" || cleanupReason != "" || resolved != "") {
		return invalidOperation("non-cleanup outcome_unknown must not carry cleanup metadata")
	}
	if uncertain == "cleanup" && (cleanupDigest == "" || cleanupReason == "" || resolved != "") {
		return invalidOperation("cleanup outcome_unknown metadata is inconsistent")
	}
	return nil
}

// validOperationTransition 判断 operation 状态是否沿允许的单调边前进。
func validOperationTransition(previous, previousUncertain, next string) bool {
	if previous == "outcome_unknown" && previousUncertain == "cleanup" {
		return next == "cleanup_pending"
	}
	allowed := map[string]struct{}{
		"provisioning\x00ready": {}, "provisioning\x00partial": {}, "provisioning\x00outcome_unknown": {}, "provisioning\x00cleanup_pending": {},
		"partial\x00ready": {}, "partial\x00outcome_unknown": {}, "partial\x00cleanup_pending": {},
		"ready\x00outcome_unknown": {}, "ready\x00cleanup_pending": {},
		"outcome_unknown\x00provisioning": {}, "outcome_unknown\x00partial": {}, "outcome_unknown\x00ready": {}, "outcome_unknown\x00cleanup_pending": {},
		"cleanup_pending\x00cleanup_cleared": {},
	}
	_, ok := allowed[previous+"\x00"+next]
	return ok
}

// advanceStateTx 读取 operation head，区分精确重放与一次真实状态 CAS。
func (s *Store) advanceStateTx(ctx context.Context, q *storedb.Queries, request OperationStateRequest, now time.Time, result *Operation, preStateDigests ...string) error {
	current, err := loadOperationStateRow(ctx, q, request)
	if err != nil {
		return err
	}
	if err := validateCurrentStateExpectation(current, request); err != nil {
		return err
	}
	if err := validateOperationStateDeadline(current, request, now); err != nil {
		return err
	}
	if len(preStateDigests) > 1 {
		return invalidOperation("advance state accepts at most one pre-state digest")
	}
	if len(preStateDigests) == 1 {
		operation, err := operationFromRow(current)
		if err != nil {
			return err
		}
		if operationStateDigest(operation) != preStateDigests[0] {
			return ErrOperationStateConflict
		}
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	return s.applyStateCAS(ctx, q, current, request, now, result)
}

// validateOperationStateDeadline prevents an expired operation from being
// advanced through provisioning or aggregate progress. Recording an explicit
// uncertainty or cleanup handoff remains allowed so the owner can reconcile
// already-issued effects without reopening the expired operation。
// validateOperationStateDeadline 校验 operation deadline 与允许的收口分支。
func validateOperationStateDeadline(row storedb.OrchestrationWaitOperation, request OperationStateRequest, now time.Time) error {
	deadline, err := parseUTC("operation deadline_at", row.DeadlineAt)
	if err != nil {
		return err
	}
	if now.Before(deadline) {
		return nil
	}
	if request.NextStatus == "outcome_unknown" || request.NextStatus == "cleanup_pending" || request.NextStatus == "cleanup_cleared" {
		return nil
	}
	return ErrDeadlineExpired
}

func loadOperationStateRow(ctx context.Context, q *storedb.Queries, request OperationStateRequest) (storedb.OrchestrationWaitOperation, error) {
	current, err := q.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: request.OperationKey})
	if errors.Is(err, sql.ErrNoRows) {
		return current, ErrOperationNotFound
	}
	if err != nil {
		return current, classifyDBError("read wait operation state", err, ErrOperationStateConflict)
	}
	if current.OperationDigest != request.OperationDigest {
		return current, ErrReplayConflict
	}
	return current, nil
}

// applyStateCAS 在证明 target 聚合后执行 operation-head 的 SQL CAS。
func (s *Store) applyStateCAS(ctx context.Context, q *storedb.Queries, current storedb.OrchestrationWaitOperation, request OperationStateRequest, now time.Time, result *Operation) error {
	if current.Revision != request.ExpectedRevision {
		return ErrOperationStateConflict
	}
	if err := s.proveOperationState(ctx, q, current, request); err != nil {
		return err
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	updatedAt := formatUTC(now)
	nextResolvedAt := ""
	if request.NextStatus == "cleanup_cleared" {
		nextResolvedAt = updatedAt
	}
	row, err := q.UpdateOrchestrationWaitOperationState(ctx, storedb.UpdateOrchestrationWaitOperationStateParams{
		NextStatus: request.NextStatus, NextUncertainPhase: request.NextUncertainPhase,
		NextCleanupRequestDigest: request.NextCleanupRequestDigest, NextCleanupReasonRef: request.NextCleanupReasonRef,
		NextResolvedAt: nextResolvedAt, UpdatedAt: updatedAt, OperationKey: request.OperationKey,
		OperationDigest: request.OperationDigest, ExpectedRevision: request.ExpectedRevision, ExpectedStatus: request.ExpectedStatus,
		ExpectedUncertainPhase: request.ExpectedUncertainPhase, ExpectedCleanupRequestDigest: request.ExpectedCleanupRequestDigest,
		ExpectedCleanupReasonRef: request.ExpectedCleanupReasonRef, ExpectedResolvedAt: request.ExpectedResolvedAt,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return s.classifyStateCAS(ctx, q, request, result)
	}
	if err != nil {
		return classifyDBError("update wait operation state", err, ErrOperationStateConflict)
	}
	return s.operationWithHeadsTx(ctx, q, row, result)
}

// validateCurrentStateExpectation 比对 CAS 请求的完整 predecessor 字段。
func validateCurrentStateExpectation(row storedb.OrchestrationWaitOperation, request OperationStateRequest) error {
	if row.Revision != request.ExpectedRevision || row.Status != request.ExpectedStatus || row.UncertainPhase != request.ExpectedUncertainPhase || row.CleanupRequestDigest != request.ExpectedCleanupRequestDigest || row.CleanupReasonRef != request.ExpectedCleanupReasonRef || row.ResolvedAt != request.ExpectedResolvedAt {
		return ErrOperationStateConflict
	}
	return nil
}

// classifyStateCAS 将数据库 CAS 失败分类为精确重放或真正冲突。
func (s *Store) classifyStateCAS(ctx context.Context, q *storedb.Queries, request OperationStateRequest, result *Operation) error {
	row, err := q.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: request.OperationKey})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrOperationNotFound
	}
	if err != nil {
		return classifyDBError("read wait operation state CAS conflict", err, ErrOperationStateConflict)
	}
	if row.OperationDigest != request.OperationDigest {
		return ErrReplayConflict
	}
	return ErrOperationStateConflict
}

// proveOperationState 在写入前验证 target 聚合头与 cleanup continuity。
func (s *Store) proveOperationState(ctx context.Context, q *storedb.Queries, row storedb.OrchestrationWaitOperation, request OperationStateRequest) error {
	operation, err := operationFromRow(row)
	if err != nil {
		return err
	}
	if err := s.populateHeads(ctx, q, &operation); err != nil {
		return err
	}
	switch request.NextStatus {
	case "ready":
		return proveReadyTargets(operation.Targets)
	case "cleanup_pending":
		return proveCleanupTargets(operation.Targets)
	case "cleanup_cleared":
		return proveCleanupCleared(operation.Targets, request)
	}
	return rejectCleanupUnknownReopen(request)
}

func proveReadyTargets(targets []TargetState) error {
	for _, target := range targets {
		switch target.Phase {
		case "waiting", "satisfied", "timed_out", "cancelled":
		default:
			return fmt.Errorf("%w: ready requires every latest target head to be terminal-ready", ErrOperationStateConflict)
		}
	}
	return nil
}

func proveCleanupTargets(targets []TargetState) error {
	for _, target := range targets {
		switch target.Phase {
		case "satisfied", "timed_out", "cancelled", "failed":
		default:
			return fmt.Errorf("%w: cleanup_pending requires every target to be terminal or failed", ErrOperationStateConflict)
		}
	}
	return nil
}

func proveCleanupCleared(targets []TargetState, request OperationStateRequest) error {
	for _, target := range targets {
		if target.Phase != "cleanup_cleared" {
			return fmt.Errorf("%w: cleanup_cleared requires every target cleanup_cleared", ErrOperationStateConflict)
		}
	}
	if request.NextCleanupRequestDigest != request.ExpectedCleanupRequestDigest || request.NextCleanupReasonRef != request.ExpectedCleanupReasonRef {
		return fmt.Errorf("%w: cleanup metadata must be preserved", ErrOperationStateConflict)
	}
	return nil
}

func rejectCleanupUnknownReopen(request OperationStateRequest) error {
	if request.ExpectedStatus == "outcome_unknown" && request.ExpectedUncertainPhase == "cleanup" && request.NextStatus == "provisioning" {
		return fmt.Errorf("%w: outcome_unknown cannot reopen provisioning", ErrOperationStateConflict)
	}
	return nil
}

func cleanupRequestDigest(operationKey, operationDigest, reason string) string {
	return digest("wait-operation/cleanup/v1", []byte(operationKey+"\x00"+operationDigest+"\x00"+reason))
}

// CanonicalCleanupRequestDigest 返回 operation owner 接受的唯一 cleanup 幂等摘要。
func CanonicalCleanupRequestDigest(operationKey, operationDigest, reason string) (string, error) {
	if err := validateText("operation_key", operationKey, MaxOperationKey, false); err != nil {
		return "", err
	}
	if err := validateDigest("operation_digest", operationDigest); err != nil {
		return "", err
	}
	if err := validateText("cleanup_reason_ref", reason, 512, false); err != nil {
		return "", err
	}
	return cleanupRequestDigest(operationKey, operationDigest, reason), nil
}
