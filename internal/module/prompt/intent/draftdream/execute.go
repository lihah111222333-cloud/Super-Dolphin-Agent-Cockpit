package draftdream

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type ParseFunc[T any] func(string) (T, error)

func Execute[T any](ctx context.Context, dream contract.DreamExecutor, prompt string, parse ParseFunc[T]) (T, error) {
	ctx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.RPCRequestTimeout)
	defer cancel()

	var zero T
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := dream.ExecuteDream(ctx, prompt)
		if err != nil {
			return zero, err
		}
		value, err := parse(raw)
		if err == nil {
			return value, nil
		}
		lastErr = err
	}
	return zero, lastErr
}
