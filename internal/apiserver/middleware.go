package apiserver

import (
	"context"
	"log/slog"
	"time"

	"github.com/creachadair/jrpc2"
)

// LoggingMiddleware wraps a jrpc2.Assigner with structured request logging.
//
// V2 equivalent: scattered log.Printf calls in every handler.
// V3: centralized, zero per-handler boilerplate.
func LoggingMiddleware(base jrpc2.Assigner, logger *slog.Logger) jrpc2.Assigner {
	return assignerFunc(func(ctx context.Context, method string) jrpc2.Handler {
		h := base.Assign(ctx, method)
		if h == nil {
			return nil
		}
		return func(ctx context.Context, req *jrpc2.Request) (any, error) {
			start := time.Now()
			result, err := h(ctx, req)
			duration := time.Since(start)

			if err != nil {
				logger.Warn("rpc call failed",
					slog.String("method", method),
					slog.Duration("duration", duration),
					slog.String("error", err.Error()),
				)
			} else {
				logger.Debug("rpc call ok",
					slog.String("method", method),
					slog.Duration("duration", duration),
				)
			}
			return result, err
		}
	})
}

// assignerFunc adapts a function to the jrpc2.Assigner interface.
type assignerFunc func(ctx context.Context, method string) jrpc2.Handler

func (f assignerFunc) Assign(ctx context.Context, method string) jrpc2.Handler {
	return f(ctx, method)
}

func (f assignerFunc) Names() []string {
	// Middleware cannot enumerate — delegate to wrapped assigner.
	// For enumeration, access the base ServiceMap directly.
	return nil
}
