package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

// Middleware 是 jrpc2 handler 的包装函数。
type Middleware func(handler.Func) handler.Func

// CapabilityResolver 是 contract.CapabilityResolver 在 RPC 层的别名。
type CapabilityResolver = contract.CapabilityResolver

// NewCapabilityResolver 构造基于当前 thread session 的能力解析器。
// 缺少 resolver、threadID 或 session 时直接返回 RPC 错误，避免能力门禁静默放行。
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

// Wrap 按声明顺序组合多个中间件。
func Wrap(mws ...Middleware) func(handler.Func) handler.Func {
	return func(next handler.Func) handler.Func {
		wrapped := next
		for i := len(mws) - 1; i >= 0; i-- {
			wrapped = mws[i](wrapped)
		}
		return wrapped
	}
}

// ThreadScope 从 RPC params 中提取 threadID 并写入 context。
// handler 中间件只做请求上下文增强；panic 恢复仍属于 transport/server 边界。
func ThreadScope(fields ...string) Middleware {
	if len(fields) == 0 {
		fields = []string{"threadId", "threadID", "thread_id"}
	}
	return func(next handler.Func) handler.Func {
		return handler.Func(func(ctx context.Context, req *jrpc2.Request) (any, error) {
			// Wails 桥接层传入动态 JSON；中间件只能探测 thread_id 字段，
			// 不能依赖具体请求结构体。
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

// CapabilityGate 在调用前校验当前 provider 是否支持目标能力。
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

// CapabilityErrorMapper 把 handler 运行期返回的 CapabilityError 映射为统一 RPC 错误。
// 它补足 CapabilityGate 的调用前检查，覆盖 provider 执行时才发现能力缺口的场景。
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

// InvalidParamsMapper 将 handler 返回的 jrpc2.InvalidParams 统一映射到平台参数错误码。
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

// resolveCapabilities 调用能力解析器，resolver 缺失时 fail-fast。
func resolveCapabilities(ctx context.Context, resolver CapabilityResolver) (dto.CapabilitySet, error) {
	if resolver == nil {
		return nil, rpcError(CodeInvalidState, "thread capability resolver is not configured")
	}
	return resolver(ctx)
}

// capabilityResolverError 把 session/capability 解析失败转换为带 threadId 诊断的 RPC 错误。
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

// ThreadHandler 组合严格解码、线程作用域和能力错误映射，作为默认 thread 方法链。
func ThreadHandler[Req, Resp any](fn func(context.Context, Req) (Resp, error)) handler.Func {
	mws := []Middleware{ThreadScope(), Validate(), CapabilityErrorMapper()}
	return Wrap(mws...)(StrictHandler(fn))
}

// CapabilityThreadHandler 在默认 thread 方法链上追加调用前能力门禁。
func CapabilityThreadHandler[Req, Resp any](cap string, resolver CapabilityResolver, fn func(context.Context, Req) (Resp, error)) handler.Func {
	mws := []Middleware{ThreadScope(), Validate(), CapabilityGate(cap, resolver)}
	return Wrap(mws...)(StrictHandler(fn))
}

// ThreadIDFrom 从 context 中读取 RPC threadID。
func ThreadIDFrom(ctx context.Context) string {
	return contract.ThreadIDFrom(ctx)
}

// Logging 记录单次 RPC handler 调用耗时和错误。
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

// Validate 是当前保留的空校验中间件。
// 它固定结构化请求校验的插入点，等待 RPC 标签和指标稳定后再补充实现。
func Validate() Middleware {
	return func(next handler.Func) handler.Func {
		return next
	}
}

// withThreadID 把 threadID 写入 contract 共享 context key。
func withThreadID(ctx context.Context, threadID string) context.Context {
	return contract.WithThreadID(ctx, threadID)
}

// TracedMethod 在 handler 注册层记录方法开始和结束日志。
// 这样单个 handler 体内无需重复写结构化 trace 日志。
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

// LoggedStrictHandler 组合严格对象解码和方法级 trace 日志。
func LoggedStrictHandler[Req, Resp any](method string, fn func(context.Context, Req) (Resp, error)) handler.Func {
	return TracedMethod(method)(StrictHandler(fn))
}

// RequireSessionCapability 校验 session 是否声明目标能力。
func RequireSessionCapability(session contract.Session, cap string) error {
	if !contract.HasCapability(session.Capabilities(), cap) {
		return ErrCapabilityGate("capability not supported by active provider")
	}
	return nil
}
