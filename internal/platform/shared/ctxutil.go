package shared

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/util"
)

// NonNilContext delegates to util.NonNilContext.
func NonNilContext(ctx context.Context) context.Context { return util.NonNilContext(ctx) }

func CheckCtx(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}
