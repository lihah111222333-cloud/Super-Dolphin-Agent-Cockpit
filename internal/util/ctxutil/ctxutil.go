// Package ctxutil 集中管理后端共享的 context 超时值和包装函数。
package ctxutil

import (
	"context"
	"time"
)

// 后端跨模块默认超时；调用方通过对应 wrapper 复用，避免散落硬编码。
const (
	LaunchTimeout             = 30 * time.Second
	StartupTimeout            = 30 * time.Second
	ShutdownTimeout           = 15 * time.Second
	InitialThreadIDTimeout    = 5 * time.Second
	SessionCloseTimeout       = 5 * time.Second
	HealthCheckPeriod         = 5 * time.Second
	StallDetectDelay          = 90 * time.Second
	DBQueryTimeout            = 10 * time.Second
	TxCleanupTimeout          = 1 * time.Second
	RPCRequestTimeout         = 30 * time.Second
	InterruptSettleTimeout    = 6 * time.Second
	AsyncLaunchTimeout        = 60 * time.Second
	DreamConsolidationTimeout = 5 * time.Minute
	PromptIntentDraftTimeout  = DreamConsolidationTimeout
)

// WithTimeout 在缺省 context 时补 Background，并只在 timeout 为正数时创建 deadline。
// 非正超时返回空 cancel，调用方仍可统一 defer cancel。
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// WithTimeoutIfNone 仅在上游尚无 deadline 时补默认超时，避免缩短调用方已设置的生命周期。
func WithTimeoutIfNone(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return WithTimeout(ctx, timeout)
}

// WithInitialThreadIDTimeout 限制 provider 初始线程 ID 回填等待时间。
func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, InitialThreadIDTimeout)
}

// WithSessionCloseTimeout 限制会话关闭路径的资源释放等待时间。
func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, SessionCloseTimeout)
}

// WithDBQueryTimeout 为普通数据库查询设置共享超时。
func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, DBQueryTimeout)
}

// WithTxCleanupTimeout 为事务清理设置短超时，避免失败路径长期占用连接。
func WithTxCleanupTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, TxCleanupTimeout)
}

// WithRPCRequestTimeout 为本地 MCP/RPC 请求设置默认超时。
func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, RPCRequestTimeout)
}

// WithPeerTimeout 为 peer 操作设置可取消 context；timeout 非正时只提供取消能力。
func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return WithTimeout(ctx, timeout)
}
