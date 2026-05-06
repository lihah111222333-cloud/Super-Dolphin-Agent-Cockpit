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
)

func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeout(ctx, timeout)
}

func WithTimeoutIfNone(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithTimeoutIfNone(ctx, timeout)
}

func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithInitialThreadIDTimeout(ctx)
}

func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithSessionCloseTimeout(ctx)
}

func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithDBQueryTimeout(ctx)
}

func WithTxCleanupTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithTxCleanupTimeout(ctx)
}

func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return ctxutil.WithRPCRequestTimeout(ctx)
}

func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return ctxutil.WithPeerTimeout(ctx, timeout)
}
