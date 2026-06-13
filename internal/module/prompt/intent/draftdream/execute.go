package draftdream

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type ParseFunc[T any] func(string) (T, error)

// Execute 执行prompt。
func Execute[T any](ctx context.Context, dream contract.DreamExecutor, prompt string, parse ParseFunc[T]) (T, error) {
	return ExecuteWithOptions(ctx, dream, prompt, contract.DreamOptions{}, parse)
}

// ExecuteWithOptions 执行带选项的prompt。
func ExecuteWithOptions[T any](ctx context.Context, dream contract.DreamExecutor, prompt string, options contract.DreamOptions, parse ParseFunc[T]) (T, error) {
	ctx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.PromptIntentDraftTimeout)
	defer cancel()

	var zero T
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := executeDream(ctx, dream, prompt, options)
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

func executeDream(ctx context.Context, dream contract.DreamExecutor, prompt string, options contract.DreamOptions) (string, error) {
	if withOptions, ok := dream.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, options)
	}
	return dream.ExecuteDream(ctx, prompt)
}
