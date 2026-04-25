package thread

import (
	"context"
	"runtime"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func (s *service) Archive(ctx context.Context, threadID string) error {
	ctx = shared.NonNilContext(ctx)
	caller := archiveCallerStack()
	pkglogger.Info("thread: Archive() ENTERED",
		"thread_id", threadID,
		"caller", caller,
	)
	// C1 fast-path: pending_launch threads have no binding and no running
	// runtime, so resolveThreadStopState (which calls bindingStore.GetByAgentID)
	// always fails with "no rows in result set". Just flip the row to
	// statusArchived so the card moves to the archived bucket.
	if s.isThreadPendingLaunch(ctx, threadID) {
		id := strings.TrimSpace(threadID)
		if err := s.updateThreadStatus(ctx, id, statusArchived); err != nil {
			return err
		}
		s.pendingLaunchMu.Delete(id)
		s.publishThreadStopped(id, "", statusArchived, "archived_pending_launch")
		pkglogger.Info("thread: Archive() pending_launch fast-path",
			"thread_id", id,
			"caller", caller,
		)
		return nil
	}
	stopState, err := s.resolveThreadStopState(ctx, threadID)
	if err != nil {
		pkglogger.Warn("thread: Archive() resolveThreadStopState FAILED",
			"thread_id", threadID,
			"error", err,
			"caller", caller,
		)
		return err
	}
	pkglogger.Info("thread: Archive() resolved stopState",
		"thread_id", threadID,
		"stopped_id", stopState.stoppedID,
		"agent_id", stopState.agentID,
		"targets", strings.Join(stopState.targets, ","),
		"caller", caller,
	)
	if err := s.stopThreadRuntime(ctx, stopState, "thread_archived", true); err != nil {
		return err
	}
	if err := s.updateThreadStatus(ctx, stopState.stoppedID, statusArchived); err != nil {
		return err
	}
	if err := s.setBindingArchived(ctx, stopState.stoppedID, true); err != nil {
		return err
	}
	s.cleanupThreadScratchpad(ctx, stopState.stoppedID, stopState.binding)
	s.cleanupThreadTurns(ctx, "thread_archived", stopState.targets...)
	s.publishThreadStopped(stopState.stoppedID, stopState.agentID, statusArchived, "archived")
	pkglogger.Info("thread: Archive() COMPLETED",
		"thread_id", threadID,
		"stopped_id", stopState.stoppedID,
		"caller", caller,
	)
	return nil
}

func (s *service) Unarchive(ctx context.Context, threadID string) error {
	caller := archiveCallerStack()
	pkglogger.Info("thread: Unarchive() ENTERED",
		"thread_id", threadID,
		"caller", caller,
	)
	if err := s.updateThreadStatus(ctx, threadID, statusCreated); err != nil {
		return err
	}
	if err := s.setBindingArchived(ctx, threadID, false); err != nil {
		return err
	}
	// Evict the zombie session left by Archive (transport closed, context
	// canceled) so that the next resolve path creates a fresh session via
	// autoResumeSession, reconnecting to the same provider thread UUID
	// and preserving conversation history.
	s.unblockResumeForThread(ctx, threadID)
	s.resetSessionRecoveryForThread(ctx, threadID)
	s.evictZombieSession(ctx, threadID)
	// Pre-warm: kick off a background resume so the session is ready by the
	// time the user sends the first message.
	s.backgroundResumeIfNeeded(ctx, threadID)
	pkglogger.Info("thread: Unarchive() COMPLETED",
		"thread_id", threadID,
		"caller", caller,
	)
	return nil
}

// archiveCallerStack returns a compact caller stack for debugging
// which code path triggered Archive/Unarchive.
func archiveCallerStack() string {
	var pcs [8]uintptr
	n := runtime.Callers(3, pcs[:])
	if n == 0 {
		return "<unknown>"
	}
	frames := runtime.CallersFrames(pcs[:n])
	var parts []string
	for {
		frame, more := frames.Next()
		short := frame.Function
		if idx := strings.LastIndex(short, "/"); idx >= 0 {
			short = short[idx+1:]
		}
		parts = append(parts, short)
		if !more || len(parts) >= 6 {
			break
		}
	}
	return strings.Join(parts, " <- ")
}
