package summarysuggest

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

type ParseFunc func(string) (string, error)

// ExecuteWithOptions 执行带选项的技能。
func ExecuteWithOptions(ctx context.Context, dream contract.DreamExecutor, prompt string, options contract.DreamOptions, parse ParseFunc) (string, error) {
	ctx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.RPCRequestTimeout)
	defer cancel()

	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := executeDream(ctx, dream, prompt, options)
		if err != nil {
			return "", err
		}
		value, err := parse(raw)
		if err == nil {
			return value, nil
		}
		lastErr = err
		if !retryable(err) {
			return "", err
		}
	}
	return "", lastErr
}

func executeDream(ctx context.Context, dream contract.DreamExecutor, prompt string, options contract.DreamOptions) (string, error) {
	if withOptions, ok := dream.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, options)
	}
	return dream.ExecuteDream(ctx, prompt)
}

func retryable(err error) bool {
	message := err.Error()
	return strings.Contains(message, "parse skill summary suggestion") ||
		strings.Contains(message, "skill summary suggestion is empty")
}
