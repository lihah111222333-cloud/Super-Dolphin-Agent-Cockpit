package turn

import (
	"context"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *service) InterruptTurn(ctx context.Context, session contract.Session, source string) (TurnStatus, error) {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return TurnStatus{}, err
	}
	if err := requireSession(session); err != nil {
		return TurnStatus{}, err
	}
	threadID, err := resolveThreadID(session, "")
	if err != nil {
		return TurnStatus{}, err
	}
	active, tracked := s.tracker.ActiveByThread(threadID)
	before := s.interruptBaseStatus(active, tracked)
	start := time.Now()
	err = session.Interrupt(ctx, dto.InterruptRequest{
		ThreadID: threadID,
		Source:   strings.TrimSpace(source),
	})
	if err != nil {
		return TurnStatus{}, err
	}
	if !tracked {
		return attachInterruptEnvelope(before, buildTurnInterruptEnvelope(before.State, before.State, false, false, 0, false)), nil
	}
	return s.finishInterrupt(ctx, active, before, start)
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

func (s *service) finishInterrupt(ctx context.Context, active activeTurn, before TurnStatus, start time.Time) (TurnStatus, error) {
	if s.tracker.MarkInterruptRequested(active.localID) {
		if err := s.waitForTurnSettle(ctx, active.localID, active.handle); err != nil {
			return TurnStatus{}, err
		}
	}
	after, ok := s.tracker.Get(active.localID)
	if !ok {
		after = TurnStatus{
			LocalID:    active.localID,
			ProviderID: interruptProviderID(before, active.handle),
			State:      "interrupted",
		}
	}
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
