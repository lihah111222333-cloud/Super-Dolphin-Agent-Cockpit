// Package summarysuggest 提供 skill 摘要建议的执行入口，支持带重试的 dream 调用和自定义解析函数。
package summarysuggest

import (
	"context"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// ParseFunc 是用于解析 dream 原始输出的函数类型。
type ParseFunc func(string) (string, error)

// ExecuteWithOptions 调用 dream 生成 skill 摘要建议。
// 解析失败只对已知可重试错误重跑一次，其他错误立即返回给调用方。
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

// executeDream 调用 dream executor，优先使用带选项的接口。
func executeDream(ctx context.Context, dream contract.DreamExecutor, prompt string, options contract.DreamOptions) (string, error) {
	if withOptions, ok := dream.(contract.DreamExecutorWithOptions); ok {
		return withOptions.ExecuteDreamWithOptions(ctx, prompt, options)
	}
	return dream.ExecuteDream(ctx, prompt)
}

// retryable 只允许摘要为空或解析格式错误触发重试。
// 业务错误和执行器错误不在这里吞掉，避免掩盖 provider 侧故障。
func retryable(err error) bool {
	message := err.Error()
	return strings.Contains(message, "parse skill summary suggestion") ||
		strings.Contains(message, "skill summary suggestion is empty")
}
