package waitoperation

import (
	"context"
	"database/sql"
	"errors"
	"time"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// appendTargetOperation 读取目标头并执行一次事务内转换。
func (s *Store) appendTargetOperation(ctx context.Context, q *storedb.Queries, request TargetTransitionRequest, recordedAt time.Time, result *TargetState) error {
	current, err := q.GetOrchestrationWaitTargetHead(ctx, storedb.GetOrchestrationWaitTargetHeadParams{OperationKey: request.OperationKey, TargetKey: request.TargetKey})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTargetNotFound
	}
	if err != nil {
		return classifyDBError("read wait operation target", err, ErrTargetConflict)
	}
	if current.OperationDigest != request.OperationDigest {
		return ErrReplayConflict
	}
	if current.Version > request.ExpectedVersion && current.RecordDigest == request.RecordDigest {
		replayed, replayErr := replayAppendTarget(ctx, q, current, request)
		if replayErr != nil {
			return replayErr
		}
		if replayed {
			*result, err = targetFromRow(current)
			return err
		}
		return ErrTargetConflict
	}
	if s.fenced.Load() {
		return ErrFenced
	}
	if err := validateCurrentTargetPredecessor(current, request); err != nil {
		return err
	}
	*result, err = s.appendTargetTx(ctx, q, request, recordedAt)
	return err
}

// validateCurrentTargetPredecessor 校验当前 target 头的完整 CAS 前驱。
func validateCurrentTargetPredecessor(current storedb.OrchestrationWaitOperationTargetJournal, request TargetTransitionRequest) error {
	if current.Version != request.ExpectedVersion || current.Phase != request.ExpectedPhase || current.RecordDigest != request.ExpectedRecordDigest {
		return ErrTargetConflict
	}
	return nil
}

// replayAppendTarget 校验 target successor 是否匹配请求声明的精确前驱。
func replayAppendTarget(ctx context.Context, q *storedb.Queries, current storedb.OrchestrationWaitOperationTargetJournal, request TargetTransitionRequest) (bool, error) {
	if current.Version <= request.ExpectedVersion || current.RecordDigest != request.RecordDigest {
		return false, nil
	}
	return appendReplayPredecessorMatches(ctx, q, request)
}

// appendReplayPredecessorMatches 证明重放同时覆盖 predecessor 与 successor 元数据。
func appendReplayPredecessorMatches(ctx context.Context, q *storedb.Queries, request TargetTransitionRequest) (bool, error) {
	rows, err := q.ListOrchestrationWaitTargetJournal(ctx, storedb.ListOrchestrationWaitTargetJournalParams{OperationKey: request.OperationKey})
	if err != nil {
		return false, classifyDBError("read append target replay history", err, ErrTargetConflict)
	}
	predecessor := false
	successor := false
	for _, row := range rows {
		if appendPredecessorMatches(row, request) {
			if _, validationErr := targetFromRow(row); validationErr != nil {
				return false, validationErr
			}
			predecessor = true
		}
		successor = successor || appendSuccessorMatches(row, request)
	}
	return predecessor && successor, nil
}

// appendSuccessorMatches 校验重放 successor 的版本、摘要与目标阶段。
func appendSuccessorMatches(row storedb.OrchestrationWaitOperationTargetJournal, request TargetTransitionRequest) bool {
	return row.OperationDigest == request.OperationDigest && row.TargetKey == request.TargetKey && row.Version == request.ExpectedVersion+1 && row.RecordDigest == request.RecordDigest && row.PreviousRecordDigest == request.ExpectedRecordDigest && row.Phase == request.NextPhase && row.UncertainPhase == request.UncertainPhase
}
