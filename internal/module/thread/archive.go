package thread

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func (s *service) Archive(ctx context.Context, threadID string) error {
	ctx = shared.NonNilContext(ctx)
	stopState, err := s.resolveThreadStopState(ctx, threadID)
	if err != nil {
		return err
	}
	if err := s.stopThreadRuntime(ctx, stopState, "thread_archived", true); err != nil {
		return err
	}
	if err := s.updateThreadStatus(ctx, stopState.stoppedID, statusArchived); err != nil {
		return err
	}
	if err := s.setBindingArchived(ctx, stopState.stoppedID, true); err != nil {
		return err
	}
	s.cleanupThreadTurns(ctx, "thread_archived", stopState.targets...)
	s.publishThreadStopped(stopState.stoppedID, stopState.agentID, statusArchived, "archived")
	return nil
}

func (s *service) Unarchive(ctx context.Context, threadID string) error {
	if err := s.updateThreadStatus(ctx, threadID, statusCreated); err != nil {
		return err
	}
	return s.setBindingArchived(ctx, threadID, false)
}
