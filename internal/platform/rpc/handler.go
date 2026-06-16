package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
)

type Middleware func(handler.Func) handler.Func

// CapabilityResolver is an alias for contract.CapabilityResolver.
type CapabilityResolver = contract.CapabilityResolver

// NewCapabilityResolver 创建capability解析器。
func NewCapabilityResolver(resolver contract.SessionResolver) CapabilityResolver {
	return func(ctx context.Context) (dto.CapabilitySet, error) {
		if resolver == nil {
			return nil, rpcError(CodeInvalidState, "thread session resolver is not configured")
		}
		threadID := strings.TrimSpace(ThreadIDFrom(ctx))
		if threadID == "" {
			return nil, rpcError(CodeInvalidParams, "thread id is required")
		}
		session, err := resolver.ResolveSession(ctx, threadID)
		if err != nil {
			return nil, err
		}
		if session == nil {
			return nil, rpcError(CodeInvalidState, "thread session is not available")
		}
		return session.Capabilities(), nil
	}
}

// Wrap 包装平台RPC。
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
// ThreadScope 处理线程作用域。
func ThreadScope(fields ...string) Middleware {
	if len(fields) == 0 {
		fields = []string{"threadId", "threadID", "thread_id"}
	}
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			// json.RawMessage: justified -- Wails bridge layer; middleware must
			// probe arbitrary RPC params for a thread_id field without knowing
			// the concrete param struct.
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
// CapabilityGate 处理capabilitygate。
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
// CapabilityErrorMapper 处理capability错误mapper。
func CapabilityErrorMapper() Middleware {
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			resp, err := next(ctx, req)
			if err != nil {
				if rpcErr := MapCapabilityError(err); rpcErr != nil {
					return emptyRPCResponse(), rpcErr
				}
			}
			return resp, err
		})
	}
}

// InvalidParamsMapper intercepts runtime parameter validation errors
// and maps them to CodeInvalidParams.
// InvalidParamsMapper 处理invalidparamsmapper。
func InvalidParamsMapper() Middleware {
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			resp, err := next(ctx, req)
			if err != nil {
				if rpcErr := MapInvalidParamsError(err); rpcErr != nil {
					return emptyRPCResponse(), rpcErr
				}
			}
			return resp, err
		})
	}
}

func emptyRPCResponse() any {
	return nil
}

func resolveCapabilities(ctx context.Context, resolver CapabilityResolver) (dto.CapabilitySet, error) {
	if resolver == nil {
		return nil, rpcError(CodeInvalidState, "thread capability resolver is not configured")
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

// ThreadHandler composes strict decoding, placeholder validation, and thread
// scoping into the default per-method handler chain.
// ThreadHandler 处理线程处理器。
func ThreadHandler[Req, Resp any](fn func(context.Context, Req) (Resp, error)) handler.Func {
	mws := []Middleware{ThreadScope(), Validate(), CapabilityErrorMapper()}
	return Wrap(mws...)(StrictHandler(fn))
}

// CapabilityThreadHandler composes ThreadScope, CapabilityGate, and
// StrictHandler.
// CapabilityThreadHandler 处理capability线程处理器。
func CapabilityThreadHandler[Req, Resp any](cap string, resolver CapabilityResolver, fn func(context.Context, Req) (Resp, error)) handler.Func {
	mws := []Middleware{ThreadScope(), Validate(), CapabilityGate(cap, resolver)}
	return Wrap(mws...)(StrictHandler(fn))
}

// ThreadIDFrom 从平台RPC处理线程ID。
func ThreadIDFrom(ctx context.Context) string {
	return contract.ThreadIDFrom(ctx)
}

// Logging 记录请求耗时和错误。
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
// Validate 校验平台RPC。
func Validate() Middleware {
	return func(next handler.Func) handler.Func {
		return next
	}
}

func withThreadID(ctx context.Context, threadID string) context.Context {
	return contract.WithThreadID(ctx, threadID)
}

// TracedMethod logs the method name before and after handler execution.
// It is a middleware that adds structured observability at the handler
// registration layer rather than inside individual handler bodies.
// TracedMethod 处理tracedmethod。
func TracedMethod(method string) Middleware {
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			start := time.Now()
			pkglogger.Info(method + ": rpc received")
			resp, err := next(ctx, req)
			pkglogger.Info(method+": rpc completed",
				"duration_ms", time.Since(start).Milliseconds(),
				"error", err,
			)
			return resp, err
		})
	}
}

// LoggedStrictHandler composes StrictHandler with TracedMethod, yielding a
// handler with strict object decoding and structured method-level traces.
// LoggedStrictHandler 处理loggedstrict处理器。
func LoggedStrictHandler[Req, Resp any](method string, fn func(context.Context, Req) (Resp, error)) handler.Func {
	return TracedMethod(method)(StrictHandler(fn))
}

// RequireSessionCapability returns a non-nil error when the given session does
// not advertise the requested capability.
// RequireSessionCapability 处理require会话capability。
func RequireSessionCapability(session contract.Session, cap string) error {
	if !contract.HasCapability(session.Capabilities(), cap) {
		return ErrCapabilityGate("capability not supported by active provider")
	}
	return nil
}
