// Package ctxutil provides context-aware timeout helpers.
package ctxutil

import (
	"context"
	"time"
)

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

// WithTimeout 设置超时。
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

// WithTimeoutIfNone 设置超时ifnone。
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

// WithInitialThreadIDTimeout 设置initial线程ID超时。
func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, InitialThreadIDTimeout)
}

// WithSessionCloseTimeout 设置会话close超时。
func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, SessionCloseTimeout)
}

// WithDBQueryTimeout 设置数据库查询超时。
func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, DBQueryTimeout)
}

// WithTxCleanupTimeout 设置txcleanup超时。
func WithTxCleanupTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, TxCleanupTimeout)
}

// WithRPCRequestTimeout 设置RPC请求超时。
func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, RPCRequestTimeout)
}

// WithPeerTimeout 设置peer超时。
func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return WithTimeout(ctx, timeout)
}
