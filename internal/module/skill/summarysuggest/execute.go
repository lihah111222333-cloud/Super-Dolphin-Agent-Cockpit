// Package summarysuggest 提供 skill 摘要建议的执行入口，支持带重试的 dream 调用和自定义解析函数。
package summarysuggest

import (
	"context"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
)

var errRetryable = errors.New("retryable skill summary suggestion error")

// ParseFunc 是用于解析 dream 原始输出的函数类型。
type ParseFunc func(string) (string, error)

// ExecuteWithOptions 调用 dream 生成 skill 摘要建议。
// 解析失败只对已知可重试错误重跑一次，其他错误立即返回给调用方。
func ExecuteWithOptions(ctx context.Context, dream contract.DreamExecutor, prompt string, options contract.DreamOptions, parse ParseFunc) (string, error) {
	ctx, cancel := platformconfig.WithTimeoutIfNone(ctx, platformconfig.RPCRequestTimeout)
	defer cancel()

	var lastErr error
	for range 2 {
		raw, err := executeDream(ctx, dream, prompt, options)
		if err != nil {
			return "", err
		}
		value, err := parse(raw)
		if err == nil {
			return value, nil
		}
		lastErr = err
		if !IsRetryable(err) {
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

// MarkRetryable 为已知可恢复的摘要解析错误附加稳定标记。
// 原始错误保持在错误链中，供调用方保留具体诊断信息。
func MarkRetryable(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", errRetryable, err)
}

// IsRetryable 判断错误链是否带有摘要解析可重试标记。
// 仅显式标记的错误可以重试，避免错误文本变化扩大重试范围。
func IsRetryable(err error) bool {
	return errors.Is(err, errRetryable)
}
