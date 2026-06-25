// Package draftdream 封装提示词意图草稿的 dream 执行逻辑，支持自动重试和选项透传。
package draftdream

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// ParseFunc 是将 LLM 原始字符串输出解析为目标类型的函数类型。
type ParseFunc[T any] func(string) (T, error)

// Execute 执行一次 dream 调用并解析结果，不带额外选项。
func Execute[T any](ctx context.Context, dream contract.DreamExecutor, prompt string, parse ParseFunc[T]) (T, error) {
	return ExecuteWithOptions(ctx, dream, prompt, contract.DreamOptions{}, parse)
}

// ExecuteWithOptions 执行带选项的 dream 调用，解析失败时最多重试一次，超时从 context 继承。
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

// executeDream 优先使用带选项接口，不支持时降级到基础接口。
func executeDream(ctx context.Context, dream contract.DreamExecutor, prompt string, options contract.DreamOptions) (string, error) {
	if withOptions, ok := dream.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, options)
	}
	return dream.ExecuteDream(ctx, prompt)
}
