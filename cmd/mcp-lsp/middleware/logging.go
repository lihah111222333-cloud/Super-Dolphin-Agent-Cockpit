package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Handler 是 MCP-LSP 工具处理函数的统一签名。
type Handler func(context.Context, json.RawMessage) (any, error)

// Middleware 包装 Handler 以附加横切关注点。
type Middleware func(Handler) Handler

// Chain 把多个中间件按顺序串起来。
func Chain(handler Handler, middlewares ...Middleware) Handler {
	wrapped := handler
	for idx := len(middlewares) - 1; idx >= 0; idx-- {
		if middlewares[idx] == nil {
			continue
		}
		wrapped = middlewares[idx](wrapped)
	}
	return wrapped
}

// Logging 记录请求耗时和错误。
func Logging(logger *slog.Logger, toolName ...string) Middleware {
	if logger == nil {
		logger = pkglogger.Get()
	}
	name := ""
	if len(toolName) > 0 {
		name = strings.TrimSpace(toolName[0])
	}
	return func(next Handler) Handler {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			start := time.Now()
			logger.DebugContext(ctx, "mcp-lsp request",
				pkglogger.String("tool", name),
				pkglogger.Int("request_bytes", len(params)),
				pkglogger.String("request", compactValue(params)),
			)
			result, err := next(ctx, params)
			if err != nil {
				logger.WarnContext(ctx, "mcp-lsp request failed",
					pkglogger.String("tool", name),
					pkglogger.Int64("duration_ms", time.Since(start).Milliseconds()),
					pkglogger.String("error", err.Error()),
				)
				return result, err
			}
			logger.DebugContext(ctx, "mcp-lsp response",
				pkglogger.String("tool", name),
				pkglogger.Int64("duration_ms", time.Since(start).Milliseconds()),
				pkglogger.String("response", compactAny(result)),
			)
			return result, nil
		}
	}
}

// compactAny 将任意值序列化后截断，用于日志输出。
func compactAny(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "<unmarshalable>"
	}
	return compactValue(raw)
}

// compactValue 截断原始字节到 2048 字符，用于日志输出。
func compactValue(raw []byte) string {
	const limit = 2048
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit] + "...(truncated)"
}
