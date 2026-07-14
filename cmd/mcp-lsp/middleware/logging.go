package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
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
				pkglogger.String("status", "started"),
			)
			result, err := next(ctx, params)
			if err != nil {
				logger.WarnContext(ctx, "mcp-lsp request failed",
					pkglogger.String("tool", name),
					pkglogger.Int64("duration_ms", time.Since(start).Milliseconds()),
					pkglogger.String("status", "failed"),
					pkglogger.String("error_kind", loggingErrorKind(err)),
				)
				return result, err
			}
			responseBytes := 0
			if raw, marshalErr := json.Marshal(result); marshalErr == nil {
				responseBytes = len(raw)
			}
			logger.DebugContext(ctx, "mcp-lsp response",
				pkglogger.String("tool", name),
				pkglogger.Int64("duration_ms", time.Since(start).Milliseconds()),
				pkglogger.Int("response_bytes", responseBytes),
				pkglogger.String("status", "succeeded"),
			)
			return result, nil
		}
	}
}

func loggingErrorKind(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "handler_error"
	}
}
