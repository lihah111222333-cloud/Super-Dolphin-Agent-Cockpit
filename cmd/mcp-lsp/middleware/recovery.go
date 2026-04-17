package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func Recovery(logger *slog.Logger, toolName ...string) Middleware {
	if logger == nil {
		logger = pkglogger.Get()
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
						pkglogger.String("tool", name),
						pkglogger.String("error", err.Error()),
						pkglogger.String("stack", string(debug.Stack())),
					)
				}
			}()
			return next(ctx, params)
		}
	}
}
