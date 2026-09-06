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

var errCleanupReplayWithoutMarker = errors.New("wait operation cleanup replay bypasses commit marker")

// CleanupTarget 执行 durable wait-operation target 清理候选；只消费可查询终态资源，未知效果保持阻断。
func (s *Store) CleanupTarget(ctx context.Context, request CleanupTargetRequest) (CleanupTargetResult, error) {
	if err := s.ready(ctx, false); err != nil {
		return TargetState{}, err
	}
	if err := validateCleanupTargetRequest(request); err != nil {
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
		return s.cleanupTargetWithoutMarker(ctx, request, now)
	}
	return s.cleanupTargetWithMarker(ctx, request, now)
}

func (s *Store) cleanupTargetWithoutMarker(ctx context.Context, request CleanupTargetRequest, now time.Time) (CleanupTargetResult, error) {
	var result TargetState
	err := s.withImmediateTx(ctx, func(q *storedb.Queries, txdb storedb.DBTX) error {
		return s.cleanupTargetTx(ctx, q, txdb, request, now, &result)
	})
	return result, err
}

func (s *Store) cleanupTargetWithMarker(ctx context.Context, request CleanupTargetRequest, now time.Time) (CleanupTargetResult, error) {
	predecessor, err := loadCleanupHead(ctx, s.queries, request)
	if err != nil {
		return TargetState{}, err
	}
	descriptor, err := NewMutationDescriptor(MutationTargetCleanup, request.OperationKey, request.OperationDigest, request.TargetKey, request, targetStateDigest(predecessor))
	if err != nil {
		return TargetState{}, err
	}
	if predecessor.Phase == "cleanup_cleared" {
		if err := s.reconcileReplayMarkers(ctx, request.OperationKey, request.OperationDigest, MutationTargetCleanup, request.TargetKey, descriptor.RequestDigest, targetStateDigest(predecessor)); err != nil {
			return TargetState{}, err
		}
		return s.cleanupTargetReplay(ctx, request)
	}
	return s.cleanupTargetMarkedMutation(ctx, request, now, descriptor)
}

func (s *Store) cleanupTargetMarkedMutation(ctx context.Context, request CleanupTargetRequest, now time.Time, descriptor CommitMarkerDescriptor) (CleanupTargetResult, error) {
	var result TargetState
	err := s.withImmediateMutation(ctx, &descriptor, func(q *storedb.Queries, txdb storedb.DBTX) error {
		return s.cleanupTargetMarkerTx(ctx, q, txdb, request, now, &descriptor, &result)
	})
	if errors.Is(err, errCleanupReplayWithoutMarker) || errors.Is(err, ErrCommitMarkerResolved) {
		return s.cleanupTargetReplay(ctx, request)
	}
	return result, err
}

func (s *Store) cleanupTargetMarkerTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, request CleanupTargetRequest, now time.Time, descriptor *CommitMarkerDescriptor, result *TargetState) error {
	current, err := loadCleanupHead(ctx, q, request)
	if err != nil {
		return err
	}
	if current.Phase == "cleanup_cleared" {
		return errCleanupReplayWithoutMarker
	}
	if targetStateDigest(current) != descriptor.PreStateDigest {
		return ErrTargetConflict
	}
	if err := s.cleanupTargetTx(ctx, q, txdb, request, now, result); err != nil {
		return err
	}
	descriptor.PostStateDigest = targetStateDigest(*result)
	return nil
}

func (s *Store) cleanupTargetReplay(ctx context.Context, request CleanupTargetRequest) (CleanupTargetResult, error) {
	var result TargetState
	err := s.withImmediateTx(ctx, func(q *storedb.Queries, _ storedb.DBTX) error {
		operation, current, err := loadCleanupTarget(ctx, q, request)
		if err != nil {
			return err
		}
		if current.Phase != "cleanup_cleared" {
			return ErrTargetConflict
		}
		return s.replayClearedCleanup(ctx, q, operation, current, request, &result)
	})
	return result, err
}

// Cleanup 是 CleanupTarget 的 owner-local 兼容别名。
func (s *Store) Cleanup(ctx context.Context, request CleanupTargetRequest) (CleanupTargetResult, error) {
	return s.CleanupTarget(ctx, request)
}

func validateCleanupTargetRequest(request CleanupTargetRequest) error {
	if err := validateCleanupIdentity(request); err != nil {
		return err
	}
	if request.ExpectedVersion <= 0 {
		return invalidOperation("expected target version must be positive")
	}
	if err := validateCleanupPhase(request.ExpectedPhase); err != nil {
		return err
	}
	return validateCleanupProof(request)
}

func validateCleanupIdentity(request CleanupTargetRequest) error {
	if err := validateText("operation_key", request.OperationKey, MaxOperationKey, false); err != nil {
		return err
	}
	if err := validateDigest("operation_digest", request.OperationDigest); err != nil {
		return err
	}
	return validateText("target_key", request.TargetKey, MaxTargetKey, false)
}

func validateCleanupPhase(phase string) error {
	switch phase {
	case "satisfied", "timed_out", "cancelled", "failed", "cleanup_pending":
		return nil
	default:
		return invalidOperation("cleanup expected phase is not terminal")
	}
}

func validateCleanupProof(request CleanupTargetRequest) error {
	if err := validateDigest("expected_record_digest", request.ExpectedRecordDigest); err != nil {
		return err
	}
	if err := validateDigest("cleanup_request_digest", request.CleanupRequestDigest); err != nil {
		return err
	}
	if err := validateText("cleanup_reason_ref", request.CleanupReasonRef, 512, false); err != nil {
		return err
	}
	want := cleanupRequestDigest(request.OperationKey, request.OperationDigest, request.CleanupReasonRef)
	if request.CleanupRequestDigest != want {
		return errors.Join(ErrReplayConflict, invalidOperation("cleanup request digest is not canonical"))
	}
	return nil
}

// cleanupTargetTx 在一个 owner 事务内完成 target 清理、资源撤销和 journal 收口。
func (s *Store) cleanupTargetTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, request CleanupTargetRequest, now time.Time, result *TargetState) error {
	operation, current, err := loadCleanupTarget(ctx, q, request)
	if err != nil {
		return err
	}
	if err := s.proveTargetMembership(ctx, q, operation, current); err != nil {
		return err
	}
	if current.Phase == "cleanup_cleared" {
		return s.replayClearedCleanup(ctx, q, operation, current, request, result)
	}
	if err := s.validateCleanupInvocation(operation, current, request); err != nil {
		return err
	}
	current, err = s.prepareCleanupHead(ctx, q, txdb, operation, request, current, now)
	if err != nil {
		return fmt.Errorf("append cleanup pending: %w", err)
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	cleaned, err := s.cleanupResourcesTx(ctx, txdb, operation, current, request, now)
	if err != nil {
		return fmt.Errorf("cleanup resources: %w", err)
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	*result, err = s.appendCleanupCleared(ctx, q, request, cleaned, now)
	if err != nil {
		return fmt.Errorf("append cleanup cleared: %w", err)
	}
	return nil
}

// validateCleanupInvocation 汇总清理写入前的 operation、版本、fence 和 owner 检查。
func (s *Store) validateCleanupInvocation(operation Operation, current TargetState, request CleanupTargetRequest) error {
	if err := validateCleanupOperation(operation, request, false); err != nil {
		return err
	}
	if err := validateCleanupHeadVersion(current, request); err != nil {
		return err
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	if s.access == nil {
		return ErrAccessOwnerRequired
	}
	return nil
}

// prepareCleanupHead 先读回最新资源 revision，再安装 cleanup_pending head。
func (s *Store) prepareCleanupHead(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, operation Operation, request CleanupTargetRequest, current TargetState, now time.Time) (TargetState, error) {
	if current.Phase == "cleanup_pending" {
		return current, nil
	}
	grant, wait, err := s.readCleanupResourcesTx(ctx, txdb, operation, current)
	if err != nil {
		return TargetState{}, fmt.Errorf("read cleanup resources: %w", err)
	}
	if err := validateCleanupResourceReadiness(grant, wait); err != nil {
		return TargetState{}, err
	}
	projected := cleanupStateFromResources(current, grant, wait)
	return s.ensureCleanupPending(ctx, q, request, projected, now)
}

// proveTargetMembership 确认 target 属于 operation 的规范 target-set 与最新 head。
func (s *Store) proveTargetMembership(ctx context.Context, q *storedb.Queries, operation Operation, current TargetState) error {
	if err := s.populateHeads(ctx, q, &operation); err != nil {
		return err
	}
	for _, target := range operation.Targets {
		if target.Key == current.Key && target.Version == current.Version && target.RecordDigest == current.RecordDigest {
			return nil
		}
	}
	return ErrTargetConflict
}

// validateCleanupHeadVersion 校验清理请求的版本和前驱摘要，拒绝越过当前头。
func validateCleanupHeadVersion(current TargetState, request CleanupTargetRequest) error {
	if current.Version < request.ExpectedVersion {
		return ErrTargetConflict
	}
	if current.Version == request.ExpectedVersion && (current.Phase != request.ExpectedPhase || current.RecordDigest != request.ExpectedRecordDigest) {
		return ErrTargetConflict
	}
	if current.Version > request.ExpectedVersion && current.Phase != "cleanup_pending" {
		return ErrTargetConflict
	}
	return nil
}

// ensureCleanupPending 复用已有 pending head，或从合法终态追加一条 pending 记录。
func (s *Store) ensureCleanupPending(ctx context.Context, q *storedb.Queries, request CleanupTargetRequest, current TargetState, now time.Time) (TargetState, error) {
	if current.Phase == "cleanup_pending" {
		return current, nil
	}
	if err := validateCleanupPredecessor(current); err != nil {
		return TargetState{}, err
	}
	return s.appendCleanupPending(ctx, q, request, current, now)
}

func (s *Store) replayClearedCleanup(ctx context.Context, q *storedb.Queries, operation Operation, current TargetState, request CleanupTargetRequest, result *TargetState) error {
	if err := validateCleanupOperation(operation, request, true); err != nil {
		return err
	}
	historyMatches, err := cleanupHistoryMatchesTarget(ctx, q, request)
	if err != nil {
		return err
	}
	if current.Version == request.ExpectedVersion || !historyMatches {
		return ErrTargetConflict
	}
	*result = current
	return nil
}

// loadCleanupTarget 读取并校验 cleanup operation head 与 target head。
func loadCleanupTarget(ctx context.Context, q *storedb.Queries, request CleanupTargetRequest) (Operation, TargetState, error) {
	operation, err := loadCleanupOperation(ctx, q, request)
	if err != nil {
		return Operation{}, TargetState{}, err
	}
	target, err := loadCleanupHead(ctx, q, request)
	if err != nil {
		return Operation{}, TargetState{}, err
	}
	if target.Version > request.ExpectedVersion {
		if target.Phase != "cleanup_pending" && target.Phase != "cleanup_cleared" {
			return Operation{}, TargetState{}, ErrTargetConflict
		}
		historyMatches, historyErr := cleanupHistoryMatchesTarget(ctx, q, request)
		if historyErr != nil {
			return Operation{}, TargetState{}, historyErr
		}
		if !historyMatches {
			return Operation{}, TargetState{}, ErrTargetConflict
		}
	}
	return operation, target, nil
}

func loadCleanupOperation(ctx context.Context, q *storedb.Queries, request CleanupTargetRequest) (Operation, error) {
	row, err := q.GetOrchestrationWaitOperation(ctx, storedb.GetOrchestrationWaitOperationParams{OperationKey: request.OperationKey})
	if errors.Is(err, sql.ErrNoRows) {
		return Operation{}, ErrOperationNotFound
	}
	if err != nil {
		return Operation{}, classifyDBError("read wait operation for cleanup", err, ErrOperationStateConflict)
	}
	if row.OperationDigest != request.OperationDigest {
		return Operation{}, ErrReplayConflict
	}
	operation, err := operationFromRow(row)
	if err != nil {
		return Operation{}, err
	}
	return operation, nil
}

func loadCleanupHead(ctx context.Context, q *storedb.Queries, request CleanupTargetRequest) (TargetState, error) {
	targetRow, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: request.TargetKey})
	if errors.Is(err, sql.ErrNoRows) {
		return TargetState{}, ErrTargetNotFound
	}
	if err != nil {
		return TargetState{}, classifyDBError("read target for cleanup", err, ErrTargetConflict)
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

// validateCleanupOperation 校验 operation head 的 cleanup 状态与请求摘要一致。
func validateCleanupOperation(operation Operation, request CleanupTargetRequest, replay bool) error {
	if operation.Status == "cleanup_cleared" {
		if operation.CleanupRequestDigest != request.CleanupRequestDigest || operation.CleanupReasonRef != request.CleanupReasonRef {
			return ErrReplayConflict
		}
		if replay {
			return nil
		}
		return ErrOperationStateConflict
	}
	if operation.Status != "cleanup_pending" {
		return ErrOperationStateConflict
	}
	if operation.CleanupRequestDigest != request.CleanupRequestDigest || operation.CleanupReasonRef != request.CleanupReasonRef {
		return ErrReplayConflict
	}
	return nil
}

// cleanupHistoryMatchesTarget 读取历史以确认清理请求的前驱确实存在。
func cleanupHistoryMatchesTarget(ctx context.Context, q *storedb.Queries, request CleanupTargetRequest) (bool, error) {
	rows, err := q.ListOrchestrationWaitTargetJournal(ctx, storedb.ListOrchestrationWaitTargetJournalParams{OperationKey: request.OperationKey})
	if err != nil {
		return false, classifyDBError("read cleanup target history", err, ErrTargetConflict)
	}
	return cleanupHistoryMatches(rows, request), nil
}

// validateCleanupPredecessor 限定只能从终态 target 进入 cleanup。
func validateCleanupPredecessor(target TargetState) error {
	switch target.Phase {
	case "satisfied", "timed_out", "cancelled", "failed":
		return nil
	default:
		return fmt.Errorf("%w: target phase %s cannot enter cleanup", ErrTargetConflict, target.Phase)
	}
}

// cleanupHistoryMatches 判断 journal 中是否存在请求声明的精确前驱。
func cleanupHistoryMatches(rows []storedb.OrchestrationWaitOperationTargetJournal, request CleanupTargetRequest) bool {
	for _, row := range rows {
		if row.TargetKey == request.TargetKey && row.Version == request.ExpectedVersion && row.Phase == request.ExpectedPhase && row.RecordDigest == request.ExpectedRecordDigest {
			return true
		}
	}
	return false
}

// prepareCleanupPending 追加 cleanup_pending 前驱，保持资源引用和 revision 连续。
func (s *Store) prepareCleanupPending(ctx context.Context, q *storedb.Queries, request CleanupTargetRequest, current TargetState, now time.Time) (TargetState, error) {
	if err := validateCleanupPredecessor(current); err != nil {
		return TargetState{}, err
	}
	return s.appendCleanupPending(ctx, q, request, current, now)
}

// appendCleanupPending 写入资源清理已移交但尚未确认的 target head。
func (s *Store) appendCleanupPending(ctx context.Context, q *storedb.Queries, request CleanupTargetRequest, current TargetState, now time.Time) (TargetState, error) {
	grantID, waitID := cloneString(current.ResolvedGrantID), cloneString(current.ResolvedWaitHandleID)
	return s.appendTargetTx(ctx, q, TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: request.TargetKey,
		ExpectedVersion: current.Version, ExpectedPhase: current.Phase, ExpectedRecordDigest: current.RecordDigest,
		ResolvedGrantID: grantID, GrantRevision: current.GrantRevision, GrantStatus: current.GrantStatus,
		ResolvedWaitHandleID: waitID, WaitRevision: current.WaitRevision, WaitStatus: current.WaitStatus,
		GrantCleanupState: "pending", WaitCleanupState: "pending", NextPhase: "cleanup_pending", UncertainPhase: "cleanup",
	}, now)
}

// cleanupResourcesTx 在同一事务中读取资源并撤销仍 active 的 grant。
func (s *Store) cleanupResourcesTx(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState, request CleanupTargetRequest, now time.Time) (TargetState, error) {
	grant, wait, err := s.readCleanupResourcesTx(ctx, txdb, operation, current)
	if err != nil {
		return TargetState{}, err
	}
	if err := validateCleanupResourceReadiness(grant, wait); err != nil {
		return TargetState{}, err
	}
	if grant != nil {
		switch grant.Status {
		case "active":
			revoked, revokeErr := s.access.RevokeGrantTx(ctx, txdb, accesswait.RevokeGrantRequest{
				GrantID: grant.GrantID, GrantRevision: grant.Revision, CallerPrincipalRef: operation.CallerPrincipalRef,
				ScopeRef: operation.ScopeRef, ReasonRef: request.CleanupReasonRef, RequestDigest: grant.RequestDigest, RevokedAt: now,
			})
			if revokeErr != nil {
				return TargetState{}, revokeErr
			}
			grant = &revoked
		case "revoked", "expired":
		default:
			return TargetState{}, fmt.Errorf("%w: grant %s has status %s", ErrCleanupUnknown, grant.GrantID, grant.Status)
		}
	}
	return cleanupStateFromResources(current, grant, wait), nil
}

// validateCleanupResourceReadiness 阻止 pending/unknown 资源被误报为可清理。
func validateCleanupResourceReadiness(grant *accesswait.AccessGrant, wait *accesswait.WaitHandle) error {
	if wait != nil && wait.Status == "pending" {
		return fmt.Errorf("%w: wait handle %s remains pending", ErrCleanupUnknown, wait.WaitHandleID)
	}
	if wait != nil && wait.Status == "unknown" {
		return fmt.Errorf("%w: wait handle %s is unknown", ErrCleanupUnknown, wait.WaitHandleID)
	}
	if grant != nil && grant.Status == "unknown" {
		return fmt.Errorf("%w: grant %s is unknown", ErrCleanupUnknown, grant.GrantID)
	}
	return nil
}

// readCleanupResourcesTx 读取并验证 target 引用的 grant/wait durable 行。
func (s *Store) readCleanupResourcesTx(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState) (*accesswait.AccessGrant, *accesswait.WaitHandle, error) {
	grant, err := s.readCleanupGrantTx(ctx, txdb, operation, current)
	if err != nil {
		return nil, nil, err
	}
	wait, err := s.readCleanupWaitTx(ctx, txdb, operation, current, grant)
	if err != nil {
		return nil, nil, err
	}
	if wait != nil && grant == nil {
		return nil, nil, errors.Join(ErrInvalidOperation, errors.New("cleanup wait has no grant"))
	}
	return grant, wait, nil
}

// readCleanupGrantTx 验证 grant 的完整 operation/target 绑定。
func (s *Store) readCleanupGrantTx(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState) (*accesswait.AccessGrant, error) {
	if current.ResolvedGrantID == nil {
		return nil, nil
	}
	value, err := s.access.GetGrantTx(ctx, txdb, *current.ResolvedGrantID)
	if err != nil {
		return nil, fmt.Errorf("cleanup grant read: %w", err)
	}
	if !cleanupGrantMatches(operation, current, value) {
		return nil, ErrReplayConflict
	}
	return &value, nil
}

// cleanupGrantMatches 比对清理所依赖 grant 的完整不可变字段。
func cleanupGrantMatches(operation Operation, current TargetState, value accesswait.AccessGrant) bool {
	if value.GrantID != current.OriginalGrantID || value.CallerPrincipalRef != operation.CallerPrincipalRef || value.ScopeRef != operation.ScopeRef {
		return false
	}
	if !reflect.DeepEqual(value.Source, operation.Source) || !reflect.DeepEqual(value.Target, current.Target) {
		return false
	}
	return value.CanRead && !value.CanSend && value.IdempotencyNamespace == current.GrantNamespace && value.IdempotencyKeyDigest == current.GrantKeyDigest && value.RequestDigest == current.GrantRequestDigest
}

// readCleanupWaitTx 验证 wait 的 selector、cursor、scope 和 grant 关联。
func (s *Store) readCleanupWaitTx(ctx context.Context, txdb storedb.DBTX, operation Operation, current TargetState, grant *accesswait.AccessGrant) (*accesswait.WaitHandle, error) {
	if current.ResolvedWaitHandleID == nil {
		return nil, nil
	}
	value, err := s.access.GetWaitByIDTx(ctx, txdb, *current.ResolvedWaitHandleID)
	if err != nil {
		return nil, fmt.Errorf("cleanup wait read: %w", err)
	}
	if !cleanupWaitMatches(operation, current, grant, value) {
		return nil, ErrReplayConflict
	}
	return &value, nil
}

// cleanupWaitMatches 比对 wait 的句柄、selector、cursor 和资源代际。
func cleanupWaitMatches(operation Operation, current TargetState, grant *accesswait.AccessGrant, value accesswait.WaitHandle) bool {
	if !cleanupWaitIdentityMatches(operation, current, value) {
		return false
	}
	if grant != nil && value.GrantID != grant.GrantID {
		return false
	}
	if !cleanupWaitFenceMatches(current, value) {
		return false
	}
	if !cleanupWaitCursorMatches(current, value) {
		return false
	}
	return cleanupWaitKeyMatches(current, grant, value)
}

func cleanupWaitIdentityMatches(operation Operation, current TargetState, value accesswait.WaitHandle) bool {
	return value.WaitHandleID == *current.ResolvedWaitHandleID && value.CallerPrincipalRef == operation.CallerPrincipalRef && value.ScopeRef == operation.ScopeRef && value.SourceAgentID == operation.Source.AgentID
}

func cleanupWaitFenceMatches(current TargetState, value accesswait.WaitHandle) bool {
	return reflect.DeepEqual(value.Target, current.Target) && reflect.DeepEqual(value.Selector, current.Selector)
}

func cleanupWaitCursorMatches(current TargetState, value accesswait.WaitHandle) bool {
	return value.AfterSequence == current.AfterSequence && value.AfterCursor == current.AfterCursor && value.DeadlineAt.Equal(current.DeadlineAt)
}

// cleanupWaitKeyMatches 校验 wait 幂等键、请求摘要和 grant revision 上界。
func cleanupWaitKeyMatches(current TargetState, grant *accesswait.AccessGrant, value accesswait.WaitHandle) bool {
	if value.IdempotencyNamespace != current.WaitNamespace || value.IdempotencyKeyDigest != current.WaitKeyDigest || value.RequestDigest != current.WaitRequestDigest || value.GrantRevision <= 0 {
		return false
	}
	return grant == nil || value.GrantRevision <= grant.Revision
}

// cleanupStateFromResources 将实际资源 revision/status 投影到下一条 journal。
func cleanupStateFromResources(current TargetState, grant *accesswait.AccessGrant, wait *accesswait.WaitHandle) TargetState {
	result := current
	if grant != nil {
		result.GrantRevision, result.GrantStatus = grant.Revision, grant.Status
	}
	if wait != nil {
		result.WaitRevision, result.WaitStatus = wait.Revision, wait.Status
	}
	return result
}

// appendCleanupCleared 写入资源已清理并保留原始引用的终态 head。
func (s *Store) appendCleanupCleared(ctx context.Context, q *storedb.Queries, request CleanupTargetRequest, current TargetState, now time.Time) (TargetState, error) {
	return s.appendTargetTx(ctx, q, TargetTransitionRequest{
		OperationKey: request.OperationKey, OperationDigest: request.OperationDigest, TargetKey: request.TargetKey,
		ExpectedVersion: current.Version, ExpectedPhase: "cleanup_pending", ExpectedRecordDigest: current.RecordDigest,
		ResolvedGrantID: cloneString(current.ResolvedGrantID), GrantRevision: current.GrantRevision, GrantStatus: current.GrantStatus,
		ResolvedWaitHandleID: cloneString(current.ResolvedWaitHandleID), WaitRevision: current.WaitRevision, WaitStatus: current.WaitStatus,
		GrantCleanupState: "cleared", WaitCleanupState: "cleared", NextPhase: "cleanup_cleared", UncertainPhase: "cleanup",
	}, now)
}
