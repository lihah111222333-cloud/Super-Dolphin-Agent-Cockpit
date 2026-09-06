package orchestrationcommand

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	modernsqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// prepareLifecycle 只创建一次 custody-pending genesis；重复调用只查询原操作结果。
func (s *Store) prepareLifecycle(ctx context.Context, request TransitionRequest, digest string) (Snapshot, error) {
	if err := validateLifecyclePreparation(request, digest); err != nil {
		return Snapshot{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Snapshot{}, err
	}
	row, err := s.queries.PrepareOrchestrationCommandJournal(ctx, prepareLifecycleParams(request, digest))
	if err == nil {
		return snapshotFromRow(row)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, fmt.Errorf("prepare command lifecycle: %w", err)
	}
	prior, lookupErr := s.GetOperation(ctx, request.CommandID, request.OperationID)
	if lookupErr == nil {
		return matchOperation(prior, digest)
	}
	if !errors.Is(lookupErr, ErrLifecycleNotFound) {
		return Snapshot{}, lookupErr
	}
	if _, stateErr := s.GetState(ctx, request.CommandID); stateErr == nil {
		return Snapshot{}, ErrTransitionConflict
	} else if !errors.Is(stateErr, ErrLifecycleNotFound) {
		return Snapshot{}, stateErr
	}
	return Snapshot{}, ErrLifecycleNotFound
}

// appendLifecycle 只接受 Coordinator 已验证的转换，并以数据库版本与字段快照做原子 CAS。
func (s *Store) appendLifecycle(ctx context.Context, plan verifiedTransition) (Snapshot, error) {
	if err := validateVerifiedTransition(ctx, plan); err != nil {
		return Snapshot{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Snapshot{}, err
	}
	row, err := s.queries.AppendOrchestrationCommandJournal(ctx, appendLifecycleParams(plan))
	if err == nil {
		return snapshotFromRow(row)
	}
	if !isJournalCASConflict(err) {
		return Snapshot{}, fmt.Errorf("append command lifecycle: %w", err)
	}
	return s.resolveLifecycleConflict(ctx, plan.Request.CommandID, plan.Request.OperationID, plan.OperationDigest)
}

// resolveLifecycleConflict 在 CAS 或预读竞争后查原操作；不会把新状态当作原请求的成功结果。
func (s *Store) resolveLifecycleConflict(ctx context.Context, commandID, operationID, digest string) (Snapshot, error) {
	prior, lookupErr := s.GetOperation(ctx, commandID, operationID)
	if lookupErr == nil {
		return matchOperation(prior, digest)
	}
	if !errors.Is(lookupErr, ErrLifecycleNotFound) {
		return Snapshot{}, lookupErr
	}
	return Snapshot{}, ErrTransitionConflict
}

// isJournalCASConflict 仅分类零行 CAS 和唯一约束失败；其它数据库故障原样上报且不重试。
func isJournalCASConflict(err error) bool {
	if errors.Is(err, sql.ErrNoRows) {
		return true
	}
	var sqliteErr *modernsqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

// GetState 读取命令日志的最新不可变快照；缺少 genesis 时返回 ErrLifecycleNotFound。
func (s *Store) GetState(ctx context.Context, commandID string) (Snapshot, error) {
	if err := validateText("command_id", commandID, 128); err != nil {
		return Snapshot{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Snapshot{}, err
	}
	row, err := s.queries.GetOrchestrationCommandJournal(ctx, storedb.GetOrchestrationCommandJournalParams{CommandID: commandID})
	if err != nil {
		return Snapshot{}, classifyJournalReadError("get command lifecycle state", err)
	}
	return s.snapshotWithPredecessor(ctx, row)
}

// GetOperation 按 command 与 operation 精确读取历史结果，不将最新状态冒充重放结果。
func (s *Store) GetOperation(ctx context.Context, commandID, operationID string) (Snapshot, error) {
	if err := validateText("command_id", commandID, 128); err != nil {
		return Snapshot{}, err
	}
	if err := validateText("operation_id", operationID, 128); err != nil {
		return Snapshot{}, err
	}
	if err := s.ready(ctx); err != nil {
		return Snapshot{}, err
	}
	row, err := s.queries.GetOrchestrationCommandOperation(ctx, storedb.GetOrchestrationCommandOperationParams{
		CommandID: commandID, OperationID: operationID,
	})
	if err != nil {
		return Snapshot{}, classifyJournalReadError("get command lifecycle operation", err)
	}
	return s.snapshotWithPredecessor(ctx, row)
}

// History 按版本升序读取有限历史；游标之后暂时没有新记录时返回空集合。
func (s *Store) History(ctx context.Context, commandID string, afterVersion int64, limit int) ([]Snapshot, error) {
	if err := validateText("command_id", commandID, 128); err != nil {
		return nil, err
	}
	if afterVersion < 0 || limit < 1 || limit > 200 {
		return nil, errors.New("command lifecycle history requires after_version >= 0 and limit 1..200")
	}
	if err := s.ready(ctx); err != nil {
		return nil, err
	}
	current, err := s.GetState(ctx, commandID)
	if err != nil {
		return nil, err
	}
	if afterVersion > current.Version {
		return nil, ErrTransitionConflict
	}
	rows, err := s.queries.ListOrchestrationCommandJournal(ctx, storedb.ListOrchestrationCommandJournalParams{
		CommandID: commandID, AfterVersion: afterVersion, Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list command lifecycle history: %w", err)
	}
	if len(rows) == 0 {
		return []Snapshot{}, nil
	}
	return s.snapshotsFromRows(ctx, rows)
}

// snapshotsFromRows 显式逐行映射并在任一损坏记录处阻断整页读取。
func (s *Store) snapshotsFromRows(ctx context.Context, rows []storedb.OrchestrationCommandJournal) ([]Snapshot, error) {
	result := make([]Snapshot, 0, len(rows))
	for index, row := range rows {
		snapshot, err := storedSnapshot(row)
		if err != nil {
			return nil, err
		}
		if index == 0 {
			err = s.validateJournalPredecessor(ctx, snapshot)
		} else {
			err = validateJournalLink(ctx, result[index-1], snapshot)
		}
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}
	return result, nil
}

// validateLifecyclePreparation 约束唯一 genesis 请求不携带派发或证据字段。
func validateLifecyclePreparation(request TransitionRequest, digest string) error {
	if err := validateText("command_id", request.CommandID, 128); err != nil {
		return err
	}
	if err := validateText("operation_id", request.OperationID, 128); err != nil {
		return err
	}
	if request.ExpectedVersion != 0 || request.Trigger != TriggerPrepare || request.AttemptID != "" || request.EvidenceRef != "" {
		return ErrInvalidTransition
	}
	return validateLifecycleDigest(request, digest)
}

// validateVerifiedTransition 复验快照并要求计划与唯一状态机规则完整相等。
func validateVerifiedTransition(ctx context.Context, plan verifiedTransition) error {
	r := plan.Request
	if err := validateTransitionRequest(r); err != nil {
		return err
	}
	if err := validateLifecycleDigest(r, plan.OperationDigest); err != nil {
		return err
	}
	if plan.Current.CommandID != r.CommandID || plan.Current.Version != r.ExpectedVersion {
		return ErrTransitionConflict
	}
	if err := validateSnapshot(plan.Current); err != nil {
		return err
	}
	expected, err := planTransition(ctx, plan.Current, r, plan.OperationDigest)
	if err != nil {
		return err
	}
	if plan != expected {
		return ErrInvalidTransition
	}
	return nil
}

// validateLifecycleDigest 从原请求重算摘要，拒绝形状正确但绑定其它请求的私有写入计划。
func validateLifecycleDigest(request TransitionRequest, digest string) error {
	if err := validateDigest("operation_digest", digest); err != nil {
		return err
	}
	expected, err := transitionDigest(request)
	if err != nil {
		return err
	}
	if digest != expected {
		return ErrInvalidTransition
	}
	return nil
}

func classifyJournalReadError(operation string, err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLifecycleNotFound
	}
	return fmt.Errorf("%s: %w", operation, err)
}
