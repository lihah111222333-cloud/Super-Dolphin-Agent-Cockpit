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
)

func WithTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

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

func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, InitialThreadIDTimeout)
}

func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, SessionCloseTimeout)
}

func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, DBQueryTimeout)
}

func WithTxCleanupTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, TxCleanupTimeout)
}

func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return WithTimeout(ctx, RPCRequestTimeout)
}

func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return WithTimeout(ctx, timeout)
}
