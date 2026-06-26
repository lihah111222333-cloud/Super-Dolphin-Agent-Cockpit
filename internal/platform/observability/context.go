package observability

import (
	"context"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type traceContextKey struct{}

// TraceContext 是跨 logger 和 observability 传递的 trace/span 标识集合。
type TraceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
}

// ContextWithTrace 同时写入 pkglogger trace 字段和本包私有 context key，保证两套读取路径一致。
func ContextWithTrace(ctx context.Context, trace TraceContext) context.Context {
	ctx = pkglogger.WithTraceContext(ctx, trace.TraceID, trace.SpanID, trace.ParentSpanID)
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceFromContext 优先读取本包 trace，再回退到 pkglogger 上下文字段。
func TraceFromContext(ctx context.Context) (TraceContext, bool) {
	if trace, ok := ctx.Value(traceContextKey{}).(TraceContext); ok {
		return trace, true
	}
	trace := TraceContext{
		TraceID:      pkglogger.TraceIDFromContext(ctx),
		SpanID:       pkglogger.SpanIDFromContext(ctx),
		ParentSpanID: pkglogger.ParentSpanIDFromContext(ctx),
	}
	if trace.TraceID == "" && trace.SpanID == "" && trace.ParentSpanID == "" {
		return TraceContext{}, false
	}
	return trace, true
}

// ContextWithSpan 是写入 trace/span/parentSpan 的便捷入口。
func ContextWithSpan(ctx context.Context, traceID string, spanID string, parentSpanID string) context.Context {
	return ContextWithTrace(ctx, TraceContext{TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID})
}
