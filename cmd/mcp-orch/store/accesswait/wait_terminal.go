package accesswait

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// updateSatisfiedWaitTx 在同一事务重验 grant、source fence 和 canonical event 后 CAS 满足 wait。
func (s *Store) updateSatisfiedWaitTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, handle WaitHandle, event eventstore.AgentEvent) (WaitHandle, error) {
	grant, err := s.validateSatisfiedEventTx(ctx, q, txdb, handle, event)
	if err != nil {
		return WaitHandle{}, err
	}
	affected, err := q.SatisfyOrchestrationEventWaitHandle(ctx, storedb.SatisfyOrchestrationEventWaitHandleParams{
		MatchedEventID: event.Envelope.EventID, MatchedSourceSequence: event.SourceSequence, MatchedCanonicalCursor: event.CanonicalCursor,
		ResolvedAt: utcText(s.now().UTC()), WaitHandleID: handle.WaitHandleID, GrantID: grant.GrantID, ExpectedRevision: handle.Revision,
	})
	if err != nil {
		return WaitHandle{}, classifyDBError("satisfy event wait handle", err)
	}
	if affected != 1 {
		return WaitHandle{}, ErrWaitTerminal
	}
	row, err := q.GetOrchestrationEventWaitHandle(ctx, storedb.GetOrchestrationEventWaitHandleParams{WaitHandleID: handle.WaitHandleID})
	if err != nil {
		return WaitHandle{}, classifyDBError("read satisfied event wait handle", err)
	}
	return waitFromRow(row)
}

// validateSatisfiedEventTx 证明事件属于授权 source 且仍是 canonical tuple。
func (s *Store) validateSatisfiedEventTx(ctx context.Context, q *storedb.Queries, txdb storedb.DBTX, handle WaitHandle, event eventstore.AgentEvent) (AccessGrant, error) {
	grant, err := s.loadActiveGrantForWait(ctx, q, handle)
	if err != nil {
		return AccessGrant{}, err
	}
	if err := s.verifyRequestedFencesTx(ctx, txdb, grant.Source, grant.Target); err != nil {
		return AccessGrant{}, err
	}
	if err := s.ensureSourceReadableTx(ctx, txdb, handle.Selector, handle.AfterSequence); err != nil {
		return AccessGrant{}, err
	}
	canonical, err := s.eventsTx.ListEventsTx(ctx, txdb, event.SourceID, event.SourceEpoch, event.Lane, event.SourceSequence-1, 1)
	if err != nil {
		return AccessGrant{}, err
	}
	if len(canonical) != 1 || !canonicalEventTupleMatches(canonical[0], event) {
		return AccessGrant{}, ErrAccessDenied
	}
	if err := validateAuthorizedEvent(canonical[0], grant.Target, handle.Selector); err != nil {
		return AccessGrant{}, err
	}
	return grant, nil
}

// loadActiveGrantForWait 在满足 wait 前读取完整 grant，供同事务 fence 重验。
func (s *Store) loadActiveGrantForWait(ctx context.Context, q *storedb.Queries, handle WaitHandle) (AccessGrant, error) {
	row, err := q.GetOrchestrationAccessGrant(ctx, storedb.GetOrchestrationAccessGrantParams{GrantID: handle.GrantID})
	if err != nil {
		return AccessGrant{}, classifyDBError("load grant before satisfying wait", err)
	}
	grant, err := grantFromRow(row)
	if err != nil {
		return AccessGrant{}, err
	}
	if grant.Status != "active" || grant.Revision != handle.GrantRevision {
		return AccessGrant{}, ErrAccessDenied
	}
	if handle.SourceAgentID != grant.Source.AgentID || !equalFence(handle.Target, grant.Target) || handle.Selector.TargetAgentID != grant.Target.AgentID || !selectorSourceMatchesGrant(handle.Selector, grant) {
		return AccessGrant{}, ErrAccessDenied
	}
	return grant, nil
}

func (s *Store) resolveSatisfiedRace(ctx context.Context, handle WaitHandle) (WaitResult, error) {
	latest, err := s.getWaitByID(ctx, handle.WaitHandleID)
	if err != nil {
		return WaitResult{}, err
	}
	if !isTerminalWaitStatus(latest.Status) {
		return WaitResult{}, ErrWaitTerminal
	}
	// Any terminal winner (satisfied, timeout, cancellation, grant revoke or
	// grant expiry) must be replayed after a competing CAS loses. Re-authorize
	// the current handle before returning even metadata-only terminal states;
	// terminal handles are not bearer capabilities and a grant may have been
	// revoked between the losing CAS and this read-back.
	if err := s.authorizeTerminalHandle(ctx, latest); err != nil {
		return WaitResult{}, err
	}
	return s.resultForTerminal(ctx, latest)
}

func isTerminalWaitStatus(status string) bool {
	switch status {
	case "satisfied", "timed_out", "cancelled", "revoked", "expired":
		return true
	default:
		return false
	}
}

// expireWait 以 deadline 和 wait revision CAS 固化 timeout 终态。
func (s *Store) expireWait(ctx context.Context, handle WaitHandle) (WaitResult, error) {
	updated, err := s.updateExpiredWait(ctx, handle)
	if err != nil {
		if errors.Is(err, ErrWaitTerminal) {
			latest, readErr := s.getWaitByID(ctx, handle.WaitHandleID)
			if readErr != nil {
				return WaitResult{}, readErr
			}
			if authErr := s.authorizeTerminalHandle(ctx, latest); authErr != nil {
				return WaitResult{}, authErr
			}
			return s.resultForTerminal(ctx, latest)
		}
		return WaitResult{}, err
	}
	return waitResult(updated, nil, updated.Status, false), nil
}

func (s *Store) updateExpiredWait(ctx context.Context, handle WaitHandle) (WaitHandle, error) {
	var updated WaitHandle
	err := s.withWrite(ctx, func(q *storedb.Queries) error {
		now := s.now().UTC()
		affected, err := q.TimeoutOrchestrationEventWaitHandle(ctx, storedb.TimeoutOrchestrationEventWaitHandleParams{
			ResolvedAt: utcText(now), WaitHandleID: handle.WaitHandleID, ExpectedRevision: handle.Revision, NowAt: utcText(now),
		})
		if err != nil {
			return classifyDBError("expire event wait handle", err)
		}
		if affected != 1 {
			return ErrWaitTerminal
		}
		row, err := q.GetOrchestrationEventWaitHandle(ctx, storedb.GetOrchestrationEventWaitHandleParams{WaitHandleID: handle.WaitHandleID})
		if err != nil {
			return classifyDBError("read expired event wait handle", err)
		}
		updated, err = waitFromRow(row)
		return err
	})
	return updated, err
}

func (s *Store) resultForTerminal(ctx context.Context, handle WaitHandle) (WaitResult, error) {
	if handle.Status != "satisfied" {
		return waitResult(handle, nil, handle.Status, false), nil
	}
	var result WaitResult
	err := s.withWriteTx(ctx, func(_ *storedb.Queries, txdb storedb.DBTX) error {
		var err error
		result, err = s.resultForTerminalTx(ctx, txdb, handle)
		return err
	})
	if err != nil {
		return mapWaitEventError(err)
	}
	return result, nil
}

// resultForTerminalTx 在事务中重验 satisfied 事件；其他终态只返回不可变元数据。
func (s *Store) resultForTerminalTx(ctx context.Context, dbtx storedb.DBTX, handle WaitHandle) (WaitResult, error) {
	if err := sqlctx.RequireTransactionDBTX(dbtx); err != nil {
		return WaitResult{}, err
	}
	if handle.Status != "satisfied" {
		return waitResult(handle, nil, handle.Status, false), nil
	}
	event, err := s.readSatisfiedEventTx(ctx, dbtx, handle)
	if err != nil {
		return WaitResult{}, err
	}
	return findMatchedTerminalEvent([]eventstore.AgentEvent{event}, handle)
}

// readSatisfiedEventTx 读取并校验 satisfied wait 的 source state、事件 tuple 与 caller fence。
func (s *Store) readSatisfiedEventTx(ctx context.Context, dbtx storedb.DBTX, handle WaitHandle) (eventstore.AgentEvent, error) {
	if _, err := s.authorizeWaitTx(ctx, dbtx, PollRequest{WaitHandleID: handle.WaitHandleID, CallerPrincipalRef: handle.CallerPrincipalRef, ScopeRef: handle.ScopeRef, GrantRevision: handle.GrantRevision}, handle); err != nil {
		return eventstore.AgentEvent{}, err
	}
	state, err := s.eventsTx.GetSourceStateTx(ctx, dbtx, handle.Selector.SourceID, handle.Selector.SourceEpoch, handle.Selector.Lane)
	if err != nil {
		if errors.Is(err, eventstore.ErrEventNotFound) {
			return eventstore.AgentEvent{}, ErrWaitNotFound
		}
		return eventstore.AgentEvent{}, err
	}
	if err := ensureSourceStateStatus(state); err != nil {
		return eventstore.AgentEvent{}, err
	}
	event, err := s.eventsTx.GetEventTx(ctx, dbtx, handle.MatchedEventID)
	if err != nil {
		return eventstore.AgentEvent{}, err
	}
	if !matchedEventTuple(event, handle) {
		return eventstore.AgentEvent{}, ErrWaitNotFound
	}
	return event, nil
}

// matchedEventTuple 确保终态事件仍绑定原 selector 的完整 source tuple。
func matchedEventTuple(event eventstore.AgentEvent, handle WaitHandle) bool {
	return event.SourceSequence == handle.MatchedSourceSequence && event.CanonicalCursor == handle.MatchedCanonicalCursor && event.SourceID == handle.Selector.SourceID && event.SourceEpoch == handle.Selector.SourceEpoch && event.Lane == handle.Selector.Lane
}

// canonicalEventTupleMatches 比较事件的 canonical source、序号、hash、event ID 和 cursor。
func canonicalEventTupleMatches(left, right eventstore.AgentEvent) bool {
	return left.SourceID == right.SourceID && left.SourceEpoch == right.SourceEpoch && left.Lane == right.Lane && left.SourceSequence == right.SourceSequence && left.EnvelopeHash == right.EnvelopeHash && left.Envelope.EventID == right.Envelope.EventID && left.CanonicalCursor == right.CanonicalCursor
}

func findMatchedTerminalEvent(events []eventstore.AgentEvent, handle WaitHandle) (WaitResult, error) {
	for index := range events {
		item := events[index]
		if item.Envelope.EventID != handle.MatchedEventID || item.CanonicalCursor != handle.MatchedCanonicalCursor {
			continue
		}
		if err := validateAuthorizedEvent(item, handle.Target, handle.Selector); err != nil {
			return WaitResult{}, err
		}
		return waitResult(handle, &item, "satisfied", false), nil
	}
	return WaitResult{}, ErrWaitNotFound
}
