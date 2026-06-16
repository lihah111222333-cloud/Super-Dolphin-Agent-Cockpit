package kernel

import (
	"context"
)

// NonNilContext returns ctx, or context.Background when callers pass nil.
func NonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// CheckCtx 处理checkctx。
func CheckCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
