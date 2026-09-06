package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// ProvisionTargetRequest identifies one planned target with an exact CAS
// predecessor. The owner derives all grant/wait fields from durable rows.
type ProvisionTargetRequest struct {
	OperationKey         string
	OperationDigest      string
	TargetKey            string
	ExpectedVersion      int64
	ExpectedPhase        string
	ExpectedRecordDigest string
}

// TargetProvisionRequest is a compatibility alias for callers that use the
// target-first naming convention.
type TargetProvisionRequest = ProvisionTargetRequest

// ProvisionTarget atomically provisions one target's grant and wait handle.
// A planned target appends grant-backed wait_pending and waiting facts in the
// same outer transaction. Existing waiting state is returned by exact replay;
// no owner call is retried after a commit-unknown result.
// ProvisionTarget 在单一事务中创建或精确重放目标的 grant 与 wait。
func (s *Store) ProvisionTarget(ctx context.Context, request ProvisionTargetRequest) (TargetState, error) {
	if err := s.ready(ctx, false); err != nil {
		return TargetState{}, err
	}
	if s.access == nil {
		return TargetState{}, ErrAccessOwnerRequired
	}
	if err := validateProvisionTargetRequest(request); err != nil {
		return TargetState{}, err
	}
	if err := s.CheckSchema(ctx); err != nil {
		return TargetState{}, err
	}
	recordedAt, err := s.nowUTC()
	if err != nil {
		return TargetState{}, err
	}
	var result TargetState
	if s.commitMarkers == nil {
		err = s.provisionTargetWithoutMarker(ctx, request, recordedAt, &result)
		return result, err
	}
	descriptor, result, err := s.provisionTargetWithMarker(ctx, request, recordedAt)
	if errors.Is(err, ErrCommitMarkerResolved) {
		err = s.replayResolvedTargetMarker(ctx, descriptor, &result)
	}
	return result, err
}

func (s *Store) provisionTargetWithoutMarker(ctx context.Context, request ProvisionTargetRequest, recordedAt time.Time, result *TargetState) error {
	return s.withImmediateTx(ctx, func(q *storedb.Queries, txdb storedb.DBTX) error {
		return s.provisionTargetTx(ctx, q, txdb, request, recordedAt, result)
	})
}

func (s *Store) provisionTargetWithMarker(ctx context.Context, request ProvisionTargetRequest, recordedAt time.Time) (CommitMarkerDescriptor, TargetState, error) {
	var result TargetState
	if replayed, replayErr := s.readProvisionReplay(ctx, request, &result); replayErr != nil {
		return CommitMarkerDescriptor{}, result, replayErr
	} else if replayed {
		descriptor, descriptorErr := s.provisionMutationDescriptor(ctx, request)
		if descriptorErr != nil {
			return CommitMarkerDescriptor{}, TargetState{}, descriptorErr
		}
		if err := s.reconcileReplayMarkers(ctx, request.OperationKey, request.OperationDigest, MutationTargetProvision, request.TargetKey, descriptor.RequestDigest, targetStateDigest(result)); err != nil {
			return CommitMarkerDescriptor{}, TargetState{}, err
		}
		return descriptor, result, nil
	}
	descriptor, err := s.provisionMutationDescriptor(ctx, request)
	if err != nil {
		return CommitMarkerDescriptor{}, TargetState{}, err
	}
	err = s.withImmediateMutation(ctx, &descriptor, func(q *storedb.Queries, txdb storedb.DBTX) error {
		return s.provisionTargetMarkerTx(ctx, q, txdb, request, recordedAt, &descriptor, &result)
	})
	return descriptor, result, err
}

func (s *Store) provisionTargetMarkerTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, request ProvisionTargetRequest, recordedAt time.Time, descriptor *CommitMarkerDescriptor, result *TargetState) error {
	_, current, err := loadProvisionTarget(ctx, q, request)
	if err != nil {
		return err
	}
	if replayed, replayErr := replayProvisionTarget(ctx, q, request, current, result); replayed || replayErr != nil {
		return replayErr
	}
	if targetStateDigest(current) != descriptor.PreStateDigest {
		return ErrTargetConflict
	}
	if err := s.provisionTargetTx(ctx, q, txdb, request, recordedAt, result); err != nil {
		return err
	}
	descriptor.PostStateDigest = targetStateDigest(*result)
	return nil
}

// provisionMutationDescriptor binds provisioning to the durable predecessor
// represented by the request, including exact historical replay predecessors.
func (s *Store) provisionMutationDescriptor(ctx context.Context, request ProvisionTargetRequest) (CommitMarkerDescriptor, error) {
	predecessor, err := s.loadProvisionPredecessor(ctx, request)
	if err != nil {
		return CommitMarkerDescriptor{}, err
	}
	return NewMutationDescriptor(MutationTargetProvision, request.OperationKey, request.OperationDigest, request.TargetKey, request, targetStateDigest(predecessor))
}

func (s *Store) readProvisionReplay(ctx context.Context, request ProvisionTargetRequest, result *TargetState) (bool, error) {
	current, err := loadProvisionTargetCurrent(ctx, s.queries, request)
	if err != nil {
		return false, err
	}
	return replayProvisionTarget(ctx, s.queries, request, current, result)
}

// loadProvisionPredecessor reads the exact durable target predecessor. When
// the head has advanced, the append-only journal supplies the replay row.
func (s *Store) loadProvisionPredecessor(ctx context.Context, request ProvisionTargetRequest) (TargetState, error) {
	_, head, err := loadProvisionTarget(ctx, s.queries, request)
	if err != nil {
		return TargetState{}, err
	}
	if head.Version == request.ExpectedVersion {
		if head.Phase != request.ExpectedPhase || head.RecordDigest != request.ExpectedRecordDigest {
			return TargetState{}, ErrTargetConflict
		}
		return head, nil
	}
	if head.Version < request.ExpectedVersion {
		return TargetState{}, ErrTargetConflict
	}
	return s.findProvisionPredecessorHistory(ctx, request)
}

func (s *Store) findProvisionPredecessorHistory(ctx context.Context, request ProvisionTargetRequest) (TargetState, error) {
	rows, err := s.queries.ListOrchestrationWaitTargetJournal(ctx, storedb.ListOrchestrationWaitTargetJournalParams{OperationKey: request.OperationKey})
	if err != nil {
		return TargetState{}, classifyDBError("read target predecessor history for provisioning marker", err, ErrTargetConflict)
	}
	for _, historical := range rows {
		if provisionPredecessorMatches(historical, request) {
			return targetFromRow(historical)
		}
	}
	return TargetState{}, ErrTargetConflict
}

func provisionPredecessorMatches(row storedb.OrchestrationWaitOperationTargetJournal, request ProvisionTargetRequest) bool {
	return row.OperationDigest == request.OperationDigest && row.TargetKey == request.TargetKey && row.Version == request.ExpectedVersion && row.Phase == request.ExpectedPhase && row.RecordDigest == request.ExpectedRecordDigest
}

func loadProvisionTargetCurrent(ctx context.Context, q *storedb.Queries, request ProvisionTargetRequest) (TargetState, error) {
	_, current, err := loadProvisionTarget(ctx, q, request)
	return current, err
}

// provisionTargetTx 执行目标加载、owner 调用和 CAS 日志追加。
func (s *Store) provisionTargetTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, request ProvisionTargetRequest, recordedAt time.Time, result *TargetState) error {
	operation, current, err := loadProvisionTarget(ctx, q, request)
	if err != nil {
		return err
	}
	if replayed, replayErr := replayProvisionTarget(ctx, q, request, current, result); replayed || replayErr != nil {
		return replayErr
	}
	if err := s.validateProvisionState(operation, current, request, recordedAt); err != nil {
		return err
	}
	grant, err := s.provisionGrant(ctx, txdb, operation, current)
	if err != nil {
		return err
	}
	grantPending, err := s.appendGrantProgress(ctx, q, request, current, grant, recordedAt)
	if err != nil {
		return err
	}
	handle, _, err := s.provisionWait(ctx, txdb, operation, grantPending, grant)
	if err != nil {
		return err
	}
	return s.appendWaiting(ctx, q, request, grantPending, grant, handle, recordedAt, result)
}

// loadProvisionTarget 在事务中加载并校验操作与目标头。
func loadProvisionTarget(ctx context.Context, q *storedb.Queries, request ProvisionTargetRequest) (Operation, TargetState, error) {
	operationRow, err := q.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: request.OperationKey})
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, TargetState{}, ErrOperationNotFound
	}
	if err != nil {
		return Operation{}, TargetState{}, classifyDBError("read wait operation for provisioning", err, ErrReplayConflict)
	}
	if operationRow.OperationDigest != request.OperationDigest {
		return Operation{}, TargetState{}, ErrReplayConflict
	}
	operation, err := operationFromRow(operationRow)
	if err != nil {
		return Operation{}, TargetState{}, err
	}
	row, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: request.TargetKey})
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, TargetState{}, ErrTargetNotFound
	}
	if err != nil {
		return Operation{}, TargetState{}, classifyDBError("read target for provisioning", err, ErrTargetConflict)
	}
	if row.OperationDigest != request.OperationDigest {
		return Operation{}, TargetState{}, ErrReplayConflict
	}
	current, err := targetFromRow(row)
	if err != nil {
		return Operation{}, TargetState{}, err
	}
	if !current.DeadlineAt.Equal(operation.DeadlineAt) {
		return Operation{}, TargetState{}, errors.Join(ErrInvalidOperation, errors.New("target deadline does not match operation deadline"))
	}
	return operation, current, nil
}

// replayProvisionTarget 识别已完成 waiting 目标的精确历史重放。
func replayProvisionTarget(ctx context.Context, q *storedb.Queries, request ProvisionTargetRequest, current TargetState, result *TargetState) (bool, error) {
	if current.Version <= request.ExpectedVersion || current.Phase != "waiting" {
		return false, nil
	}
	if request.ExpectedPhase != "planned" && request.ExpectedPhase != "grant_pending" && request.ExpectedPhase != "wait_pending" {
		return false, ErrTargetConflict
	}
	if request.ExpectedRecordDigest == "" {
		return false, ErrTargetConflict
	}
	history, err := q.ListOrchestrationWaitTargetJournal(ctx, storedb.ListOrchestrationWaitTargetJournalParams{OperationKey: request.OperationKey})
	if err != nil {
		return false, classifyDBError("read target replay history", err, ErrTargetConflict)
	}
	for _, historical := range history {
		if replayHistoryMatches(historical, request) {
			*result = current
			return true, nil
		}
	}
	return false, ErrTargetConflict
}

func replayHistoryMatches(row storedb.OrchestrationWaitOperationTargetJournal, request ProvisionTargetRequest) bool {
	return row.TargetKey == request.TargetKey && row.Version == request.ExpectedVersion && row.Phase == request.ExpectedPhase && row.RecordDigest == request.ExpectedRecordDigest
}

// validateProvisionState 校验 CAS 前置、fence 和 deadline 约束。
func (s *Store) validateProvisionState(operation Operation, current TargetState, request ProvisionTargetRequest, recordedAt time.Time) error {
	if current.Version != request.ExpectedVersion || current.Phase != request.ExpectedPhase || current.RecordDigest != request.ExpectedRecordDigest {
		return ErrTargetConflict
	}
	if current.Phase != "planned" && current.Phase != "wait_pending" {
		return ErrTargetConflict
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	if !recordedAt.Before(operation.DeadlineAt) {
		return ErrDeadlineExpired
	}
	return nil
}

func (s *Store) provisionGrant(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState) (accesswait.AccessGrant, error) {
	request := accesswait.AccessGrantRequest{GrantID: current.OriginalGrantID, CallerPrincipalRef: operation.CallerPrincipalRef, ScopeRef: operation.ScopeRef, Source: operation.Source, Target: current.Target, CanRead: true, IdempotencyNamespace: current.GrantNamespace, IdempotencyKeyDigest: current.GrantKeyDigest, RequestDigest: current.GrantRequestDigest, IssuedAt: operation.IssuedAt, ExpiresAt: operation.DeadlineAt}
	grant, err := s.loadOrCreateGrant(ctx, txdb, current, request)
	if err != nil {
		return accesswait.AccessGrant{}, err
	}
	if err := validateProvisionedGrant(operation, current, grant, request); err != nil {
		return accesswait.AccessGrant{}, fmt.Errorf("target %q grant response: %w", current.Key, err)
	}
	return grant, nil
}

// loadOrCreateGrant 根据目标阶段创建或读取幂等 grant。
func (s *Store) loadOrCreateGrant(ctx context.Context, txdb storedb.DBTX, current TargetState, request accesswait.AccessGrantRequest) (accesswait.AccessGrant, error) {
	if current.Phase == "planned" {
		grant, err := s.access.CreateGrantTx(ctx, txdb, request)
		if err != nil {
			return accesswait.AccessGrant{}, fmt.Errorf("target %q grant: %w", current.Key, err)
		}
		return grant, nil
	}
	if current.ResolvedGrantID == nil || *current.ResolvedGrantID != current.OriginalGrantID {
		return accesswait.AccessGrant{}, ErrReplayConflict
	}
	grant, err := s.access.GetGrantTx(ctx, txdb, current.OriginalGrantID)
	if err != nil {
		return accesswait.AccessGrant{}, fmt.Errorf("target %q grant replay: %w", current.Key, err)
	}
	return grant, nil
}

func (s *Store) appendGrantProgress(ctx context.Context, q *storedb.Queries, request ProvisionTargetRequest, current TargetState, grant accesswait.AccessGrant, recordedAt time.Time) (TargetState, error) {
	if current.Phase != "planned" {
		return current, nil
	}
	grantID := grant.GrantID
	grantPending, err := s.appendTargetTx(ctx, q, TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: request.TargetKey,
		ExpectedVersion: current.Version, ExpectedPhase: current.Phase, ExpectedRecordDigest: current.RecordDigest,
		GrantRevision: 0, GrantStatus: "planned", WaitRevision: 0, WaitStatus: "absent", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "grant_pending",
	}, recordedAt)
	if err != nil {
		return TargetState{}, err
	}
	return s.appendTargetTx(ctx, q, TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: request.TargetKey,
		ExpectedVersion: grantPending.Version, ExpectedPhase: grantPending.Phase, ExpectedRecordDigest: grantPending.RecordDigest,
		ResolvedGrantID: &grantID, GrantRevision: grant.Revision, GrantStatus: grant.Status,
		WaitRevision: 0, WaitStatus: "planned", GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "wait_pending",
	}, recordedAt)
}

func (s *Store) provisionWait(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState, grant accesswait.AccessGrant) (accesswait.WaitHandle, accesswait.WaitRequest, error) {
	request := accesswait.WaitRequest{GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: operation.CallerPrincipalRef, ScopeRef: operation.ScopeRef, Selector: current.Selector, AfterSequence: current.AfterSequence, AfterCursor: current.AfterCursor, DeadlineAt: operation.DeadlineAt, IdempotencyNamespace: current.WaitNamespace, IdempotencyKeyDigest: current.WaitKeyDigest, RequestDigest: current.WaitRequestDigest}
	handle, err := s.access.CreateWaitTx(ctx, txdb, request)
	if err != nil {
		return accesswait.WaitHandle{}, request, fmt.Errorf("target %q wait: %w", current.Key, err)
	}
	if err := validateProvisionedWait(operation, current, grant, handle, request); err != nil {
		return accesswait.WaitHandle{}, request, fmt.Errorf("target %q wait response: %w", current.Key, err)
	}
	return handle, request, nil
}

func (s *Store) appendWaiting(ctx context.Context, q *storedb.Queries, request ProvisionTargetRequest, current TargetState, grant accesswait.AccessGrant, handle accesswait.WaitHandle, recordedAt time.Time, result *TargetState) error {
	grantID := grant.GrantID
	waitID := handle.WaitHandleID
	var err error
	*result, err = s.appendTargetTx(ctx, q, TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: request.TargetKey,
		ExpectedVersion: current.Version, ExpectedPhase: current.Phase, ExpectedRecordDigest: current.RecordDigest,
		ResolvedGrantID: &grantID, GrantRevision: grant.Revision, GrantStatus: grant.Status,
		ResolvedWaitHandleID: &waitID, WaitRevision: handle.Revision, WaitStatus: handle.Status,
		GrantCleanupState: "none", WaitCleanupState: "none", NextPhase: "waiting",
	}, recordedAt)
	return err
}

// validateProvisionTargetRequest 校验目标 provisioning 请求的 CAS 字段。
func validateProvisionTargetRequest(request ProvisionTargetRequest) error {
	if err := validateText("operation_key", request.OperationKey, MaxOperationKey, false); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", request.OperationDigest); err != nil {
		return err
	}
	if err := validateText("target_key", request.TargetKey, MaxTargetKey, false); err != nil {
		return err
	}
	if request.ExpectedVersion <= 0 {
		return invalidOperation("expected target version must be positive")
	}
	if err := validateTargetPhase(request.ExpectedPhase); err != nil {
		return invalidOperation("expected phase: %v", err)
	}
	return validateDigest("expected_record_digest", request.ExpectedRecordDigest)
}

func validateProvisionedGrant(operation Operation, target TargetState, grant accesswait.AccessGrant, request accesswait.AccessGrantRequest) error {
	if !grantIdentityMatches(grant, request) || !grantTimingMatches(grant, operation) {
		return ErrReplayConflict
	}
	if err := grant.Validate(); err != nil {
		return errors.Join(ErrReplayConflict, err)
	}
	return nil
}

// grantIdentityMatches 比较 grant 的不可变身份与请求。
func grantIdentityMatches(grant accesswait.AccessGrant, request accesswait.AccessGrantRequest) bool {
	return grant.GrantID == request.GrantID && grant.CallerPrincipalRef == request.CallerPrincipalRef && grant.ScopeRef == request.ScopeRef && grant.Source == request.Source && grant.Target == request.Target && grant.CanRead && !grant.CanSend && grant.IdempotencyNamespace == request.IdempotencyNamespace && grant.IdempotencyKeyDigest == request.IdempotencyKeyDigest && grant.RequestDigest == request.RequestDigest
}

func grantTimingMatches(grant accesswait.AccessGrant, operation Operation) bool {
	return grant.IssuedAt.Equal(operation.IssuedAt) && grant.ExpiresAt.Equal(operation.DeadlineAt) && grant.Revision > 0 && grant.Status == "active"
}

func validateProvisionedWait(operation Operation, target TargetState, grant accesswait.AccessGrant, handle accesswait.WaitHandle, request accesswait.WaitRequest) error {
	if !waitIdentityMatches(operation, target, grant, handle, request) || !waitCursorMatches(handle, request) || !waitTimingMatches(handle, request) {
		return ErrReplayConflict
	}
	return nil
}

// waitIdentityMatches 比较 wait handle 的身份和选择器字段。
func waitIdentityMatches(operation Operation, target TargetState, grant accesswait.AccessGrant, handle accesswait.WaitHandle, request accesswait.WaitRequest) bool {
	return handle.WaitHandleID != "" && handle.GrantID == grant.GrantID && handle.GrantRevision == grant.Revision && handle.CallerPrincipalRef == request.CallerPrincipalRef && handle.ScopeRef == request.ScopeRef && handle.SourceAgentID == operation.Source.AgentID && handle.Target == target.Target && reflect.DeepEqual(handle.Selector, request.Selector)
}

func waitCursorMatches(handle accesswait.WaitHandle, request accesswait.WaitRequest) bool {
	return handle.AfterSequence == request.AfterSequence && handle.AfterCursor == request.AfterCursor
}

// waitTimingMatches 比较 wait handle 的时间、幂等和状态字段。
func waitTimingMatches(handle accesswait.WaitHandle, request accesswait.WaitRequest) bool {
	return handle.DeadlineAt.Equal(request.DeadlineAt) && handle.IdempotencyNamespace == request.IdempotencyNamespace && handle.IdempotencyKeyDigest == request.IdempotencyKeyDigest && handle.RequestDigest == request.RequestDigest && handle.Revision > 0 && handle.Status == "pending"
}
