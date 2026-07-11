package turn

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// InterruptActiveTurn 中断当前线程的活跃 turn，并等待 provider handle 与 tracker 收敛。
// 没有活跃 turn 时直接成功返回，供线程关闭流程幂等调用。
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

// CleanupThread 在线程关闭时终止本地 tracker 状态，并清理该线程的大工具结果生命周期记录。
// 该路径不访问 provider，只负责本进程内状态和落盘缓存的关闭收口。
func (s *service) CleanupThread(_ context.Context, threadID, reason string) error {
	s.tracker.AbortThread(threadID, reason)
	resetToolResultLifecycle(threadID)
	return nil
}
