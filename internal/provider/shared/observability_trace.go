package shared

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

var traceSpanSeq atomic.Uint64

func RecordTrace(ctx context.Context, tracer *observability.Service, event observability.TraceEvent, provider string, code observability.CodeAnchor) {
	if tracer == nil {
		return
	}
	fillTraceEvent(ctx, &event, provider, code)
	_ = tracer.Record(ctx, event)
}

func fillTraceEvent(ctx context.Context, event *observability.TraceEvent, provider string, code observability.CodeAnchor) {
	fillTraceDefaults(event, provider, code)
	if trace, ok := observability.TraceFromContext(ctx); ok {
		event.TraceID = trace.TraceID
		event.ParentSpanID = trace.SpanID
	}
	if event.SpanID == "" {
		event.SpanID = provider + ":" + event.Method + ":" + traceID(traceSpanSeq.Add(1))
	}
	if captureTraceStack(event.Status) {
		event.Stack = observability.CaptureStackForStatus(traceStackConfig(), event.Status)
	}
}

func fillTraceDefaults(event *observability.TraceEvent, provider string, code observability.CodeAnchor) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.Kind == "" {
		event.Kind = "provider"
	}
	if event.Status == "" {
		event.Status = observability.StatusOK
	}
	if event.Code.File == "" {
		event.Code = code
	}
	if event.Metadata == nil {
		event.Metadata = map[string]any{}
	}
	if provider != "" {
		event.Metadata["provider"] = provider
	}
}

func captureTraceStack(status observability.Status) bool {
	return status == observability.StatusError || status == observability.StatusSlow || status == observability.StatusPanic
}

func ErrorSummary(status observability.Status) string {
	if status == observability.StatusError {
		return "provider operation failed"
	}
	return ""
}

func TraceStatus(err error) observability.Status {
	if err != nil {
		return observability.StatusError
	}
	return observability.StatusOK
}

func traceStackConfig() observability.Config {
	return observability.Config{TraceStacks: map[observability.Status]bool{observability.StatusSlow: true, observability.StatusError: true, observability.StatusPanic: true}, StackMaxFrames: 8, StackMaxBytes: 4096}
}

func traceID(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
