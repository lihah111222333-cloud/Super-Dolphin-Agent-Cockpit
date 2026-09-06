package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

var errTransactionCallbackReplay = errors.New("event store transaction callback replay is fenced")

// ErrTransactionCommitUnknown 标识 callback 已成功但事务提交结果无法确认。
var ErrTransactionCommitUnknown = errors.New("event store transaction commit outcome is unknown")

// WithTransaction 在单个 SQLite IMMEDIATE 事务中执行 callback。
// callback 成功后若提交结果不明，后续 busy 重试不会再次执行 callback。
func (s *Store) WithTransaction(ctx context.Context, callback func(storedb.DBTX) error) error {
	if ctx == nil {
		return errors.New("event store transaction context is required")
	}
	if callback == nil {
		return errors.New("event store transaction callback is required")
	}
	if err := s.ready(ctx); err != nil {
		return err
	}
	callbackRan, callbackSucceeded := false, false
	var callbackErr error
	var terminalErr error
	err := sqlctx.WithImmediateTx(ctx, s.db, s.queries, func(_ *storedb.Queries, txdb storedb.DBTX) error {
		if callbackSucceeded {
			return errTransactionCallbackReplay
		}
		callbackRan = true
		callbackErr = callback(txdb)
		// appendEventTx uses committedEventError for state-machine outcomes
		// (gap/quarantine) whose durable transition must survive while the
		// business error is reported after COMMIT. Treat that marker as a
		// successful callback for transaction purposes; otherwise the generic
		// transaction owner would roll back the very quarantine evidence it
		// is required to preserve.
		if _, ok := errors.AsType[*committedEventError](callbackErr); ok {
			// Keep the complete callback error chain (including any adapter
			// classification such as runtimeeventwriter.ErrGap) while using
			// the marker only to decide that the transaction must commit.
			terminalErr = callbackErr
			callbackSucceeded = true
			return nil
		}
		callbackSucceeded = callbackErr == nil
		return callbackErr
	})
	if err == nil {
		return terminalErr
	}
	if callbackSucceeded || (callbackRan && callbackErr == nil) {
		return errors.Join(ErrTransactionCommitUnknown, err)
	}
	return err
}

// GetSourceStateTx 在调用方事务中读取 source/lane 水位，不创建缺失 state。
func (s *Store) GetSourceStateTx(
	ctx context.Context,
	dbtx storedb.DBTX,
	sourceID, sourceEpoch string,
	lane Lane,
) (SourceState, error) {
	if err := s.validateTransaction(ctx, dbtx); err != nil {
		return SourceState{}, err
	}
	if err := validateSourceKey(sourceID, sourceEpoch, lane); err != nil {
		return SourceState{}, err
	}
	row, err := storedb.New(dbtx).GetOrchestrationEventSourceState(ctx, storedb.GetOrchestrationEventSourceStateParams{
		SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: string(lane),
	})
	if err != nil {
		return SourceState{}, classifyEventDBError("get event source state in transaction", err)
	}
	return sourceStateFromRow(row)
}

// AppendTx 在调用方已持有的事务中执行 canonical append 状态机。
// 它不会调用 Append 或再次开启事务；身份与 source fence 由同一事务候选先行校验。
func (s *Store) AppendTx(ctx context.Context, dbtx storedb.DBTX, frame IngressFrame) (AppendResult, error) {
	if err := s.validateTransaction(ctx, dbtx); err != nil {
		return AppendResult{}, err
	}
	if err := frame.Validate(); err != nil {
		return AppendResult{}, err
	}
	if err := validateCanonicalEnvelopeHash(frame); err != nil {
		return AppendResult{}, err
	}
	// AppendTx is a canonical owner entry point, not a way to smuggle a
	// pre-checked frame through an arbitrary transaction.  Re-read the full
	// source/identity fence on the caller's DBTX before any source row is
	// created or mutated.  A bound frame cannot use the legacy envelope-only
	// verifier because that would reintroduce a split-read TOCTOU window.
	verifier, ok := s.verifier.(IngressFenceVerifierTx)
	if !ok {
		return AppendResult{}, fmt.Errorf("%w: transactional ingress fence verifier is required", ErrEventFence)
	}
	if err := verifier.VerifyIngressFrameFenceTx(ctx, dbtx, frame); err != nil {
		return AppendResult{}, errors.Join(ErrEventFence, err)
	}
	return appendEventTx(ctx, storedb.New(dbtx), frame)
}

// AppendAllocatedTx is a candidate compatibility API; callers still build the
// frame around the supplied sequence and therefore must not be used as the
// production ingress seam. Use AppendAllocatedEnvelopeTx for owner-derived
// sequence, cursor and hash.
//
// AppendAllocatedTx 在调用方事务中分配下一个 source sequence 并追加一帧。
// builder 只能看到持久化分配的序号；source fence 与 canonical 派生仍在同一 DBTX 校验。
func (s *Store) AppendAllocatedTx(
	ctx context.Context,
	dbtx storedb.DBTX,
	sourceID, sourceEpoch string,
	lane Lane,
	writerEpoch string,
	build func(int64) (IngressFrame, error),
) (AppendResult, error) {
	if err := s.validateTransaction(ctx, dbtx); err != nil {
		return AppendResult{}, err
	}
	if err := validateSourceKey(sourceID, sourceEpoch, lane); err != nil {
		return AppendResult{}, err
	}
	if err := validateText("writer_epoch", writerEpoch, 128, false); err != nil {
		return AppendResult{}, err
	}
	if build == nil {
		return AppendResult{}, errors.New("event store sequence allocator frame builder is required")
	}
	q := storedb.New(dbtx)
	state, err := sourceStateForAllocation(ctx, q, sourceID, sourceEpoch, lane, writerEpoch)
	if err != nil {
		return AppendResult{}, err
	}
	if err := validateAllocatedSourceState(state, sourceID, sourceEpoch, lane, writerEpoch); err != nil {
		return AppendResult{}, err
	}
	frame, err := build(state.NextSequence)
	if err != nil {
		return AppendResult{}, err
	}
	if err := validateAllocatedFrame(frame, sourceID, sourceEpoch, lane, writerEpoch, state.NextSequence); err != nil {
		return AppendResult{}, err
	}
	if err := s.verifyAllocatedFrameFenceTx(ctx, dbtx, frame); err != nil {
		return AppendResult{}, err
	}
	return appendEventTx(ctx, q, frame)
}

// validateAllocatedFrame 验证分配帧的 source tuple、序号及 canonical 派生字段。
func validateAllocatedFrame(frame IngressFrame, sourceID, sourceEpoch string, lane Lane, writerEpoch string, sequence int64) error {
	if err := validateAllocatedFrameIdentity(frame, sourceID, sourceEpoch, lane, writerEpoch, sequence); err != nil {
		return err
	}
	if err := frame.Validate(); err != nil {
		return err
	}
	return validateAllocatedCanonicalFields(frame, sourceID, sourceEpoch, lane, sequence)
}

// validateAllocatedFrameIdentity 校验分配帧不能伪造 source tuple 或 writer epoch。
func validateAllocatedFrameIdentity(frame IngressFrame, sourceID, sourceEpoch string, lane Lane, writerEpoch string, sequence int64) error {
	if frame.SourceID != sourceID || frame.SourceEpoch != sourceEpoch || frame.Lane != lane || frame.SourceSequence != sequence || frame.Envelope.WriterEpoch != writerEpoch {
		return fmt.Errorf("%w: allocated frame does not match source or sequence", ErrEventFence)
	}
	return nil
}

// validateAllocatedCanonicalFields 校验 envelope hash 与 cursor 均由 owner 规范派生。
func validateAllocatedCanonicalFields(frame IngressFrame, sourceID, sourceEpoch string, lane Lane, sequence int64) error {
	hash, err := CanonicalEnvelopeHash(frame.Envelope)
	if err != nil {
		return err
	}
	if frame.EnvelopeHash != hash {
		return fmt.Errorf("%w: allocated frame envelope hash is not canonical", ErrEventFence)
	}
	cursor, err := CanonicalCursor(sourceID, sourceEpoch, lane, sequence)
	if err != nil {
		return err
	}
	if frame.CanonicalCursor != cursor {
		return fmt.Errorf("%w: allocated frame cursor is not canonical", ErrEventFence)
	}
	return nil
}

// verifyAllocatedFrameFenceTx 在调用方 DBTX 上重新执行 ingress identity fence。
func (s *Store) verifyAllocatedFrameFenceTx(ctx context.Context, dbtx storedb.DBTX, frame IngressFrame) error {
	verifier, ok := s.verifier.(IngressFenceVerifierTx)
	if !ok {
		return fmt.Errorf("%w: transactional ingress fence verifier is required", ErrEventFence)
	}
	if err := verifier.VerifyIngressFrameFenceTx(ctx, dbtx, frame); err != nil {
		return errors.Join(ErrEventFence, err)
	}
	return nil
}

// AppendAllocatedEnvelopeTx 是 envelope-only 的 canonical allocator seam；sequence、cursor 和 hash 均由 EventStore 派生。
func (s *Store) AppendAllocatedEnvelopeTx(
	ctx context.Context,
	dbtx storedb.DBTX,
	sourceID, sourceEpoch string,
	lane Lane,
	writerEpoch string,
	build func() (EventEnvelope, error),
) (AppendResult, error) {
	if build == nil {
		return AppendResult{}, errors.New("event store envelope builder is required")
	}
	return s.AppendAllocatedTx(ctx, dbtx, sourceID, sourceEpoch, lane, writerEpoch, func(sequence int64) (IngressFrame, error) {
		envelope, err := build()
		if err != nil {
			return IngressFrame{}, err
		}
		if err := envelope.Validate(); err != nil {
			return IngressFrame{}, err
		}
		hash, err := CanonicalEnvelopeHash(envelope)
		if err != nil {
			return IngressFrame{}, err
		}
		cursor, err := CanonicalCursor(sourceID, sourceEpoch, lane, sequence)
		if err != nil {
			return IngressFrame{}, err
		}
		return IngressFrame{SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: lane,
			SourceSequence: sequence, EnvelopeHash: hash, CanonicalCursor: cursor, Envelope: envelope}, nil
	})
}

// validateAllocatedSourceState 拒绝无法证明单调分配或 dense append 的持久化状态。
func validateAllocatedSourceState(state SourceState, sourceID, sourceEpoch string, lane Lane, writerEpoch string) error {
	if state.SourceID != sourceID || state.SourceEpoch != sourceEpoch || state.Lane != lane || state.WriterEpoch != writerEpoch {
		return ErrEventFence
	}
	if err := validateAllocationStatus(state.Status, lane); err != nil {
		return err
	}
	return validateAllocationSequence(state)
}

// validateAllocationStatus 校验 allocator 可接受的 source 生命周期状态。
func validateAllocationStatus(status string, lane Lane) error {
	if status == "active" {
		return nil
	}
	if status == "gap" {
		if lane == LaneControl {
			return ErrEventQuarantined
		}
		return nil
	}
	if status == "quarantined" || status == "closed" {
		return ErrEventQuarantined
	}
	return ErrEventFence
}

// validateAllocationSequence 校验 source sequence、materialized 水位和 dedup floor 的单调关系。
func validateAllocationSequence(state SourceState) error {
	if state.NextSequence < 1 || state.MaterializedSequence < 0 || state.DedupFloor < 0 || state.Revision < 1 {
		return ErrEventFence
	}
	if state.MaterializedSequence >= state.NextSequence || state.NextSequence != state.MaterializedSequence+1 {
		return ErrEventFence
	}
	if state.DedupFloor > state.MaterializedSequence {
		return ErrEventFence
	}
	return nil
}

// sourceStateForAllocation reads an existing state, or returns an in-memory
// sequence-one candidate after checking epoch registration. It deliberately
// does not insert a row until envelope/fence validation has succeeded.
func sourceStateForAllocation(ctx context.Context, q *storedb.Queries, sourceID, sourceEpoch string, lane Lane, writerEpoch string) (SourceState, error) {
	row, err := q.GetOrchestrationEventSourceState(ctx, storedb.GetOrchestrationEventSourceStateParams{SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: string(lane)})
	if err == nil {
		return sourceStateFromRow(row)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SourceState{}, classifyEventDBError("read event source state for allocation", err)
	}
	if err := rejectUnregisteredSourceEpoch(ctx, q, IngressFrame{SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: lane}); err != nil {
		return SourceState{}, err
	}
	return SourceState{SourceID: sourceID, SourceEpoch: sourceEpoch, Lane: lane, NextSequence: 1, MaterializedSequence: 0, DedupFloor: 0, Revision: 1, Status: "active", WriterEpoch: writerEpoch}, nil
}

func (s *Store) validateTransaction(ctx context.Context, dbtx storedb.DBTX) error {
	if ctx == nil {
		return errors.New("event store transaction context is required")
	}
	if err := s.ready(ctx); err != nil {
		return err
	}
	return sqlctx.RequireTransactionDBTX(dbtx)
}
