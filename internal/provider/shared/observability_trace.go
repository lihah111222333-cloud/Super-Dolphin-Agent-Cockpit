package shared

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

var traceSpanSeq atomic.Uint64

// RecordTrace 补齐 provider trace 默认字段并写入观测服务。
// tracer 为空时直接返回；写入失败只告警，不能反向影响 provider 主流程。
func RecordTrace(ctx context.Context, tracer *observability.Service, event observability.TraceEvent, provider string, code observability.CodeAnchor) {
	if tracer == nil {
		return
	}
	fillTraceEvent(ctx, &event, provider, code)
	if err := tracer.Record(ctx, event); err != nil {
		observability.WarnRecordError(nil, "provider.shared", event, err)
	}
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

// fillTraceDefaults 为 provider trace 事件填充时间、状态、代码锚点和 provider 元数据。
// 已由调用方设置的字段不会被覆盖，便于特殊事件自定义状态。
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

// ErrorSummary 返回 provider trace 状态对应的默认错误摘要。
// 当前只对错误状态给出通用文案，其他状态保持空摘要。
func ErrorSummary(status observability.Status) string {
	if status == observability.StatusError {
		return "provider operation failed"
	}
	return ""
}

// ErrorSummaryForError 优先返回脱敏后的真实错误摘要，缺失错误时保留旧的状态摘要。
func ErrorSummaryForError(status observability.Status, err error) string {
	if preview := observability.SafeErrorPreview(err); preview != "" {
		return preview
	}
	return ErrorSummary(status)
}

// ErrorMetadata 返回 provider trace 的统一错误字段。
func ErrorMetadata(err error) map[string]any {
	return observability.SafeErrorMetadata(err, "provider_error")
}

// TraceStatus 将 error 映射为 provider trace 状态。
// nil 表示 OK，非 nil 统一视为错误，细分状态由调用方显式设置。
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
