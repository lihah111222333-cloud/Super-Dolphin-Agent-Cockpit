package config

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

const (
	LaunchTimeout             = kernel.LaunchTimeout
	StartupTimeout            = kernel.StartupTimeout
	ShutdownTimeout           = kernel.ShutdownTimeout
	InitialThreadIDTimeout    = kernel.InitialThreadIDTimeout
	SessionCloseTimeout       = kernel.SessionCloseTimeout
	HealthCheckPeriod         = kernel.HealthCheckPeriod
	StallDetectDelay          = kernel.StallDetectDelay
	DBQueryTimeout            = kernel.DBQueryTimeout
	TxCleanupTimeout          = kernel.TxCleanupTimeout
	RPCRequestTimeout         = kernel.RPCRequestTimeout
	InterruptSettleTimeout    = kernel.InterruptSettleTimeout
	AsyncLaunchTimeout        = kernel.AsyncLaunchTimeout
	DreamConsolidationTimeout = kernel.DreamConsolidationTimeout
	PromptIntentDraftTimeout  = kernel.PromptIntentDraftTimeout
)

// WithTimeout 设置超时。
func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return kernel.WithTimeout(ctx, timeout)
}

// WithTimeoutIfNone 设置超时ifnone。
func WithTimeoutIfNone(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return kernel.WithTimeoutIfNone(ctx, timeout)
}

// WithInitialThreadIDTimeout 设置initial线程ID超时。
func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return kernel.WithInitialThreadIDTimeout(ctx)
}

// WithSessionCloseTimeout 设置会话close超时。
func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return kernel.WithSessionCloseTimeout(ctx)
}

// WithDBQueryTimeout 设置数据库查询超时。
func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return kernel.WithDBQueryTimeout(ctx)
}

// WithTxCleanupTimeout 设置txcleanup超时。
func WithTxCleanupTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return kernel.WithTxCleanupTimeout(ctx)
}

// WithRPCRequestTimeout 设置RPC请求超时。
func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return kernel.WithRPCRequestTimeout(ctx)
}

// WithPeerTimeout 设置peer超时。
func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return kernel.WithPeerTimeout(ctx, timeout)
}
