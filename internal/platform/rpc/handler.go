package rpc

import (
	"context"
	"encoding/json"
	"errors"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type Middleware func(handler.Func) handler.Func

// CapabilityResolver from context returns the active provider capabilities.
type CapabilityResolver func(ctx context.Context) (dto.CapabilitySet, error)

func NewCapabilityResolver(resolver contract.SessionResolver) CapabilityResolver {
	return func(ctx context.Context) (dto.CapabilitySet, error) {
		if resolver == nil {
			return nil, errors.New("thread session resolver is not configured")
		}
		threadID := strings.TrimSpace(ThreadIDFrom(ctx))
		if threadID == "" {
			return nil, errors.New("thread id is required")
		}
		session, err := resolver.ResolveSession(ctx, threadID)
		if err != nil {
			return nil, err
		}
		if session == nil {
			return nil, errors.New("thread session is not available")
		}
		return session.Capabilities(), nil
	}
}

func Wrap(mws ...Middleware) func(handler.Func) handler.Func {
	return func(next handler.Func) handler.Func {
		wrapped := next
		for i := len(mws) - 1; i >= 0; i-- {
			wrapped = mws[i](wrapped)
		}
		return wrapped
	}
}

// ThreadScope is part of the default V3 handler chain. Unlike V2's HTTP mux
// middleware stack, recovery remains a transport/server boundary concern while
// handler middleware here focuses on request-scoped context enrichment.
// ThreadScope supports multiple parameter field names for thread id lookup.
func ThreadScope(fields ...string) Middleware {
	if len(fields) == 0 {
		fields = []string{"threadId", "threadID", "thread_id"}
	}
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			var raw map[string]json.RawMessage
			if err := req.UnmarshalParams(&raw); err != nil {
				return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "invalid params: %v", err)
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
			return nil, jrpc2.Errorf(jrpc2.Code(CodeInvalidParams), "threadId is required")
		})
	}
}

// CapabilityGate rejects calls when the active provider does not support cap.
func CapabilityGate(cap string, resolver CapabilityResolver) Middleware {
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			caps, err := resolveCapabilities(ctx, resolver)
			if err != nil {
				return nil, capabilityResolverError(ctx, err)
			}
			if !contract.HasCapability(caps, cap) {
				return nil, rpcErrorData(CodeCapabilityGate, "capability not supported by active provider", map[string]any{
					"capability": cap,
				})
			}
			return next(ctx, req)
		})
	}
}

// CapabilityErrorMapper intercepts runtime contract.CapabilityError values
// returned from handler functions and maps them to the standard -31004
// CodeCapabilityGate RPC error. This complements CapabilityGate (pre-call
// check) by catching errors from provider methods that discover capability
// gaps at execution time rather than at dispatch time.
func CapabilityErrorMapper() Middleware {
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			resp, err := next(ctx, req)
			if err != nil {
				if rpcErr := MapCapabilityError(err); rpcErr != nil {
					return nil, rpcErr
				}
			}
			return resp, err
		})
	}
}

// InvalidParamsMapper intercepts runtime parameter validation errors
// and maps them to CodeInvalidParams.
func InvalidParamsMapper() Middleware {
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			resp, err := next(ctx, req)
			if err != nil {
				if rpcErr := MapInvalidParamsError(err); rpcErr != nil {
					return nil, rpcErr
				}
			}
			return resp, err
		})
	}
}

func resolveCapabilities(ctx context.Context, resolver CapabilityResolver) (dto.CapabilitySet, error) {
	if resolver == nil {
		return nil, errors.New("thread capability resolver is not configured")
	}
	return resolver(ctx)
}

func capabilityResolverError(ctx context.Context, err error) error {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		msg = "thread session is not available; start or resume the thread first"
	}
	data := map[string]any{}
	if threadID := strings.TrimSpace(ThreadIDFrom(ctx)); threadID != "" {
		data["threadId"] = threadID
	}
	if detail := strings.TrimSpace(err.Error()); detail != "" {
		data["detail"] = detail
	}
	return rpcErrorData(CodeInvalidState, msg, data)
}

// ThreadHandler keeps the default per-method stack narrow: strict decoding,
// placeholder validation, and thread scoping. Transport-level logging/recovery
// should be layered outside handler helpers when parity with V2's outer HTTP
// middleware is required.
func ThreadHandler[Req, Resp any](fn func(context.Context, Req) (Resp, error)) handler.Func {
	return baseThreadHandler(fn)
}

// CapabilityThreadHandler composes ThreadScope, CapabilityGate, and StrictHandler.
func CapabilityThreadHandler[Req, Resp any](cap string, resolver CapabilityResolver, fn func(context.Context, Req) (Resp, error)) handler.Func {
	return baseThreadHandler(fn, CapabilityGate(cap, resolver))
}

func ThreadIDFrom(ctx context.Context) string {
	value, _ := ctx.Value(threadIDKey{}).(string)
	return value
}

func Logging(logger *pkglogger.Logger) Middleware {
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

// Validate is intentionally a no-op hook today. Keeping it in the default chain
// documents where structured request validation/metrics parity with V2 should be
// added once the RPC surface and labels are stable.
func Validate() Middleware {
	return func(next handler.Func) handler.Func {
		return next
	}
}

func withThreadID(ctx context.Context, threadID string) context.Context {
	return context.WithValue(ctx, threadIDKey{}, threadID)
}

type threadIDKey struct{}
