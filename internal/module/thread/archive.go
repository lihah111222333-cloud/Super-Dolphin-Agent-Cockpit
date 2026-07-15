package thread

import (
	"context"
	"errors"
	"runtime"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// Archive 归档线程。
func (s *service) Archive(ctx context.Context, threadID string) error {
	ctx = util.NonNilContext(ctx)
	caller := archiveCallerStack()
	pkglogger.Info("thread: Archive() ENTERED", "thread_id", threadID, "caller", caller)
	if handled, err := s.archivePendingLaunchThread(ctx, threadID, caller); handled || err != nil {
		return err
	}
	stopState, err := s.resolveThreadStopState(ctx, threadID)
	if err != nil {
		pkglogger.Warn("thread: Archive() resolveThreadStopState FAILED", "thread_id", threadID, "error", err, "caller", caller)
		return err
	}
	pkglogger.Info("thread: Archive() resolved stopState",
		"thread_id", threadID,
		"stopped_id", stopState.stoppedID,
		"agent_id", stopState.agentID,
		"targets", strings.Join(stopState.targets, ","),
		"caller", caller,
	)
	releaseResume, err := s.stopThreadRuntime(ctx, stopState, "thread_archived", true)
	if err != nil {
		return err
	}
	defer releaseResume()
	if err := s.updateThreadStatus(ctx, stopState.stoppedID, statusArchived); err != nil {
		return err
	}
	if err := s.setBindingArchived(ctx, stopState.stoppedID, true); err != nil {
		return err
	}
	cleanupErr := s.cleanupThreadScratchpad(ctx, stopState.stoppedID, stopState.binding)
	turnCleanupErr := s.cleanupThreadTurns(ctx, "thread_archived", stopState.targets...)
	s.publishThreadStopped(stopState.stoppedID, stopState.agentID, statusArchived, "archived")
	pkglogger.Info("thread: Archive() COMPLETED", "thread_id", threadID, "stopped_id", stopState.stoppedID, "caller", caller)
	return newLifecyclePartialCleanupError("archive", errors.Join(cleanupErr, turnCleanupErr))
}

// archivePendingLaunchThread 归档待处理启动线程。
func (s *service) archivePendingLaunchThread(ctx context.Context, threadID string, caller string) (bool, error) {
	return s.transitionPendingLaunchThread(ctx, threadID, caller, statusArchived, "archived_pending_launch", true)
}

// unarchivePendingLaunchThread 恢复尚未绑定 provider 的 pending_launch 线程。
func (s *service) unarchivePendingLaunchThread(ctx context.Context, threadID string, caller string) (bool, error) {
	return s.transitionPendingLaunchThread(ctx, threadID, caller, statusCreated, "unarchived_pending_launch", false)
}

// transitionPendingLaunchThread 只更新 pending_launch 线程状态并发布投影事件；这类线程没有 binding/session。
func (s *service) transitionPendingLaunchThread(ctx context.Context, threadID, caller, status, reason string, completeIntent bool) (bool, error) {
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
	if err := s.updateThreadStatus(ctx, id, status); err != nil {
		return true, err
	}
	if completeIntent {
		s.CompleteLaunchIntent(ctx, id)
	}
	s.publishThreadStopped(id, "", status, reason)
	pkglogger.Info("thread: pending_launch lifecycle fast-path", "thread_id", id, "status", status, "reason", reason, "caller", caller)
	return true, nil
}

// Unarchive 恢复已归档线程并重新打开后续会话恢复入口。
func (s *service) Unarchive(ctx context.Context, threadID string) error {
	ctx = util.NonNilContext(ctx)
	caller := archiveCallerStack()
	pkglogger.Info("thread: Unarchive() ENTERED", "thread_id", threadID, "caller", caller)
	if handled, err := s.unarchivePendingLaunchThread(ctx, threadID, caller); handled || err != nil {
		return err
	}
	if err := s.updateThreadStatus(ctx, threadID, statusCreated); err != nil {
		return err
	}
	if err := s.setBindingArchived(ctx, threadID, false); err != nil {
		return err
	}
	s.publishThreadStopped(threadID, "", statusCreated, "unarchived")
	// 归档会关闭 transport 并取消 context；恢复时先清掉旧 session，
	// 让下一次解析用同一个 provider thread UUID 重新连接并保留历史。
	s.unblockResumeForThread(ctx, threadID)
	s.resetSessionRecoveryForThread(ctx, threadID)
	s.evictZombieSession(ctx, threadID)
	// 提前后台恢复，尽量让用户发送第一条消息时会话已经可用。
	s.backgroundResumeIfNeeded(ctx, threadID)
	pkglogger.Info("thread: Unarchive() COMPLETED", "thread_id", threadID, "caller", caller)
	return nil
}

// archiveCallerStack 返回触发 Archive/Unarchive 的紧凑调用栈。
// 日志只需要定位入口链路，因此最多保留 6 层以控制字段长度。
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
