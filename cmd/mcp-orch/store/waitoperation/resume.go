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

// ResumeTarget 依据 durable grant/wait 行对账一个未知目标，绝不重试创建副作用。
func (s *Store) ResumeTarget(ctx context.Context, request ResumeTargetRequest) (ResumeTargetResult, error) {
	if err := s.ready(ctx, false); err != nil {
		return TargetState{}, err
	}
	if err := validateResumeTargetRequest(request); err != nil {
		return TargetState{}, err
	}
	if err := s.CheckSchema(ctx); err != nil {
		return TargetState{}, err
	}
	now, err := s.nowUTC()
	if err != nil {
		return TargetState{}, err
	}
	if s.commitMarkers == nil {
		var result TargetState
		err = s.withImmediateTx(ctx, func(q *storedb.Queries, txdb storedb.DBTX) error {
			return s.resumeTargetTx(ctx, q, txdb, request, now, &result)
		})
		return result, err
	}
	return s.resumeTargetWithMarker(ctx, request, now)
}

// resumeTargetWithMarker 以 durable marker 包围恢复事务，并收口已解决重放。
func (s *Store) resumeTargetWithMarker(ctx context.Context, request ResumeTargetRequest, now time.Time) (TargetState, error) {
	predecessor, err := loadResumePredecessor(ctx, s.queries, request)
	if err != nil {
		return TargetState{}, err
	}
	preStateDigest := targetStateDigest(predecessor)
	descriptor, err := NewMutationDescriptor(MutationTargetResume, request.OperationKey, request.OperationDigest, request.TargetKey, request, preStateDigest)
	if err != nil {
		return TargetState{}, err
	}
	var result TargetState
	err = s.withImmediateMutation(ctx, &descriptor, func(q *storedb.Queries, txdb storedb.DBTX) error {
		if err := verifyResumePreState(ctx, q, request, descriptor.PreStateDigest); err != nil {
			return err
		}
		if err := s.resumeTargetTx(ctx, q, txdb, request, now, &result); err != nil {
			return err
		}
		descriptor.PostStateDigest = targetStateDigest(result)
		return nil
	})
	if errors.Is(err, ErrCommitMarkerResolved) {
		err = s.replayResolvedResumeTarget(ctx, request, &result)
	}
	return result, err
}

// verifyResumePreState 在事务内证明 marker 前驱仍是 durable target 日志中的同一行。
func verifyResumePreState(ctx context.Context, q *storedb.Queries, request ResumeTargetRequest, expectedDigest string) error {
	predecessor, err := loadResumePredecessor(ctx, q, request)
	if err != nil {
		return err
	}
	if targetStateDigest(predecessor) != expectedDigest {
		return ErrTargetConflict
	}
	return nil
}

// loadResumePredecessor 从 append-only target 日志读取 resume 请求指定的精确前驱。
func loadResumePredecessor(ctx context.Context, q *storedb.Queries, request ResumeTargetRequest) (TargetState, error) {
	if _, _, err := loadResumeTarget(ctx, q, request); err != nil {
		return TargetState{}, err
	}
	rows, err := q.ListOrchestrationWaitTargetJournal(ctx, storedb.ListOrchestrationWaitTargetJournalParams{OperationKey: request.OperationKey})
	if err != nil {
		return TargetState{}, classifyDBError("read resume target predecessor", err, ErrTargetConflict)
	}
	for _, row := range rows {
		if row.OperationDigest != request.OperationDigest || row.TargetKey != request.TargetKey || row.Version != request.ExpectedVersion || row.Phase != request.ExpectedPhase || row.UncertainPhase != request.ExpectedUncertainPhase || row.RecordDigest != request.ExpectedRecordDigest {
			continue
		}
		return targetFromRow(row)
	}
	return TargetState{}, ErrTargetConflict
}

// replayResolvedResumeTarget 只读恢复已解决 marker，绝不重跑 owner 副作用。
func (s *Store) replayResolvedResumeTarget(ctx context.Context, request ResumeTargetRequest, result *TargetState) error {
	operation, current, err := loadResumeTarget(ctx, s.queries, request)
	if err != nil {
		return err
	}
	if err := s.proveTargetMembership(ctx, s.queries, operation, current); err != nil {
		return err
	}
	replay, err := validateResumeHead(ctx, s.queries, current, request)
	if err != nil {
		return err
	}
	if !replay {
		return ErrTargetConflict
	}
	*result = current
	return nil
}

// ReconcileTarget 是 ResumeTarget 的 owner-local 兼容别名。
func (s *Store) ReconcileTarget(ctx context.Context, request ResumeTargetRequest) (ResumeTargetResult, error) {
	return s.ResumeTarget(ctx, request)
}

// validateResumeTargetRequest 校验未知目标的固定身份、阶段和前驱摘要。
func validateResumeTargetRequest(request ResumeTargetRequest) error {
	if err := validateResumeIdentity(request); err != nil {
		return err
	}
	if request.ExpectedVersion <= 0 {
		return invalidOperation("expected target version must be positive")
	}
	if request.ExpectedPhase != "outcome_unknown" {
		return invalidOperation("resume requires an outcome_unknown target")
	}
	if err := validateResumePhase(request.ExpectedUncertainPhase); err != nil {
		return err
	}
	return validateDigest("expected_record_digest", request.ExpectedRecordDigest)
}

func validateResumeIdentity(request ResumeTargetRequest) error {
	if err := validateText("operation_key", request.OperationKey, MaxOperationKey, false); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", request.OperationDigest); err != nil {
		return err
	}
	return validateText("target_key", request.TargetKey, MaxTargetKey, false)
}

func validateResumePhase(phase string) error {
	switch phase {
	case "grant_create", "wait_create", "target_transition":
		return nil
	default:
		return invalidOperation("resume uncertain phase is unsupported")
	}
}

// resumeTargetTx 在单一事务内读取资源并追加唯一安全 successor。
func (s *Store) resumeTargetTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, request ResumeTargetRequest, now time.Time, result *TargetState) error {
	operation, current, err := loadResumeTarget(ctx, q, request)
	if err != nil {
		return err
	}
	if err := s.proveTargetMembership(ctx, q, operation, current); err != nil {
		return err
	}
	replay, err := validateResumeHead(ctx, q, current, request)
	if err != nil {
		return err
	}
	if replay {
		*result = current
		return nil
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	if s.access == nil {
		return ErrAccessOwnerRequired
	}
	grant, wait, err := s.readResumeResourcesTx(ctx, txdb, operation, current)
	if err != nil {
		return err
	}
	transition, err := resumeTransition(current, grant, wait)
	if err != nil {
		return err
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	*result, err = s.appendTargetTx(ctx, q, transition, now)
	return err
}

// validateResumeHead 区分未知前驱、精确重放和可执行的当前未知头。
func validateResumeHead(ctx context.Context, q *storedb.Queries, current TargetState, request ResumeTargetRequest) (bool, error) {
	if current.Version < request.ExpectedVersion {
		return false, ErrTargetConflict
	}
	if current.Version > request.ExpectedVersion {
		if current.Phase != "outcome_unknown" {
			historyMatches, historyErr := resumeHistoryMatches(ctx, q, request)
			if historyErr != nil {
				return false, historyErr
			}
			if historyMatches {
				return true, nil
			}
		}
		return false, ErrTargetConflict
	}
	if current.Phase != request.ExpectedPhase || current.UncertainPhase != request.ExpectedUncertainPhase || current.RecordDigest != request.ExpectedRecordDigest {
		return false, ErrTargetConflict
	}
	return false, nil
}

// loadResumeTarget 读取并验证 operation/target 的 exact digest 关系。
func loadResumeTarget(ctx context.Context, q *storedb.Queries, request ResumeTargetRequest) (Operation, TargetState, error) {
	operation, err := loadResumeOperation(ctx, q, request)
	if err != nil {
		return Operation{}, TargetState{}, err
	}
	target, err := loadResumeHead(ctx, q, request)
	if err != nil {
		return Operation{}, TargetState{}, err
	}
	return operation, target, nil
}

// loadResumeOperation 读取 operation 并阻断 cleanup 生命周期的误用。
func loadResumeOperation(ctx context.Context, q *storedb.Queries, request ResumeTargetRequest) (Operation, error) {
	operationRow, err := q.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: request.OperationKey})
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, ErrOperationNotFound
	}
	if err != nil {
		return Operation{}, classifyDBError("read wait operation for resume", err, ErrOperationStateConflict)
	}
	if operationRow.OperationDigest != request.OperationDigest {
		return Operation{}, ErrReplayConflict
	}
	operation, err := operationFromRow(operationRow)
	if err != nil {
		return Operation{}, err
	}
	if operation.Status == "cleanup_pending" || operation.Status == "cleanup_cleared" || (operation.Status == "outcome_unknown" && operation.UncertainPhase == "cleanup") {
		return Operation{}, ErrOperationStateConflict
	}
	return operation, nil
}

func loadResumeHead(ctx context.Context, q *storedb.Queries, request ResumeTargetRequest) (TargetState, error) {
	targetRow, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: request.TargetKey})
	if errors.Is(err, sql.ErrNoRows) {
		return TargetState{}, ErrTargetNotFound
	}
	if err != nil {
		return TargetState{}, classifyDBError("read target for resume", err, ErrTargetConflict)
	}
	if targetRow.OperationDigest != request.OperationDigest {
		return TargetState{}, ErrReplayConflict
	}
	target, err := targetFromRow(targetRow)
	if err != nil {
		return TargetState{}, err
	}
	return target, nil
}

// resumeHistoryMatches 判断未知目标请求的前驱是否仍在 append-only journal 中。
func resumeHistoryMatches(ctx context.Context, q *storedb.Queries, request ResumeTargetRequest) (bool, error) {
	rows, err := q.ListOrchestrationWaitTargetJournal(ctx, storedb.ListOrchestrationWaitTargetJournalParams{OperationKey: request.OperationKey})
	if err != nil {
		return false, classifyDBError("read resume target history", err, ErrTargetConflict)
	}
	for _, row := range rows {
		if row.TargetKey == request.TargetKey && row.Version == request.ExpectedVersion && row.Phase == request.ExpectedPhase && row.UncertainPhase == request.ExpectedUncertainPhase && row.RecordDigest == request.ExpectedRecordDigest {
			return true, nil
		}
	}
	return false, nil
}

// readResumeResourcesTx 只读取现有 grant/wait，不调用任何创建 owner。
func (s *Store) readResumeResourcesTx(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState) (*accesswait.AccessGrant, *accesswait.WaitHandle, error) {
	grantID := current.OriginalGrantID
	if current.ResolvedGrantID != nil {
		grantID = *current.ResolvedGrantID
	}
	grant, err := s.readResumeGrantTx(ctx, txdb, operation, current, grantID)
	if err != nil {
		return nil, nil, err
	}
	var wait *accesswait.WaitHandle
	needWait := current.ResolvedWaitHandleID != nil || current.UncertainPhase == "wait_create" || (current.UncertainPhase == "target_transition" && current.WaitStatus != "absent")
	allowMissingWait := current.UncertainPhase == "target_transition" && current.ResolvedWaitHandleID == nil && current.WaitStatus == "planned"
	if needWait {
		wait, err = s.readResumeWaitTx(ctx, txdb, operation, current, grant, allowMissingWait)
		if err != nil {
			return nil, nil, err
		}
	}
	return grant, wait, nil
}

// readResumeGrantTx 按原始 grant id 读取未知创建的结果。
func (s *Store) readResumeGrantTx(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState, grantID string) (*accesswait.AccessGrant, error) {
	grant, err := s.access.GetGrantTx(ctx, txdb, grantID)
	if err != nil {
		if errors.Is(err, accesswait.ErrAccessNotFound) {
			return nil, fmt.Errorf("%w: grant %s", ErrResumeUnknown, grantID)
		}
		return nil, fmt.Errorf("resume grant read: %w", err)
	}
	if err := validateResumeGrant(operation, current, grant); err != nil {
		return nil, err
	}
	return &grant, nil
}

// readResumeWaitTx 按原始 wait 幂等键读取未知创建的结果。
func (s *Store) readResumeWaitTx(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState, grant *accesswait.AccessGrant, allowMissing bool) (*accesswait.WaitHandle, error) {
	var value accesswait.WaitHandle
	var err error
	if current.ResolvedWaitHandleID != nil {
		value, err = s.access.GetWaitByIDTx(ctx, txdb, *current.ResolvedWaitHandleID)
	} else {
		value, err = s.access.GetWaitByKeyTx(ctx, txdb, current.WaitNamespace, current.WaitKeyDigest)
	}
	if err != nil {
		if errors.Is(err, accesswait.ErrWaitNotFound) {
			if allowMissing {
				return nil, nil
			}
			return nil, fmt.Errorf("%w: wait %s", ErrResumeUnknown, current.WaitKeyDigest)
		}
		return nil, fmt.Errorf("resume wait read: %w", err)
	}
	if value.WaitHandleID == "" {
		return nil, errors.Join(ErrInvalidOperation, errors.New("resume wait handle id is empty"))
	}
	if current.ResolvedWaitHandleID != nil && value.WaitHandleID != *current.ResolvedWaitHandleID {
		return nil, ErrReplayConflict
	}
	if err := validateResumeWait(operation, current, *grant, value); err != nil {
		return nil, err
	}
	return &value, nil
}

// validateResumeGrant 校验 grant 的完整 immutable identity 和 operation 时间窗。
func validateResumeGrant(operation Operation, target TargetState, grant accesswait.AccessGrant) error {
	if !resumeGrantIdentityMatches(operation, target, grant) || !grant.IssuedAt.Equal(operation.IssuedAt) || !grant.ExpiresAt.Equal(operation.DeadlineAt) {
		return ErrReplayConflict
	}
	if grant.Status != "active" {
		return fmt.Errorf("%w: grant %s has status %s", ErrResumeTerminal, grant.GrantID, grant.Status)
	}
	return nil
}

// resumeGrantIdentityMatches 比对未知 grant 的 caller、fence 和幂等摘要。
func resumeGrantIdentityMatches(operation Operation, target TargetState, grant accesswait.AccessGrant) bool {
	if grant.GrantID != target.OriginalGrantID || grant.CallerPrincipalRef != operation.CallerPrincipalRef || grant.ScopeRef != operation.ScopeRef {
		return false
	}
	if !reflect.DeepEqual(grant.Source, operation.Source) || !reflect.DeepEqual(grant.Target, target.Target) {
		return false
	}
	return grant.CanRead && !grant.CanSend && grant.IdempotencyNamespace == target.GrantNamespace && grant.IdempotencyKeyDigest == target.GrantKeyDigest && grant.RequestDigest == target.GrantRequestDigest
}

// validateResumeWait 校验 wait 的 immutable selector、cursor 和 pending 状态。
func validateResumeWait(operation Operation, target TargetState, grant accesswait.AccessGrant, wait accesswait.WaitHandle) error {
	if !resumeWaitIdentityMatches(operation, target, grant, wait) || !resumeWaitCursorMatches(target, wait) || !resumeWaitKeyMatches(target, grant, wait) {
		return ErrReplayConflict
	}
	if wait.Status != "pending" {
		return fmt.Errorf("%w: wait %s has status %s", ErrResumeTerminal, wait.WaitHandleID, wait.Status)
	}
	return nil
}

// resumeWaitIdentityMatches 比对未知 wait 的 caller、grant 和 target fence。
func resumeWaitIdentityMatches(operation Operation, target TargetState, grant accesswait.AccessGrant, wait accesswait.WaitHandle) bool {
	return wait.GrantID == grant.GrantID && wait.CallerPrincipalRef == operation.CallerPrincipalRef && wait.ScopeRef == operation.ScopeRef && wait.SourceAgentID == operation.Source.AgentID && reflect.DeepEqual(wait.Target, target.Target) && reflect.DeepEqual(wait.Selector, target.Selector)
}

func resumeWaitCursorMatches(target TargetState, wait accesswait.WaitHandle) bool {
	return wait.AfterSequence == target.AfterSequence && wait.AfterCursor == target.AfterCursor && wait.DeadlineAt.Equal(target.DeadlineAt)
}

func resumeWaitKeyMatches(target TargetState, grant accesswait.AccessGrant, wait accesswait.WaitHandle) bool {
	return wait.IdempotencyNamespace == target.WaitNamespace && wait.IdempotencyKeyDigest == target.WaitKeyDigest && wait.RequestDigest == target.WaitRequestDigest && wait.GrantRevision > 0 && wait.GrantRevision <= grant.Revision
}

// resumeTransition 选择唯一不产生副作用的 target successor。
func resumeTransition(current TargetState, grant *accesswait.AccessGrant, wait *accesswait.WaitHandle) (TargetTransitionRequest, error) {
	if grant == nil {
		return TargetTransitionRequest{}, errors.Join(ErrInvalidOperation, errors.New("resume requires grant"))
	}
	request := TargetTransitionRequest{
		OperationKey: current.OperationKey, OperationDigest: current.OperationDigest, TargetKey: current.Key,
		ExpectedVersion: current.Version, ExpectedPhase: current.Phase, ExpectedRecordDigest: current.RecordDigest,
		ResolvedGrantID: &grant.GrantID, GrantRevision: grant.Revision, GrantStatus: grant.Status,
		GrantCleanupState: "none", WaitCleanupState: "none",
	}
	switch current.UncertainPhase {
	case "grant_create":
		if wait == nil {
			request.NextPhase = "wait_pending"
			request.WaitStatus = "planned"
			return request, nil
		}
	case "wait_create":
		if wait != nil {
			waitID := wait.WaitHandleID
			request.ResolvedWaitHandleID = &waitID
			request.WaitRevision, request.WaitStatus = wait.Revision, wait.Status
			request.NextPhase = "waiting"
			return request, nil
		}
	case "target_transition":
		if current.GrantStatus != "active" || current.ResolvedGrantID == nil {
			return TargetTransitionRequest{}, fmt.Errorf("%w: target-transition unknown has no resolved active grant", ErrResumeUnknown)
		}
		if wait == nil {
			request.NextPhase = "wait_pending"
			request.WaitStatus = "planned"
			return request, nil
		}
		waitID := wait.WaitHandleID
		request.ResolvedWaitHandleID = &waitID
		request.WaitRevision, request.WaitStatus = wait.Revision, wait.Status
		request.NextPhase = "waiting"
		return request, nil
	}
	return TargetTransitionRequest{}, fmt.Errorf("%w: no safe durable successor for %s", ErrResumeUnknown, current.UncertainPhase)
}
