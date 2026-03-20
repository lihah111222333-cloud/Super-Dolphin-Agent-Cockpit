package rpc

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type Middleware func(handler.Func) handler.Func

// CapabilityResolver from context returns the active provider capabilities.
type CapabilityResolver func(ctx context.Context) dto.CapabilitySet

func Wrap(mws ...Middleware) func(handler.Func) handler.Func {
	return func(next handler.Func) handler.Func {
		wrapped := next
		for i := len(mws) - 1; i >= 0; i-- {
			wrapped = mws[i](wrapped)
		}
		return wrapped
	}
}

// ThreadScope supports multiple parameter field names for thread id lookup.
func ThreadScope(fields ...string) Middleware {
	if len(fields) == 0 {
		fields = []string{"threadId", "threadID", "thread_id"}
	}
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			var raw map[string]json.RawMessage
			if err := req.UnmarshalParams(&raw); err != nil {
				return nil, jrpc2.Errorf(jrpc2.InvalidParams, "invalid params: %v", err)
			}
			for _, field := range fields {
				tidRaw, ok := raw[field]
				if !ok || len(tidRaw) == 0 {
					continue
				}
				var threadID string
				if err := json.Unmarshal(tidRaw, &threadID); err != nil || threadID == "" {
					continue
				}
				return next(withThreadID(ctx, threadID), req)
			}
			return nil, jrpc2.Errorf(jrpc2.InvalidParams, "threadId is required")
		})
	}
}

// CapabilityGate rejects calls when the active provider does not support cap.
func CapabilityGate(cap string, resolver CapabilityResolver) Middleware {
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			var caps dto.CapabilitySet
			if resolver != nil {
				caps = resolver(ctx)
			}
			if !caps.Has(cap) {
				return nil, jrpc2.Errorf(jrpc2.Code(CodeCapabilityGate), "capability not supported by active provider").WithData(map[string]any{
					"capability": cap,
				})
			}
			return next(ctx, req)
		})
	}
}

func ThreadIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(threadIDKey{}).(string)
	return value
}

func Logging(logger *slog.Logger) Middleware {
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			start := time.Now()
			resp, err := next(ctx, req)
			if logger != nil {
				logger.Info("rpc",
					"method", req.Method(),
					"duration_ms", time.Since(start).Milliseconds(),
					"error", err,
				)
			}
			return resp, err
		})
	}
}

func Validate() Middleware {
	return func(next handler.Func) handler.Func {
		return next
	}
}

func withThreadID(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, threadIDKey{}, threadID)
}

type threadIDKey struct{}
