package config

import (
	"context"
	"time"
)

const (
	TurnTimeout            = 10 * time.Minute
	LaunchTimeout          = 30 * time.Second
	ShutdownTimeout        = 15 * time.Second
	InitialThreadIDTimeout = 5 * time.Second
	SessionCloseTimeout    = 5 * time.Second
	HealthCheckPeriod      = 5 * time.Second
	StallDetectDelay       = 90 * time.Second
	DBQueryTimeout         = 10 * time.Second
	RPCRequestTimeout      = 30 * time.Second
	InterruptSettleTimeout = 6 * time.Second
)

func WithInitialThreadIDTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, InitialThreadIDTimeout)
}

func WithSessionCloseTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, SessionCloseTimeout)
}

func WithDBQueryTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, DBQueryTimeout)
}

func WithRPCRequestTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, RPCRequestTimeout)
}

// WithPeerTimeout wraps context.WithTimeout for peer callback scenarios.
// Allowed by TestTimeoutLocality whitelist.
func WithPeerTimeout(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, timeout)
}
