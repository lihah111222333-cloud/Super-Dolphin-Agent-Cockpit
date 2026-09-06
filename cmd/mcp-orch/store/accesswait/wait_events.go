package accesswait

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/eventstore"
	storedb "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

// ReadAuthorizedEvents 读取授权范围内的 canonical event metadata；正文仍由 CONTENT owner 负责。
func (s *Store) ReadAuthorizedEvents(ctx context.Context, request AccessRequest, selector EventSelector, afterSequence, limit int64) ([]eventstore.AgentEvent, error) {
	if err := validateEventReadInput(request, selector, afterSequence, limit); err != nil {
		return nil, err
	}
	var result []eventstore.AgentEvent
	err := s.withWriteTx(ctx, func(_ *storedb.Queries, txdb storedb.DBTX) error {
		authorization, err := s.validateEventReadTx(ctx, txdb, request, selector, afterSequence, limit)
		if err != nil {
			return err
		}
		if err := s.ensureSourceReadableTx(ctx, txdb, selector, afterSequence); err != nil {
			return err
		}
		items, err := s.eventsTx.ListEventsTx(ctx, txdb, selector.SourceID, selector.SourceEpoch, selector.Lane, afterSequence, limit)
		if err != nil {
			return err
		}
		if err := s.ensureSourceReadableTx(ctx, txdb, selector, afterSequence); err != nil {
			return err
		}
		// Re-authorize after the canonical read on the same transaction-bound
		// connection; an identity/grant rollover must not leak the read result.
		postAuthorization, err := s.validateEventReadTx(ctx, txdb, request, selector, afterSequence, limit)
		if err != nil {
			return err
		}
		if postAuthorization.GrantRevision != authorization.GrantRevision || !equalFence(postAuthorization.Grant.Target, authorization.Grant.Target) {
			return ErrAccessDenied
		}
		result, err = selectedAuthorizedEvents(items, postAuthorization.Grant.Target, selector)
		return err
	})
	if err != nil {
		return nil, mapWaitEventErrorList(err)
	}
	return result, nil
}

func (s *Store) validateEventReadTx(ctx context.Context, dbtx storedb.DBTX, request AccessRequest, selector EventSelector, afterSequence, limit int64) (Authorization, error) {
	if err := validateEventReadInput(request, selector, afterSequence, limit); err != nil {
		return Authorization{}, err
	}
	authorization, err := s.AuthorizeTx(ctx, dbtx, request)
	if err != nil {
		return Authorization{}, err
	}
	if !eventReadTargetMatches(selector, authorization) || !selectorSourceMatchesGrant(selector, authorization.Grant) {
		return Authorization{}, ErrAccessDenied
	}
	return authorization, nil
}

// validateEventReadInput 在分配事件读取前校验 capability、selector 和分页边界。
func validateEventReadInput(request AccessRequest, selector EventSelector, afterSequence, limit int64) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if request.Capability != "read" {
		return ErrAccessDenied
	}
	if err := selector.Validate(); err != nil {
		return err
	}
	if afterSequence < 0 || limit < 1 || limit > maxWaitPollEvents {
		return invalidWait("authorized event read limit is 1..200")
	}
	return nil
}

func eventReadTargetMatches(selector EventSelector, authorization Authorization) bool {
	return selector.TargetAgentID == authorization.Grant.Target.AgentID && selector.SourceID != "" && selector.SourceEpoch != "" && validSelectorLane(selector.Lane)
}

func selectedAuthorizedEvents(items []eventstore.AgentEvent, target FenceSnapshot, selector EventSelector) ([]eventstore.AgentEvent, error) {
	selected := make([]eventstore.AgentEvent, 0, len(items))
	for _, item := range items {
		if !eventTypeSelected(item.Envelope.EventType, selector.EventTypes) {
			continue
		}
		if err := validateAuthorizedEvent(item, target, selector); err != nil {
			return nil, err
		}
		selected = append(selected, item)
	}
	return selected, nil
}

func eventTypeSelected(eventType string, allowed []string) bool {
	for _, candidate := range allowed {
		if candidate == eventType {
			return true
		}
	}
	return false
}

func selectWaitEvent(items []eventstore.AgentEvent, handle WaitHandle) (*eventstore.AgentEvent, error) {
	allowed := make(map[string]struct{}, len(handle.Selector.EventTypes))
	for _, eventType := range handle.Selector.EventTypes {
		allowed[eventType] = struct{}{}
	}
	for index := range items {
		item := items[index]
		if !waitEventCandidate(item, handle, allowed) {
			continue
		}
		if err := validateAuthorizedEvent(item, handle.Target, handle.Selector); err != nil {
			return nil, err
		}
		return &item, nil
	}
	return nil, nil
}

func waitEventCandidate(item eventstore.AgentEvent, handle WaitHandle, allowed map[string]struct{}) bool {
	if item.SourceSequence <= handle.AfterSequence {
		return false
	}
	if item.Envelope.SubjectAgentID != handle.Selector.TargetAgentID {
		return false
	}
	_, ok := allowed[item.Envelope.EventType]
	return ok
}

func validateAuthorizedEvent(item eventstore.AgentEvent, target FenceSnapshot, selector EventSelector) error {
	if !eventSelectorMatches(item, selector) {
		return ErrAccessDenied
	}
	if !eventFenceMatches(item, target) {
		return ErrAccessDenied
	}
	return nil
}

func eventSelectorMatches(item eventstore.AgentEvent, selector EventSelector) bool {
	return item.SourceID == selector.SourceID && item.SourceEpoch == selector.SourceEpoch && item.Lane == selector.Lane && item.Envelope.SubjectAgentID == selector.TargetAgentID && item.Envelope.EventType != ""
}

// eventFenceMatches 比较事件携带的 incarnation/binding fence，拒绝旧代际回放。
func eventFenceMatches(item eventstore.AgentEvent, target FenceSnapshot) bool {
	if item.Envelope.IncarnationID == nil {
		return false
	}
	return item.Envelope.ReservationID == target.ReservationID &&
		*item.Envelope.IncarnationID == target.IncarnationID &&
		item.Envelope.IncarnationGeneration == target.IncarnationGeneration &&
		item.Envelope.BindingVersion == target.BindingVersion &&
		item.Envelope.BindingDigest == target.BindingDigest &&
		item.Envelope.SourceFenceRef == target.SourceFenceRef
}

func waitRequestMatchesHandle(request WaitRequest, handle WaitHandle, selectorDigest string) bool {
	handleDigest, err := handle.Selector.Digest()
	if err != nil || handleDigest != selectorDigest {
		return false
	}
	return waitIdentityMatches(request, handle) && waitCursorMatches(request, handle) && waitRequestProofMatches(request, handle)
}

// waitIdentityMatches 比较 wait 重放的句柄、grant、caller 和 scope 身份。
func waitIdentityMatches(request WaitRequest, handle WaitHandle) bool {
	idMatches := handle.WaitHandleID == request.WaitHandleID || request.waitHandleIDGenerated
	return idMatches && handle.GrantID == request.GrantID && handle.GrantRevision == request.GrantRevision && handle.CallerPrincipalRef == request.CallerPrincipalRef && handle.ScopeRef == request.ScopeRef
}

func waitCursorMatches(request WaitRequest, handle WaitHandle) bool {
	return handle.AfterSequence == request.AfterSequence && handle.AfterCursor == request.AfterCursor
}

func waitRequestProofMatches(request WaitRequest, handle WaitHandle) bool {
	return handle.DeadlineAt.Equal(request.DeadlineAt) && handle.IdempotencyNamespace == request.IdempotencyNamespace && handle.IdempotencyKeyDigest == request.IdempotencyKeyDigest && handle.RequestDigest == request.RequestDigest
}

func waitResult(handle WaitHandle, event *eventstore.AgentEvent, status string, isError bool) WaitResult {
	result := WaitResult{Handle: handle, Event: event, Status: status, IsError: isError}
	if event != nil {
		result.NextCursor = event.CanonicalCursor
	}
	return result
}

// mapWaitEventError 将 canonical reader 错误映射为 wait 稳定错误分类。
func mapWaitEventError(err error) (WaitResult, error) {
	if mapped := stableWaitError(err); mapped != nil {
		return WaitResult{}, mapped
	}
	return WaitResult{}, err
}

// mapWaitEventErrorList 为授权事件读取提供与 PollWait 一致的错误映射。
func mapWaitEventErrorList(err error) error {
	if mapped := stableWaitError(err); mapped != nil {
		return mapped
	}
	return err
}

// stableWaitError 合并本地与 canonical event 的稳定错误分类。
func stableWaitError(err error) error {
	if mapped := localWaitError(err); mapped != nil {
		return mapped
	}
	return canonicalWaitError(err)
}

// localWaitError 映射 wait owner 自身已经判定的错误。
func localWaitError(err error) error {
	switch {
	case errors.Is(err, ErrWaitNotFound):
		return ErrWaitNotFound
	case errors.Is(err, ErrWaitCursorAhead):
		return ErrWaitCursorAhead
	case errors.Is(err, ErrWaitReplayExpired):
		return ErrWaitReplayExpired
	case errors.Is(err, ErrWaitScanLimit):
		return ErrWaitScanLimit
	default:
		return nil
	}
}

// canonicalWaitError 映射 EventStore 的 gap、quarantine 与 fence 错误。
func canonicalWaitError(err error) error {
	switch {
	case errors.Is(err, eventstore.ErrEventGap):
		return ErrWaitGap
	case errors.Is(err, eventstore.ErrEventQuarantined):
		return ErrWaitQuarantined
	case errors.Is(err, eventstore.ErrEventNotFound):
		return ErrWaitNotFound
	case errors.Is(err, eventstore.ErrEventReplayExpired):
		return ErrWaitReplayExpired
	case errors.Is(err, eventstore.ErrEventFence), errors.Is(err, eventstore.ErrEventConflict):
		return ErrAccessDenied
	default:
		return nil
	}
}
