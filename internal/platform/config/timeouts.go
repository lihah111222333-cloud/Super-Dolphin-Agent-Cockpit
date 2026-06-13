package config

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/util/ctxutil"
)

const (
	LaunchTimeout             = ctxutil.LaunchTimeout
	StartupTimeout            = ctxutil.StartupTimeout
	ShutdownTimeout           = ctxutil.ShutdownTimeout
	InitialThreadIDTimeout    = ctxutil.InitialThreadIDTimeout
	SessionCloseTimeout       = ctxutil.SessionCloseTimeout
	HealthCheckPeriod         = ctxutil.HealthCheckPeriod
	StallDetectDelay          = ctxutil.StallDetectDelay
	DBQueryTimeout            = ctxutil.DBQueryTimeout
	TxCleanupTimeout          = ctxutil.TxCleanupTimeout
	RPCRequestTimeout         = ctxutil.RPCRequestTimeout
	InterruptSettleTimeout    = ctxutil.InterruptSettleTimeout
	AsyncLaunchTimeout        = ctxutil.AsyncLaunchTimeout
	DreamConsolidationTimeout = ctxutil.DreamConsolidationTimeout
	PromptIntentDraftTimeout  = ctxutil.PromptIntentDraftTimeout
)

// WithTimeout 设置超时。
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeout(ctx, timeout)
}

// WithTimeoutIfNone 设置超时ifnone。
func WithTimeoutIfNone(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeoutIfNone(ctx, timeout)
}

// WithInitialThreadIDTimeout 设置initial线程ID超时。
func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithInitialThreadIDTimeout(ctx)
}

// WithSessionCloseTimeout 设置会话close超时。
func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithSessionCloseTimeout(ctx)
}

// WithDBQueryTimeout 设置数据库查询超时。
func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithDBQueryTimeout(ctx)
}

// WithTxCleanupTimeout 设置txcleanup超时。
func WithTxCleanupTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithTxCleanupTimeout(ctx)
}

// WithRPCRequestTimeout 设置RPC请求超时。
func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithRPCRequestTimeout(ctx)
}

// WithPeerTimeout 设置peer超时。
func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithPeerTimeout(ctx, timeout)
}
