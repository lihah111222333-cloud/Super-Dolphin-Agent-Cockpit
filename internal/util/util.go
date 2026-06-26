// Package util 放置模块层共享的轻量 helper。
// 本包避免依赖 internal/*，防止通用工具把模块依赖方向拉乱。
package util

import (
	"context"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// FirstNonEmpty 返回第一个 TrimSpace 后非空的值，所有值为空时返回空字符串。
func FirstNonEmpty(values ...string) string {
	return FirstTrimmed(values...)
}

// FirstTrimmed 返回第一个 TrimSpace 后非空的值，并返回裁剪后的结果。
func FirstTrimmed(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// ClampLimit 将分页或数量限制收敛到允许范围；低于 min 时使用调用方给出的默认值。
func ClampLimit(val, min, max, defaultVal int) int {
	if val < min {
		return defaultVal
	}
	if max > 0 && val > max {
		return max
	}
	return val
}

// NonNilContext 为 nil context 提供 Background，便于下游统一传递。
func NonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// LogIgnoredError 记录被调用方明确选择不继续传播的错误。
// logger 或 err 为空时不输出，避免清理路径产生额外噪声。
func LogIgnoredError(logger *pkglogger.Logger, msg string, err error) {
	if err != nil && logger != nil {
		logger.Warn(msg, "error", err)
	}
}

// IsRemoteTurnInput 判断 turn 输入是否是 HTTP(S) 远程地址。
func IsRemoteTurnInput(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}
