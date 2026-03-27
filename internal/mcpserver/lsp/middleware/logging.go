package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"
)

type Handler func(context.Context, json.RawMessage) (any, error)

type Middleware func(Handler) Handler

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

func Logging(logger *slog.Logger, toolName ...string) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	name := ""
	if len(toolName) > 0 {
		name = strings.TrimSpace(toolName[0])
	}
	return func(next Handler) Handler {
		return func(ctx context.Context, params json.RawMessage) (any, error) {
			start := time.Now()
			logger.DebugContext(ctx, "mcp-lsp request",
				slog.String("tool", name),
				slog.Int("request_bytes", len(params)),
				slog.String("request", previewValue(params)),
			)
			result, err := next(ctx, params)
			if err != nil {
				logger.WarnContext(ctx, "mcp-lsp request failed",
					slog.String("tool", name),
					slog.Int64("duration_ms", time.Since(start).Milliseconds()),
					slog.String("error", err.Error()),
				)
				return nil, err
			}
			logger.DebugContext(ctx, "mcp-lsp response",
				slog.String("tool", name),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("response", previewAny(result)),
			)
			return result, nil
		}
	}
}

func previewAny(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "<unmarshalable>"
	}
	return previewValue(raw)
}

func previewValue(raw []byte) string {
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
