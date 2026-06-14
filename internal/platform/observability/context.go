package observability

import (
	"context"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type traceContextKey struct{}

type TraceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
}

// ContextWithTrace 处理带trace的上下文。
func ContextWithTrace(ctx context.Context, trace TraceContext) context.Context {
	ctx = pkglogger.WithTraceContext(ctx, trace.TraceID, trace.SpanID, trace.ParentSpanID)
	return context.WithValue(ctx, traceContextKey{}, trace)
}

// TraceFromContext 从上下文处理trace。
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

// ContextWithSpan 处理带span的上下文。
func ContextWithSpan(ctx context.Context, traceID string, spanID string, parentSpanID string) context.Context {
	return ContextWithTrace(ctx, TraceContext{TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID})
}
