package thread

import (
	"context"
	"runtime"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	pkglogger "github.com/anthropic-ai/super-agent-v3/internal/platform/logging"
)

// Archive 归档线程。
func (s *service) Archive(ctx context.Context, threadID string) error {
	ctx = kernel.NonNilContext(ctx)
	caller := archiveCallerStack()
	pkglogger.Info("thread: Archive() ENTERED",
		"thread_id", threadID,
		"caller", caller,
	)
	if handled, err := s.archivePendingLaunchThread(ctx, threadID, caller); handled || err != nil {
		return err
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

// archivePendingLaunchThread 归档待处理启动线程。
func (s *service) archivePendingLaunchThread(ctx context.Context, threadID string, caller string) (bool, error) {
	if s == nil || s.threadStore == nil {
		return false, nil
	}
	id := strings.TrimSpace(threadID)
	if id == "" {
		return false, nil
	}
	mu := s.acquirePendingLaunchLock(id)
	mu.Lock()
	defer mu.Unlock()
	pendingLaunch, err := s.isThreadPendingLaunch(ctx, id)
	if err != nil {
		return false, err
	}
	if !pendingLaunch {
		return false, nil
	}
	if err := s.updateThreadStatus(ctx, id, statusArchived); err != nil {
		return true, err
	}
	s.CompleteLaunchIntent(ctx, id)
	s.publishThreadStopped(id, "", statusArchived, "archived_pending_launch")
	pkglogger.Info("thread: Archive() pending_launch fast-path",
		"thread_id", id,
		"caller", caller,
	)
	return true, nil
}

// Unarchive 处理unarchive。
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
	s.publishThreadStopped(threadID, "", statusCreated, "unarchived")
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
// archiveCallerStack 归档callerstack。
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
