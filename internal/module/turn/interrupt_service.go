package turn

import (
	"context"
	"errors"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

var interruptSettleTimeout = config.InterruptSettleTimeout

func (s *service) InterruptTurn(ctx context.Context, session contract.Session, source string) (TurnStatus, error) {
	ctx, threadID, err := requireTurnContext(ctx, session)
	if err != nil {
		return TurnStatus{}, err
	}
	active, tracked := s.tracker.ActiveByThread(threadID)
	before := s.interruptBaseStatus(active, tracked)
	start := time.Now()
	waited, err := interruptAndWait(ctx, session, s.tracker, active, threadID, source, nil)
	if err != nil {
		return TurnStatus{}, err
	}
	if !tracked {
		return attachInterruptEnvelope(before, buildTurnInterruptEnvelope(before.State, before.State, false, false, 0, false)), nil
	}
	return s.finishInterrupt(ctx, active, before, start, waited)
}

func (s *service) interruptBaseStatus(active activeTurn, tracked bool) TurnStatus {
	if !tracked {
		return TurnStatus{}
	}
	if status, ok := s.tracker.Get(active.localID); ok {
		return status
	}
	return TurnStatus{
		LocalID:    active.localID,
		ProviderID: interruptProviderID(TurnStatus{}, active.handle),
		State:      "running",
	}
}

func (s *service) finishInterrupt(
	ctx context.Context,
	active activeTurn,
	before TurnStatus,
	start time.Time,
	waited bool,
) (TurnStatus, error) {
	if waited {
		if err := s.waitForTurnSettle(ctx, active.localID, active.handle); err != nil {
			if status, ok := s.timeoutInterruptStatus(ctx, err, active, before, start); ok {
				return status, nil
			}
			return TurnStatus{}, err
		}
	}
	after := s.interruptStatus(active, before, "interrupted")
	envelope := buildTurnInterruptEnvelope(
		before.State,
		after.State,
		true,
		true,
		time.Since(start).Milliseconds(),
		interruptConfirmed(after.State),
	)
	return attachInterruptEnvelope(after, envelope), nil
}

func (s *service) interruptStatus(active activeTurn, fallback TurnStatus, defaultState string) TurnStatus {
	if after, ok := s.tracker.Get(active.localID); ok {
		return after
	}
	return TurnStatus{
		LocalID:    active.localID,
		ProviderID: interruptProviderID(fallback, active.handle),
		State:      defaultState,
	}
}

func (s *service) timeoutInterruptStatus(
	ctx context.Context,
	err error,
	active activeTurn,
	before TurnStatus,
	start time.Time,
) (TurnStatus, bool) {
	if !errors.Is(err, context.DeadlineExceeded) || (ctx != nil && ctx.Err() != nil) {
		return TurnStatus{}, false
	}
	after := s.interruptStatus(active, before, before.State)
	return attachInterruptEnvelope(after, buildTurnInterruptTimeoutEnvelope(
		before.State,
		after.State,
		time.Since(start).Milliseconds(),
	)), true
}
