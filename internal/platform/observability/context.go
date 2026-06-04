package observability

import "context"

type traceContextKey struct{}

type TraceContext struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
}

func ContextWithTrace(ctx context.Context, trace TraceContext) context.Context {
	return context.WithValue(ctx, traceContextKey{}, trace)
}

func TraceFromContext(ctx context.Context) (TraceContext, bool) {
	trace, ok := ctx.Value(traceContextKey{}).(TraceContext)
	return trace, ok
}

func ContextWithSpan(ctx context.Context, traceID string, spanID string, parentSpanID string) context.Context {
	return ContextWithTrace(ctx, TraceContext{TraceID: traceID, SpanID: spanID, ParentSpanID: parentSpanID})
}
