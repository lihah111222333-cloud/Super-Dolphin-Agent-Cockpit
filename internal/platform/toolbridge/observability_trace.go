package toolbridge

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

var toolTraceSpanSeq atomic.Uint64

func (h *Handler) recordToolTrace(ctx context.Context, event observability.TraceEvent) {
	if h == nil || h.tracer == nil {
		return
	}
	fillToolTrace(ctx, &event)
	if err := h.tracer.Record(ctx, event); err != nil {
		observability.WarnRecordError(h.logger, "toolbridge", event, err)
	}
}

func beginToolTraceContext(ctx context.Context) context.Context {
	trace, _ := observability.TraceFromContext(ctx)
	parentSpanID := trace.SpanID
	if trace.TraceID == "" {
		trace.TraceID = nextToolTraceSpan("trace")
	}
	trace.ParentSpanID = parentSpanID
	trace.SpanID = nextToolTraceSpan("tool.call")
	return observability.ContextWithTrace(ctx, trace)
}

// fillToolTrace 处理fill工具trace。
func fillToolTrace(ctx context.Context, event *observability.TraceEvent) {
	fillToolTraceDefaults(event)
	if trace, ok := observability.TraceFromContext(ctx); ok {
		event.TraceID = trace.TraceID
		if event.SpanID == "" {
			event.SpanID = trace.SpanID
			event.ParentSpanID = trace.ParentSpanID
		} else if event.ParentSpanID == "" {
			event.ParentSpanID = trace.SpanID
		}
	}
	if event.SpanID == "" {
		event.SpanID = nextToolTraceSpan(event.Method)
	}
	if shouldCaptureToolStack(event.Status) {
		event.Stack = observability.CaptureStackForStatus(toolTraceStackConfig(), event.Status)
	}
}

func fillToolTraceDefaults(event *observability.TraceEvent) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	if event.Kind == "" {
		event.Kind = "toolbridge"
	}
	if event.Code.File == "" {
		event.Code = observability.CodeAnchor{File: "internal/platform/toolbridge/handler.go", Function: "toolbridge.(*Handler).HandleToolCall", Line: 89}
	}
	if event.Status == "" {
		event.Status = observability.StatusOK
	}
}

func shouldCaptureToolStack(status observability.Status) bool {
	return status == observability.StatusError || status == observability.StatusSlow || status == observability.StatusPanic
}

func nextToolTraceSpan(method string) string {
	return "toolbridge:" + method + ":" + formatUint(toolTraceSpanSeq.Add(1))
}

func formatUint(v uint64) string {
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

func toolTraceStackConfig() observability.Config {
	return observability.Config{TraceStacks: map[observability.Status]bool{observability.StatusSlow: true, observability.StatusError: true, observability.StatusPanic: true}, StackMaxFrames: 8, StackMaxBytes: 4096}
}

func toolTraceBeginEvent(req ToolCallRequest) observability.TraceEvent {
	return observability.TraceEvent{
		Method:      "tool.call.begin",
		ThreadID:    req.ThreadID,
		AgentID:     req.AgentID,
		TurnID:      req.TurnID,
		CallID:      req.CallID,
		ToolName:    req.Name,
		ClientKind:  classifyTool(req.Name),
		ClientRoute: req.ClientKind,
		Status:      observability.StatusOK,
		Metadata: map[string]any{
			"argument_bytes": int64(len(req.Arguments)),
		},
	}
}

func toolTraceEndEvent(req ToolCallRequest, result any, callErr error, elapsed time.Duration, affectedFiles int) observability.TraceEvent {
	success := callErr == nil && toolTraceResultSuccess(result)
	status := observability.StatusOK
	if !success {
		status = observability.StatusError
	}
	return observability.TraceEvent{
		Method:      "tool.call.end",
		ThreadID:    req.ThreadID,
		AgentID:     req.AgentID,
		TurnID:      req.TurnID,
		CallID:      req.CallID,
		ToolName:    req.Name,
		ClientKind:  classifyTool(req.Name),
		ClientRoute: req.ClientKind,
		DurationMS:  elapsed.Milliseconds(),
		Status:      status,
		Error:       toolTraceErrorSummary(status),
		Metadata: map[string]any{
			"success":              success,
			"result_bytes":         int64(toolTraceJSONSize(result)),
			"truncated":            false,
			"affected_files_count": int64(affectedFiles),
		},
	}
}

func toolTraceErrorSummary(status observability.Status) string {
	if status == observability.StatusError {
		return "tool call failed"
	}
	return ""
}

func toolTraceResultSuccess(result any) bool {
	if r, ok := result.(*ToolCallResult); ok && r != nil {
		return r.Success
	}
	if r, ok := result.(ToolCallResult); ok {
		return r.Success
	}
	return true
}

func toolTraceJSONSize(value any) int {
	if value == nil {
		return 0
	}
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(data)
}

func toolDiffTraceEvent(req ToolCallRequest, result difftracker.DiffResult, elapsed time.Duration, err error) observability.TraceEvent {
	status := observability.StatusOK
	if err != nil {
		status = observability.StatusError
	}
	return observability.TraceEvent{
		Method:     "tool.diff.emit",
		SpanID:     nextToolTraceSpan("tool.diff.emit"),
		ThreadID:   req.ThreadID,
		AgentID:    req.AgentID,
		TurnID:     req.TurnID,
		CallID:     req.CallID,
		ToolName:   req.Name,
		DurationMS: elapsed.Milliseconds(),
		Status:     status,
		Error:      toolTraceErrorSummary(status),
		Code:       observability.CodeAnchor{File: "internal/platform/toolbridge/diff_gen.go", Function: "toolbridge.(*Handler).emitToolDiff", Line: 36},
		Metadata: map[string]any{
			"success":              err == nil,
			"result_bytes":         int64(len(result.DiffText)),
			"truncated":            false,
			"affected_files_count": int64(len(result.Files)),
		},
	}
}
