package turn

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *service) InterruptActiveTurn(ctx context.Context, session contract.Session, source string) error {
	ctx = normalizeContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := requireSession(session); err != nil {
		return err
	}
	threadID, err := resolveThreadID(session, "")
	if err != nil {
		return err
	}
	active, tracked := s.tracker.ActiveByThread(threadID)
	if !tracked {
		return nil
	}
	err = session.Interrupt(ctx, dto.InterruptRequest{
		ThreadID: threadID,
		Source:   strings.TrimSpace(source),
	})
	if err != nil || !s.tracker.MarkInterruptRequested(active.localID) {
		return err
	}
	return s.waitForTurnSettle(ctx, active.localID, active.handle)
}

func (s *service) CleanupThread(_ context.Context, threadID, reason string) error {
	s.tracker.AbortThread(threadID, reason)
	return nil
}
