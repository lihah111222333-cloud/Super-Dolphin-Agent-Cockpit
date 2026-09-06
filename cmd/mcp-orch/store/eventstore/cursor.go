package eventstore

import (
	"context"
	"database/sql"
	"errors"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// EnsureCursor 显式创建投影初始 cursor；重复调用只返回同一版本。
func (s *Store) EnsureCursor(ctx context.Context, key CursorKey) (MaterializationCursor, error) {
	if err := key.Validate(); err != nil {
		return MaterializationCursor{}, err
	}
	var result MaterializationCursor
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		_, err := q.GetOrchestrationEventSourceState(ctx, storedb.GetOrchestrationEventSourceStateParams{
			SourceID: key.SourceID, SourceEpoch: key.SourceEpoch, Lane: string(key.Lane),
		})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrEventNotFound
		}
		if err != nil {
			return classifyEventDBError("check cursor source", err)
		}
		row, err := q.GetOrchestrationEventMaterializationCursor(ctx, cursorParams(key))
		if err == nil {
			result, err = materializationCursorFromRow(row)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return classifyEventDBError("read materialization cursor", err)
		}
		row, err = q.InsertOrchestrationEventMaterializationCursor(ctx, storedb.InsertOrchestrationEventMaterializationCursorParams{
			ProjectionID: key.ProjectionID, SourceID: key.SourceID, SourceEpoch: key.SourceEpoch,
			Lane: string(key.Lane), MaterializedSequence: 0, MaterializedHash: "", Status: "active",
		})
		if err != nil {
			return classifyEventDBError("create materialization cursor", err)
		}
		result, err = materializationCursorFromRow(row)
		return err
	})
	return result, err
}

// GetCursor 读取投影 cursor，不为缺失 source 或 cursor 补建数据。
func (s *Store) GetCursor(ctx context.Context, key CursorKey) (MaterializationCursor, error) {
	if err := key.Validate(); err != nil {
		return MaterializationCursor{}, err
	}
	if err := s.ready(ctx); err != nil {
		return MaterializationCursor{}, err
	}
	row, err := s.queries.GetOrchestrationEventMaterializationCursor(ctx, cursorParams(key))
	if err != nil {
		return MaterializationCursor{}, classifyEventDBError("get materialization cursor", err)
	}
	return materializationCursorFromRow(row)
}

// AdvanceCursor 只允许在 source 已 materialize 的范围内以 revision CAS 推进。
func (s *Store) AdvanceCursor(ctx context.Context, key CursorKey, expectedRevision, sequence int64, hash, status string) (MaterializationCursor, error) {
	if err := key.Validate(); err != nil {
		return MaterializationCursor{}, err
	}
	if expectedRevision <= 0 || sequence < 0 || !knownCursorStatus(status) {
		return MaterializationCursor{}, invalidEvent("cursor revision/sequence/status is invalid")
	}
	if err := validateDigest("materialized_hash", hash, sequence > 0); err != nil {
		return MaterializationCursor{}, err
	}
	var result MaterializationCursor
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		var err error
		result, err = advanceCursorTx(ctx, q, key, expectedRevision, sequence, hash, status)
		return err
	})
	return result, err
}

// advanceCursorTx 在写事务内复核 source 水位并执行 cursor CAS。
func advanceCursorTx(ctx context.Context, q *storedb.Queries, key CursorKey, expectedRevision, sequence int64, hash, status string) (MaterializationCursor, error) {
	state, cursor, err := loadCursorContext(ctx, q, key)
	if err != nil {
		return MaterializationCursor{}, err
	}
	if err := validateCursorAgainstSource(state, sequence); err != nil {
		return MaterializationCursor{}, err
	}
	if cursor.Revision == expectedRevision && cursor.MaterializedSequence == sequence && cursor.MaterializedHash == hash && cursor.Status == status {
		return cursor, nil
	}
	if !cursorAdvanceAllowed(cursor, expectedRevision, sequence) {
		return MaterializationCursor{}, ErrEventFence
	}
	return writeCursorAdvance(ctx, q, key, expectedRevision, sequence, hash, status)
}

// loadCursorContext 一次读取 source watermark 与投影 cursor，供 CAS 复用。
func loadCursorContext(ctx context.Context, q *storedb.Queries, key CursorKey) (SourceState, MaterializationCursor, error) {
	stateRow, err := q.GetOrchestrationEventSourceState(ctx, storedb.GetOrchestrationEventSourceStateParams{SourceID: key.SourceID, SourceEpoch: key.SourceEpoch, Lane: string(key.Lane)})
	if err != nil {
		return SourceState{}, MaterializationCursor{}, classifyEventDBError("check cursor source watermark", err)
	}
	state, err := sourceStateFromRow(stateRow)
	if err != nil {
		return SourceState{}, MaterializationCursor{}, err
	}
	cursorRow, err := q.GetOrchestrationEventMaterializationCursor(ctx, cursorParams(key))
	if err != nil {
		return SourceState{}, MaterializationCursor{}, classifyEventDBError("load materialization cursor", err)
	}
	cursor, err := materializationCursorFromRow(cursorRow)
	return state, cursor, err
}

func writeCursorAdvance(ctx context.Context, q *storedb.Queries, key CursorKey, expectedRevision, sequence int64, hash, status string) (MaterializationCursor, error) {
	affected, err := q.UpdateOrchestrationEventMaterializationCursor(ctx, storedb.UpdateOrchestrationEventMaterializationCursorParams{
		MaterializedSequence: sequence, MaterializedHash: hash, NextRevision: expectedRevision + 1,
		Status: status, ProjectionID: key.ProjectionID, SourceID: key.SourceID, SourceEpoch: key.SourceEpoch,
		Lane: string(key.Lane), ExpectedRevision: expectedRevision,
	})
	if err != nil {
		return MaterializationCursor{}, classifyEventDBError("advance materialization cursor", err)
	}
	if err := requireEventRows("advance materialization cursor", affected); err != nil {
		return MaterializationCursor{}, err
	}
	row, err := q.GetOrchestrationEventMaterializationCursor(ctx, cursorParams(key))
	if err != nil {
		return MaterializationCursor{}, classifyEventDBError("read advanced materialization cursor", err)
	}
	return materializationCursorFromRow(row)
}

func validateCursorAgainstSource(state SourceState, sequence int64) error {
	if sequence > state.MaterializedSequence {
		return ErrEventFence
	}
	return nil
}

func cursorAdvanceAllowed(cursor MaterializationCursor, expectedRevision, sequence int64) bool {
	return cursor.Revision == expectedRevision && sequence >= cursor.MaterializedSequence
}

// RecordTombstone 固化一个连续 dedup floor，保留 hash/cursor 以拒绝迟到重放。
func (s *Store) RecordTombstone(ctx context.Context, req TombstoneRequest) (Tombstone, error) {
	if err := req.Validate(); err != nil {
		return Tombstone{}, err
	}
	var result Tombstone
	err := s.withWriteTx(ctx, func(q *storedb.Queries, dbtx storedb.DBTX) error {
		var err error
		result, err = s.recordTombstoneTx(ctx, q, dbtx, req)
		return err
	})
	return result, err
}

// recordTombstoneTx 在同一事务中校验 materialized receipt 并推进 dedup floor。
func (s *Store) recordTombstoneTx(ctx context.Context, q *storedb.Queries, dbtx storedb.DBTX, req TombstoneRequest) (Tombstone, error) {
	state, err := loadTombstoneSourceState(ctx, q, req)
	if err != nil {
		return Tombstone{}, err
	}
	if existing, found, lookupErr := getExistingTombstone(ctx, q, req); lookupErr != nil {
		return Tombstone{}, lookupErr
	} else if found {
		return replayTombstone(existing, req)
	}
	if !tombstoneFloorAllowed(state, req) {
		return Tombstone{}, ErrEventFence
	}
	receipt, err := loadTombstoneReceipt(ctx, q, req)
	if err != nil {
		return Tombstone{}, err
	}
	if !tombstoneReceiptMatches(receipt, req) {
		return Tombstone{}, ErrEventFence
	}
	if s.retentionPin != nil {
		if err := s.retentionPin.VerifyRetentionPinTx(ctx, dbtx, req); err != nil {
			return Tombstone{}, errors.Join(ErrEventFence, err)
		}
	}
	return insertTombstoneAndAdvance(ctx, q, state, req)
}

func loadTombstoneSourceState(ctx context.Context, q *storedb.Queries, req TombstoneRequest) (SourceState, error) {
	row, err := q.GetOrchestrationEventSourceState(ctx, storedb.GetOrchestrationEventSourceStateParams{SourceID: req.SourceID, SourceEpoch: req.SourceEpoch, Lane: string(req.Lane)})
	if err != nil {
		return SourceState{}, classifyEventDBError("load tombstone source state", err)
	}
	return sourceStateFromRow(row)
}

func loadTombstoneReceipt(ctx context.Context, q *storedb.Queries, req TombstoneRequest) (IngressReceipt, error) {
	row, err := q.GetOrchestrationEventIngressReceipt(ctx, receiptKey(IngressFrame{SourceID: req.SourceID, SourceEpoch: req.SourceEpoch, Lane: req.Lane, SourceSequence: req.SourceSequence}))
	if err != nil {
		return IngressReceipt{}, classifyEventDBError("check tombstone receipt", err)
	}
	return ingressReceiptFromRow(row)
}

func insertTombstoneAndAdvance(ctx context.Context, q *storedb.Queries, state SourceState, req TombstoneRequest) (Tombstone, error) {
	row, err := q.InsertOrchestrationEventTombstone(ctx, storedb.InsertOrchestrationEventTombstoneParams{
		SourceID: req.SourceID, SourceEpoch: req.SourceEpoch, Lane: string(req.Lane), SourceSequence: req.SourceSequence,
		EnvelopeHash: req.EnvelopeHash, EventID: req.EventID, CanonicalCursor: req.CanonicalCursor,
		DedupFloor: req.SourceSequence, Status: req.Status,
	})
	if err != nil {
		return Tombstone{}, classifyEventDBError("insert event tombstone", err)
	}
	result, err := tombstoneFromRow(row)
	if err != nil {
		return Tombstone{}, err
	}
	_, err = updateSourceState(ctx, q, state, state.NextSequence, state.MaterializedSequence, req.SourceSequence, state.Status, state.GapReason)
	return result, err
}

func getExistingTombstone(ctx context.Context, q *storedb.Queries, req TombstoneRequest) (Tombstone, bool, error) {
	row, err := q.GetOrchestrationEventTombstone(ctx, storedb.GetOrchestrationEventTombstoneParams{SourceID: req.SourceID, SourceEpoch: req.SourceEpoch, Lane: string(req.Lane), SourceSequence: req.SourceSequence})
	if errors.Is(err, sql.ErrNoRows) {
		return Tombstone{}, false, nil
	}
	if err != nil {
		return Tombstone{}, false, classifyEventDBError("look up event tombstone", err)
	}
	result, err := tombstoneFromRow(row)
	return result, true, err
}

func replayTombstone(existing Tombstone, req TombstoneRequest) (Tombstone, error) {
	if existing.EnvelopeHash != req.EnvelopeHash || existing.EventID != req.EventID || existing.CanonicalCursor != req.CanonicalCursor || existing.Status != req.Status {
		return Tombstone{}, ErrEventConflict
	}
	return existing, nil
}

func tombstoneFloorAllowed(state SourceState, req TombstoneRequest) bool {
	return req.SourceSequence == state.DedupFloor+1 && req.SourceSequence <= state.MaterializedSequence && req.ExpectedFloor == state.DedupFloor
}

func tombstoneReceiptMatches(receipt IngressReceipt, req TombstoneRequest) bool {
	return receipt.EnvelopeHash == req.EnvelopeHash && receipt.Envelope.EventID == req.EventID &&
		receipt.CanonicalCursor == req.CanonicalCursor && receipt.Status == "materialized"
}

func cursorParams(key CursorKey) storedb.GetOrchestrationEventMaterializationCursorParams {
	return storedb.GetOrchestrationEventMaterializationCursorParams{ProjectionID: key.ProjectionID, SourceID: key.SourceID, SourceEpoch: key.SourceEpoch, Lane: string(key.Lane)}
}
