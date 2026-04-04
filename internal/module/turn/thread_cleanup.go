package turn

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func (s *service) InterruptActiveTurn(ctx context.Context, session contract.Session, source string) error {
	ctx, threadID, err := requireTurnContext(ctx, session)
	if err != nil {
		return err
	}
	active, tracked := s.tracker.ActiveByThread(threadID)
	if !tracked {
		return nil
	}
	_, err = interruptAndWait(ctx, session, s.tracker, active, threadID, source, func() error {
		return s.waitForTurnSettle(ctx, active.localID, active.handle)
	})
	return err
}

func (s *service) CleanupThread(_ context.Context, threadID, reason string) error {
	s.tracker.AbortThread(threadID, reason)
	return nil
}
