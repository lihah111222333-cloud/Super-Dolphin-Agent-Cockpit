package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
)

func Recovery(logger *slog.Logger, toolName ...string) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	name := ""
	if len(toolName) > 0 {
		name = strings.TrimSpace(toolName[0])
	}
	return func(next Handler) Handler {
		return func(ctx context.Context, params json.RawMessage) (_ any, err error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					err = fmt.Errorf("panic recovered: %v", recovered)
					logger.ErrorContext(ctx, "mcp-lsp panic recovered",
						slog.String("tool", name),
						slog.String("error", err.Error()),
						slog.String("stack", string(debug.Stack())),
					)
				}
			}()
			return next(ctx, params)
		}
	}
}
