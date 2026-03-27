package middleware

import (
	"context"
	"encoding/json"
	"time"
)

const (
	TierFast   = 5 * time.Second
	TierNormal = 30 * time.Second
	TierSlow   = 120 * time.Second
	TierExec   = 300 * time.Second
)

func Timeout(limit time.Duration) Middleware {
	if limit <= 0 {
		limit = TierNormal
	}
	return func(next Handler) Handler {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			if _, ok := ctx.Deadline(); ok {
				return next(ctx, params)
			}
			timeoutCtx, cancel := context.WithTimeout(ctx, limit)
			defer cancel()
			return next(timeoutCtx, params)
		}
	}
}

func ClampTimeout(requestedSeconds int, fallback time.Duration, ceiling time.Duration) time.Duration {
	if fallback <= 0 {
		fallback = TierNormal
	}
	if ceiling <= 0 {
		ceiling = fallback
	}
	if requestedSeconds <= 0 {
		return fallback
	}
	requested := time.Duration(requestedSeconds) * time.Second
	if requested > ceiling {
		return ceiling
	}
	return requested
}
