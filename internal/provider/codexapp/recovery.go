package codexapp

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type recoveryManager struct {
	transport *transport
	logger    *slog.Logger
	maxRetry  int
}

func (r *recoveryManager) CheckHealth(ctx context.Context) error {
	if r.transport == nil || !r.transport.Running() {
		return errors.New("codexapp: transport not running")
	}
	callCtx, cancel := withTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := r.transport.Call(callCtx, "app/list", nil)
	return err
}

func (r *recoveryManager) Reconnect(ctx context.Context) error {
	if r.transport == nil {
		return errors.New("codexapp: transport not configured")
	}
	attempts := r.maxRetry
	if attempts <= 0 {
		attempts = 1
	}
	if r.logger != nil {
		r.logger.Warn("codexapp reconnect", "attempts", attempts)
	}
	return shared.Retry(ctx, attempts, 200*time.Millisecond, func() error {
		callCtx, cancel := withTimeout(ctx, 5*time.Second)
		defer cancel()
		return r.transport.reconnect(callCtx)
	})
}
