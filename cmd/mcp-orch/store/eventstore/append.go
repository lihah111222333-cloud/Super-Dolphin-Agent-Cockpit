package eventstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

const maxEventDrain = 1024

type committedEventError struct{ err error }

// Error 返回需要在事务提交后交给调用方的业务错误。
func (e *committedEventError) Error() string { return e.err.Error() }

// Unwrap 保留原始 gap/conflict 哨兵供 errors.Is 使用。
func (e *committedEventError) Unwrap() error { return e.err }

func commitEventError(err error) error {
	return &committedEventError{err: err}
}

// Append 接收一个事件候选，并在单个写事务中完成去重、越洞暂存和连续 materialize。
func (s *Store) Append(ctx context.Context, frame IngressFrame) (AppendResult, error) {
	if err := s.ready(ctx); err != nil {
		return AppendResult{}, err
	}
	if err := frame.Validate(); err != nil {
		return AppendResult{}, err
	}
	if err := validateCanonicalEnvelopeHash(frame); err != nil {
		return AppendResult{}, err
	}
	if _, ok := s.verifier.(IngressFenceVerifierTx); ok {
		// The canonical path rechecks the complete source/identity fence on
		// the same IMMEDIATE transaction used for append. Do not retain a
		// split pre-read when the stronger owner seam is available.
		return s.appendValidated(ctx, frame)
	}
	// A bound task event must never fall back to a split identity read. Legacy
	// envelope-only verification remains available only for pre-incarnation
	// observations that carry no task execution fence.
	if frame.Envelope.IncarnationID != nil {
		return AppendResult{}, fmt.Errorf("%w: transactional ingress fence verifier is required", ErrEventFence)
	}
	if verifier, ok := s.verifier.(IngressFenceVerifier); ok {
		if err := verifier.VerifyIngressFrameFence(ctx, frame); err != nil {
			return AppendResult{}, errors.Join(ErrEventFence, err)
		}
	} else if err := s.verifier.VerifyEventFence(ctx, frame.Envelope); err != nil {
		return AppendResult{}, errors.Join(ErrEventFence, err)
	}
	return s.appendValidated(ctx, frame)
}

// appendValidated 在完整 source/envelope fence 已验证后执行 dense append 状态机。
func (s *Store) appendValidated(ctx context.Context, frame IngressFrame) (AppendResult, error) {
	var result AppendResult
	var materializedEvents []AgentEvent
	var terminalErr error
	err := s.withWriteTx(ctx, func(q *storedb.Queries, txdb storedb.DBTX) error {
		if verifier, ok := s.verifier.(IngressFenceVerifierTx); ok {
			if err := verifier.VerifyIngressFrameFenceTx(ctx, txdb, frame); err != nil {
				return errors.Join(ErrEventFence, err)
			}
		}
		var txErr error
		result, txErr = appendEventTxCollect(ctx, q, frame, &materializedEvents)
		committed, isCommitted := errors.AsType[*committedEventError](txErr)
		if isCommitted {
			terminalErr = committed.Unwrap()
			return nil
		}
		return txErr
	})
	if err != nil {
		return AppendResult{}, err
	}
	if len(materializedEvents) > 0 && s.notifier != nil {
		if notifyErr := s.notifyMaterialized(materializedEvents); notifyErr != nil {
			// The canonical rows are already committed. Return the result together
			// with the notification error so callers cannot mistake a local wake-up
			// failure for a rolled-back append.
			return result, notifyErr
		}
	}
	return result, terminalErr
}

// notifyMaterialized 仅在 owner 事务成功返回后发布本次追加 materialize 的事件。
// replay 和 gap 结果不会填充 collector，因此不会唤醒 waiter。
func (s *Store) notifyMaterialized(events []AgentEvent) error {
	if s == nil || eventNotifierIsNil(s.notifier) {
		return errors.New("event store watcher notifier is unavailable")
	}
	var notifyErr error
	for _, event := range events {
		if err := s.notifier.Notify(event); err != nil {
			// A full/closed local subscriber must not prevent later events in
			// this committed batch from reaching healthy subscribers. Every
			// notification is best-effort after COMMIT; aggregate errors only
			// after traversing the complete materialized prefix.
			notifyErr = errors.Join(notifyErr, fmt.Errorf("notify committed canonical event: %w", err))
		}
	}
	return notifyErr
}

// appendEventTx 在已取得 SQLite 写锁后执行单次事件接收状态机。
func appendEventTx(ctx context.Context, q *storedb.Queries, frame IngressFrame) (AppendResult, error) {
	if err := validateCanonicalEnvelopeHash(frame); err != nil {
		return AppendResult{}, err
	}
	return appendEventTxCollect(ctx, q, frame, nil)
}

// appendEventTxCollect 是事务内 append 状态机。collector 由 Append 持有，仅在
// COMMIT 后消费；Tx-compatible 调用方传 nil，因而不会发出提交前唤醒。
func appendEventTxCollect(ctx context.Context, q *storedb.Queries, frame IngressFrame, collector *[]AgentEvent) (AppendResult, error) {
	state, err := getOrCreateSourceState(ctx, q, frame)
	if err != nil {
		return AppendResult{}, err
	}
	if err := validateSourceStateForAppend(state, frame); err != nil {
		return AppendResult{}, err
	}
	if frame.SourceSequence <= state.DedupFloor {
		return replayBelowFloor(ctx, q, frame)
	}
	existing, err := q.GetOrchestrationEventIngressReceipt(ctx, receiptKey(frame))
	if err == nil {
		return appendExistingReceiptCollect(ctx, q, state, frame, existing, collector)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return AppendResult{}, classifyEventDBError("read ingress receipt", err)
	}
	if frame.SourceSequence < state.NextSequence {
		return AppendResult{}, ErrEventFence
	}
	receipt, err := insertStagedReceipt(ctx, q, frame)
	if err != nil {
		return AppendResult{}, err
	}
	if frame.SourceSequence > state.NextSequence {
		if frame.Lane == LaneControl {
			result, err := markControlGap(ctx, q, state, receipt, frame.SourceSequence)
			return result, commitEventError(err)
		}
		result, err := markGap(ctx, q, state, receipt, frame.SourceSequence)
		return result, commitEventError(err)
	}
	return materializeContiguousCollect(ctx, q, state, receipt, collector)
}

func getOrCreateSourceState(ctx context.Context, q *storedb.Queries, frame IngressFrame) (SourceState, error) {
	row, err := q.GetOrchestrationEventSourceState(ctx, sourceStateKey(frame))
	if err == nil {
		return sourceStateFromRow(row)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return SourceState{}, classifyEventDBError("read event source state", err)
	}
	if err := rejectUnregisteredSourceEpoch(ctx, q, frame); err != nil {
		return SourceState{}, err
	}
	row, err = q.CreateOrchestrationEventSourceState(ctx, storedb.CreateOrchestrationEventSourceStateParams{
		SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: string(frame.Lane), WriterEpoch: frame.Envelope.WriterEpoch,
	})
	if err != nil {
		return SourceState{}, classifyEventDBError("create event source state", err)
	}
	return sourceStateFromRow(row)
}

func rejectUnregisteredSourceEpoch(ctx context.Context, q *storedb.Queries, frame IngressFrame) error {
	rows, err := q.ListOrchestrationEventSourceStatesBySource(ctx, storedb.ListOrchestrationEventSourceStatesBySourceParams{SourceID: frame.SourceID})
	if err != nil {
		return classifyEventDBError("check event source epoch", err)
	}
	for _, row := range rows {
		if row.SourceEpoch != frame.SourceEpoch {
			return ErrEventFence
		}
	}
	return nil
}

// validateSourceStateForAppend 校验来源写入者代际及生命周期状态，阻断不允许的追加。
func validateSourceStateForAppend(state SourceState, frame IngressFrame) error {
	if state.WriterEpoch != frame.Envelope.WriterEpoch {
		return ErrEventFence
	}
	switch state.Status {
	case "active":
		return nil
	case "gap":
		// A control-lane hole can affect lifecycle authority. It must be
		// explicitly reconciled/quarantined and may not be auto-drained by
		// an ordinary append.
		if frame.Lane == LaneControl {
			return ErrEventQuarantined
		}
		return nil
	case "quarantined", "closed":
		return ErrEventQuarantined
	default:
		return ErrEventFence
	}
}

// appendExistingReceipt 处理同 source tuple 的重放、哈希冲突和 staged 续接。
func appendExistingReceipt(ctx context.Context, q *storedb.Queries, state SourceState, frame IngressFrame, row storedb.OrchestrationEventIngressReceipt) (AppendResult, error) {
	return appendExistingReceiptCollect(ctx, q, state, frame, row, nil)
}

// appendExistingReceiptCollect 按已有 receipt 的哈希和状态处理冲突、重放或连续 materialize，并收集提交后通知事件。
func appendExistingReceiptCollect(ctx context.Context, q *storedb.Queries, state SourceState, frame IngressFrame, row storedb.OrchestrationEventIngressReceipt, collector *[]AgentEvent) (AppendResult, error) {
	existing, err := ingressReceiptFromRow(row)
	if err != nil {
		return AppendResult{}, err
	}
	if existing.EnvelopeHash != frame.EnvelopeHash {
		return quarantineExistingReceipt(ctx, q, state, existing)
	}
	if existing.Status == "quarantined" {
		return resultForReceipt(state, existing), ErrEventQuarantined
	}
	if existing.Status == "materialized" {
		return replayMaterialized(ctx, q, state, existing)
	}
	if existing.SourceSequence != state.NextSequence {
		return resultForReceipt(state, existing), commitEventError(ErrEventGap)
	}
	return materializeContiguousCollect(ctx, q, state, existing, collector)
}

func quarantineExistingReceipt(ctx context.Context, q *storedb.Queries, state SourceState, receipt IngressReceipt) (AppendResult, error) {
	const reason = "same source sequence has a different envelope hash"
	if err := updateReceiptStatus(ctx, q, receipt, "quarantined", reason); err != nil {
		return AppendResult{}, err
	}
	nextState, err := updateSourceState(ctx, q, state, state.NextSequence, state.MaterializedSequence, state.DedupFloor, "quarantined", reason)
	if err != nil {
		return AppendResult{}, err
	}
	receipt.Status, receipt.ErrorRef = "quarantined", reason
	return resultForReceipt(nextState, receipt), commitEventError(ErrEventConflict)
}

func replayMaterialized(ctx context.Context, q *storedb.Queries, state SourceState, receipt IngressReceipt) (AppendResult, error) {
	row, err := q.GetOrchestrationAgentEvent(ctx, storedb.GetOrchestrationAgentEventParams{EventID: receipt.Envelope.EventID})
	if err != nil {
		return AppendResult{}, classifyEventDBError("read materialized event", err)
	}
	event, err := agentEventFromRow(row)
	if err != nil {
		return AppendResult{}, err
	}
	if !eventMatchesReceipt(event, receipt) {
		// A receipt and canonical row are immutable facts.  If their complete
		// tuple diverges, returning either one as a replay would manufacture a
		// false event; quarantine/reconciliation must handle the corruption.
		return AppendResult{}, ErrEventConflict
	}
	return AppendResult{Receipt: receipt, Event: &event, State: state}, nil
}

// eventMatchesReceipt 比较 canonical event 与 receipt 的完整来源元组，确保重放引用同一不可变事实。
func eventMatchesReceipt(event AgentEvent, receipt IngressReceipt) bool {
	return event.Status == "materialized" && event.SourceID == receipt.SourceID &&
		event.SourceEpoch == receipt.SourceEpoch && event.Lane == receipt.Lane &&
		event.SourceSequence == receipt.SourceSequence && event.EnvelopeHash == receipt.EnvelopeHash &&
		event.CanonicalCursor == receipt.CanonicalCursor && reflect.DeepEqual(event.Envelope, receipt.Envelope)
}

func replayBelowFloor(ctx context.Context, q *storedb.Queries, frame IngressFrame) (AppendResult, error) {
	row, err := q.GetOrchestrationEventTombstone(ctx, tombstoneKey(frame))
	if errors.Is(err, sql.ErrNoRows) {
		return AppendResult{}, ErrEventFence
	}
	if err != nil {
		return AppendResult{}, classifyEventDBError("read event tombstone", err)
	}
	tombstone, err := tombstoneFromRow(row)
	if err != nil {
		return AppendResult{}, err
	}
	if tombstone.EnvelopeHash != frame.EnvelopeHash {
		return AppendResult{}, ErrEventConflict
	}
	return AppendResult{}, ErrEventReplayExpired
}

func insertStagedReceipt(ctx context.Context, q *storedb.Queries, frame IngressFrame) (IngressReceipt, error) {
	row, err := q.InsertOrchestrationEventIngressReceipt(ctx, ingressReceiptParams(frame, "staged", ""))
	if err != nil {
		return IngressReceipt{}, classifyEventDBError("insert event ingress receipt", err)
	}
	return ingressReceiptFromRow(row)
}

func markGap(ctx context.Context, q *storedb.Queries, state SourceState, receipt IngressReceipt, received int64) (AppendResult, error) {
	reason := fmt.Sprintf("missing source sequence before %d", received)
	nextState, err := updateSourceState(ctx, q, state, state.NextSequence, state.MaterializedSequence, state.DedupFloor, "gap", reason)
	if err != nil {
		return AppendResult{}, err
	}
	return resultForReceipt(nextState, receipt), ErrEventGap
}

// markControlGap records a control-lane hole as a durable quarantine. The
// staged receipt and source status are committed together by the transaction
// owner; the returned business error is deliberately post-commit.
func markControlGap(ctx context.Context, q *storedb.Queries, state SourceState, receipt IngressReceipt, received int64) (AppendResult, error) {
	reason := fmt.Sprintf("missing control source sequence before %d", received)
	if err := updateReceiptStatus(ctx, q, receipt, "quarantined", reason); err != nil {
		return AppendResult{}, err
	}
	nextState, err := updateSourceState(ctx, q, state, state.NextSequence, state.MaterializedSequence, state.DedupFloor, "quarantined", reason)
	if err != nil {
		return AppendResult{}, err
	}
	receipt.Status, receipt.ErrorRef = "quarantined", reason
	return resultForReceipt(nextState, receipt), ErrEventQuarantined
}

// materializeContiguous 将当前序列及已暂存的连续后继一次性 materialize。
func materializeContiguous(ctx context.Context, q *storedb.Queries, state SourceState, initial IngressReceipt) (AppendResult, error) {
	return materializeContiguousCollect(ctx, q, state, initial, nil)
}

// materializeContiguousCollect 排空 dense 前缀，并记录事务触及的每个事件，供
// 提交后的进程内提示通知使用。
func materializeContiguousCollect(ctx context.Context, q *storedb.Queries, state SourceState, initial IngressReceipt, collector *[]AgentEvent) (AppendResult, error) {
	next, materialized := state.NextSequence, state.MaterializedSequence
	current := initial
	var inputEvent *AgentEvent
	for range maxEventDrain {
		updatedNext, updatedMaterialized, following, event, hasFollowing, err := drainOneEvent(ctx, q, state, current, next, materialized)
		if err != nil {
			return AppendResult{}, err
		}
		next, materialized, current = updatedNext, updatedMaterialized, following
		collectMaterializedEvent(collector, event, initial.SourceSequence, &inputEvent)
		if !hasFollowing {
			break
		}
	}
	status, reason, err := stagedTailStatus(ctx, q, state, next)
	if err != nil {
		return AppendResult{}, err
	}
	nextState, err := updateSourceState(ctx, q, state, next, materialized, state.DedupFloor, status, reason)
	if err != nil {
		return AppendResult{}, err
	}
	row, err := q.GetOrchestrationEventIngressReceipt(ctx, receiptKey(initial.IngressFrame))
	if err != nil {
		return AppendResult{}, classifyEventDBError("read materialized receipt", err)
	}
	receipt, err := ingressReceiptFromRow(row)
	if err != nil {
		return AppendResult{}, err
	}
	return AppendResult{Receipt: receipt, Event: inputEvent, State: nextState}, nil
}

// collectMaterializedEvent 把事务内 materialize 结果交给 post-commit collector。
func collectMaterializedEvent(collector *[]AgentEvent, event *AgentEvent, initialSequence int64, input **AgentEvent) {
	if event == nil {
		return
	}
	if collector != nil {
		*collector = append(*collector, *event)
	}
	if event.SourceSequence == initialSequence {
		*input = event
	}
}

// drainOneEvent 处理一个连续序列并预取下一个 staged receipt。
func drainOneEvent(ctx context.Context, q *storedb.Queries, state SourceState, current IngressReceipt, next, materialized int64) (int64, int64, IngressReceipt, *AgentEvent, bool, error) {
	if current.SourceSequence != next {
		var err error
		current, err = loadStagedReceipt(ctx, q, state, next)
		if errors.Is(err, sql.ErrNoRows) {
			return next, materialized, IngressReceipt{}, nil, false, nil
		}
		if err != nil {
			return 0, 0, IngressReceipt{}, nil, false, err
		}
	}
	event, err := materializeReceipt(ctx, q, current)
	if err != nil {
		return 0, 0, IngressReceipt{}, nil, false, err
	}
	next++
	materialized++
	following, err := loadStagedReceipt(ctx, q, state, next)
	if errors.Is(err, sql.ErrNoRows) {
		return next, materialized, IngressReceipt{}, &event, false, nil
	}
	if err != nil {
		return 0, 0, IngressReceipt{}, nil, false, classifyEventDBError("peek staged event receipt", err)
	}
	return next, materialized, following, &event, true, nil
}

func loadStagedReceipt(ctx context.Context, q *storedb.Queries, state SourceState, sequence int64) (IngressReceipt, error) {
	row, err := q.GetOrchestrationEventIngressReceiptAtSequence(ctx, stagedReceiptKey(state, sequence))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IngressReceipt{}, err
		}
		return IngressReceipt{}, classifyEventDBError("read staged event receipt", err)
	}
	return ingressReceiptFromRow(row)
}

func materializeReceipt(ctx context.Context, q *storedb.Queries, receipt IngressReceipt) (AgentEvent, error) {
	event, err := insertOrReplayEvent(ctx, q, receipt)
	if err != nil {
		return AgentEvent{}, err
	}
	if err := updateReceiptStatus(ctx, q, receipt, "materialized", ""); err != nil {
		return AgentEvent{}, err
	}
	return event, nil
}

// insertOrReplayEvent 写入 canonical event，唯一冲突时只接受同哈希原事实。
func insertOrReplayEvent(ctx context.Context, q *storedb.Queries, receipt IngressReceipt) (AgentEvent, error) {
	row, err := q.InsertOrchestrationAgentEvent(ctx, agentEventParams(receipt.IngressFrame))
	if err == nil {
		return agentEventFromRow(row)
	}
	if !isEventUniqueError(err) {
		return AgentEvent{}, classifyEventDBError("insert canonical event", err)
	}
	existing, lookupErr := q.GetOrchestrationAgentEventBySourceSequence(ctx, eventSourceKey(receipt.IngressFrame))
	if lookupErr != nil {
		return AgentEvent{}, classifyEventDBError("replay canonical event", lookupErr)
	}
	event, lookupErr := agentEventFromRow(existing)
	if lookupErr != nil {
		return AgentEvent{}, lookupErr
	}
	if !eventMatchesReceipt(event, receipt) {
		return AgentEvent{}, ErrEventConflict
	}
	return event, nil
}

func updateReceiptStatus(ctx context.Context, q *storedb.Queries, receipt IngressReceipt, status, errorRef string) error {
	affected, err := q.UpdateOrchestrationEventIngressReceiptStatus(ctx, storedb.UpdateOrchestrationEventIngressReceiptStatusParams{
		NextStatus: status, ErrorRef: errorRef, SourceID: receipt.SourceID, SourceEpoch: receipt.SourceEpoch,
		Lane: string(receipt.Lane), SourceSequence: receipt.SourceSequence, ExpectedStatus: receipt.Status,
	})
	if err != nil {
		return classifyEventDBError("update event receipt status", err)
	}
	return requireEventRows("update event receipt status", affected)
}

func updateSourceState(ctx context.Context, q *storedb.Queries, state SourceState, next, materialized, floor int64, status, reason string) (SourceState, error) {
	affected, err := q.UpdateOrchestrationEventSourceState(ctx, storedb.UpdateOrchestrationEventSourceStateParams{
		NextSequence: next, MaterializedSequence: materialized, DedupFloor: floor, NextRevision: state.Revision + 1,
		Status: status, GapReason: reason, WriterEpoch: state.WriterEpoch, SourceID: state.SourceID,
		SourceEpoch: state.SourceEpoch, Lane: string(state.Lane), ExpectedRevision: state.Revision,
	})
	if err != nil {
		return SourceState{}, classifyEventDBError("update event source state", err)
	}
	if err := requireEventRows("update event source state", affected); err != nil {
		return SourceState{}, err
	}
	row, err := q.GetOrchestrationEventSourceState(ctx, sourceStateKey(IngressFrame{SourceID: state.SourceID, SourceEpoch: state.SourceEpoch, Lane: state.Lane}))
	if err != nil {
		return SourceState{}, classifyEventDBError("read updated event source state", err)
	}
	return sourceStateFromRow(row)
}

func stagedTailStatus(ctx context.Context, q *storedb.Queries, state SourceState, next int64) (string, string, error) {
	// A bounded drain may stop with the immediate successor already staged.
	// That is a pending contiguous tail, not a gap; only a later staged row
	// with no receipt at `next` proves a missing source sequence.
	_, err := q.GetOrchestrationEventIngressReceiptAtSequence(ctx, stagedReceiptKey(state, next))
	if err == nil {
		return "active", "", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", "", classifyEventDBError("check next staged event receipt", err)
	}
	count, err := q.HasOrchestrationEventIngressStagedAfter(ctx, storedb.HasOrchestrationEventIngressStagedAfterParams{
		SourceID: state.SourceID, SourceEpoch: state.SourceEpoch, Lane: string(state.Lane), FromSequence: next,
	})
	if err != nil {
		return "", "", classifyEventDBError("check staged event tail", err)
	}
	if count == 1 {
		return "gap", fmt.Sprintf("missing source sequence at %d", next), nil
	}
	return "active", "", nil
}

func resultForReceipt(state SourceState, receipt IngressReceipt) AppendResult {
	return AppendResult{Receipt: receipt, State: state}
}

func sourceStateKey(frame IngressFrame) storedb.GetOrchestrationEventSourceStateParams {
	return storedb.GetOrchestrationEventSourceStateParams{SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: string(frame.Lane)}
}

func receiptKey(frame IngressFrame) storedb.GetOrchestrationEventIngressReceiptParams {
	return storedb.GetOrchestrationEventIngressReceiptParams{SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: string(frame.Lane), SourceSequence: frame.SourceSequence}
}

func stagedReceiptKey(state SourceState, sequence int64) storedb.GetOrchestrationEventIngressReceiptAtSequenceParams {
	return storedb.GetOrchestrationEventIngressReceiptAtSequenceParams{SourceID: state.SourceID, SourceEpoch: state.SourceEpoch, Lane: string(state.Lane), SourceSequence: sequence}
}

func eventSourceKey(frame IngressFrame) storedb.GetOrchestrationAgentEventBySourceSequenceParams {
	return storedb.GetOrchestrationAgentEventBySourceSequenceParams{SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: string(frame.Lane), SourceSequence: frame.SourceSequence}
}

func tombstoneKey(frame IngressFrame) storedb.GetOrchestrationEventTombstoneParams {
	return storedb.GetOrchestrationEventTombstoneParams{SourceID: frame.SourceID, SourceEpoch: frame.SourceEpoch, Lane: string(frame.Lane), SourceSequence: frame.SourceSequence}
}
