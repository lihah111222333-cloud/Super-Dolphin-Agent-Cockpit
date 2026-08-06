package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// Handler 是 MCP-LSP 工具处理函数的统一签名。
type Handler func(context.Context, json.RawMessage) (any, error)

// Middleware 包装 Handler 以附加横切关注点。
type Middleware func(Handler) Handler

// Chain 把多个中间件按顺序串起来。
func Chain(handler Handler, middlewares ...Middleware) Handler {
	wrapped := handler
	for idx := range slices.Backward(middlewares) {
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
				code, retryable, _, meta := common.ClassifyToolError(name, err)
				attrs := loggingFailureAttrs(name, start, code, retryable, meta)
				logger.WarnContext(ctx, "mcp-lsp request failed",
					append(attrs, pkglogger.String("error_kind", loggingErrorKind(err)))...,
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

// loggingFailureAttrs 只保留可聚合的错误分类字段，禁止把原始参数或错误文本写入日志。
func loggingFailureAttrs(tool string, start time.Time, code string, retryable bool, meta map[string]any) []any {
	attrs := []any{
		pkglogger.String("tool", tool),
		pkglogger.Int64("duration_ms", time.Since(start).Milliseconds()),
		pkglogger.String("status", "failed"),
		slog.String("error_code", code),
		slog.Bool("retryable", retryable),
	}
	if timeoutMillis, ok := meta["timeout_ms"].(int64); ok && timeoutMillis > 0 {
		attrs = append(attrs, slog.Int64("timeout_ms", timeoutMillis))
	}
	if maxOutstanding, ok := meta["max_outstanding"].(int); ok && maxOutstanding > 0 {
		attrs = append(attrs, slog.Int("max_outstanding", maxOutstanding))
	}
	if method, ok := meta["lsp_method"].(string); ok && method == "textDocument/documentSymbol" {
		attrs = append(attrs, slog.String("lsp_method", method))
	}
	return attrs
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
