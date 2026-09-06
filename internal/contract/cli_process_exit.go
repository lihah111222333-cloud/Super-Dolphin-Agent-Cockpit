package contract

import (
	"context"
	"errors"
)

type launchSeqContextKey struct{}

// WithLaunchSeq 让 launcher 把 lifecycle owner 已分配的 launch fence 传给独立执行器。
func WithLaunchSeq(ctx context.Context, seq uint64) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, launchSeqContextKey{}, seq)
}

// LaunchSeqFromContext 读取 launcher 传入的生命周期围栏。
func LaunchSeqFromContext(ctx context.Context) (uint64, bool) {
	if ctx == nil {
		return 0, false
	}
	seq, ok := ctx.Value(launchSeqContextKey{}).(uint64)
	return seq, ok && seq != 0
}

// CLIProcessExit 描述受管 CLI 进程的一次、且仅一次退出通知。
// SessionID 是 provider 返回的真实 thread/session 身份，LaunchSeq 是宿主
// 生命周期代际；消费端必须同时校验两者，未知身份不得被当作退出事实。
type CLIProcessExit struct {
	AgentID    string
	SessionID  string
	LaunchSeq  uint64
	Err        error
	Unexpected bool
}

// CLIProcessExitBinding 将 transport 退出与宿主身份绑定。
// 空 binding 允许旧 Start API 保持兼容，但不会产生可消费的退出事件。
type CLIProcessExitBinding struct {
	AgentID   string
	SessionID string
	LaunchSeq uint64
	OnExit    func(CLIProcessExit)
}

// Valid 只接受可安全路由到 lifecycle owner 的事件。
func (e CLIProcessExit) Valid() bool {
	return e.AgentID != "" && e.SessionID != "" && e.LaunchSeq != 0
}

// Valid 判断 binding 是否具备完整的身份和回调。
func (b CLIProcessExitBinding) Valid() bool {
	return b.AgentID != "" && b.SessionID != "" && b.LaunchSeq != 0 && b.OnExit != nil
}

// ErrMissingCLIProcessExitIdentity 用于明确拒绝缺少生命周期身份的绑定。
var ErrMissingCLIProcessExitIdentity = errors.New("CLI process exit binding requires agent, session and launch sequence")
