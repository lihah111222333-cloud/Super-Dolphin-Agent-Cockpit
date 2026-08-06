package logger

import (
	"context"
	"log/slog"
)

// withTraceAttrs 将 context 中的 trace 字段绑定到日志器；没有 trace 信息时返回原日志器。
func withTraceAttrs(ctx context.Context, base *slog.Logger) *slog.Logger {
	if base == nil {
		base = slog.Default()
	}
	if ctx == nil {
		return base
	}
	attrs := make([]any, 0, 3)
	if traceID := TraceIDFromContext(ctx); traceID != "" {
		attrs = append(attrs, FieldTraceID, traceID)
	}
	if spanID := SpanIDFromContext(ctx); spanID != "" {
		attrs = append(attrs, FieldSpanID, spanID)
	}
	if parentSpanID := ParentSpanIDFromContext(ctx); parentSpanID != "" {
		attrs = append(attrs, FieldParentSpanID, parentSpanID)
	}
	if len(attrs) == 0 {
		return base
	}
	return base.With(attrs...)
}
