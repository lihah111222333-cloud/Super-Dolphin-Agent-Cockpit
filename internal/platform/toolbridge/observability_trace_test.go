package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/mcpcontrol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

func TestHandleToolCallRecordsSummaryOnlyTraceEvents(t *testing.T) {
	tracer := newToolbridgeTraceService(t)
	secretOutput := "FULL_TOOL_RESULT_SHOULD_NOT_APPEAR"
	secretFile := "FILE_CONTENT_SHOULD_NOT_APPEAR"
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(_ context.Context, _ string, _ any, result any) error {
		resp := result.(*peerToolCallResponse)
		resp.Content = []peerToolCallContent{{Type: "text", Text: secretOutput}}
		resp.StructuredContent = json.RawMessage(`{"path":"secret.txt","content":"` + secretFile + `"}`)
		return nil
	}}}
	h, _ := newHandlerForTest(peer)
	h.tracer = tracer

	ctx := observability.ContextWithSpan(context.Background(), "trace-tool", "turn-span", "root-span")
	result, err := h.HandleToolCall(ctx, contract.ToolCallRawMessage{
		ID:     json.RawMessage(`"call-1"`),
		Method: "item/tool/call",
		Params: mustRawJSON(t, map[string]any{
			"name":       "file",
			"arguments":  map[string]any{"file_path": "secret.txt"},
			"agentId":    "agent-1",
			"threadId":   "thread-1",
			"turnId":     "turn-1",
			"callId":     "call-1",
			"clientKind": dto.ClientKindLSP,
		}),
	})
	requireObservedToolCallResult(t, result, err)
	assertSummaryOnlyToolTrace(t, tracer, secretOutput, secretFile)
}

func TestRecordToolTraceLogsRecordErrors(t *testing.T) {
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "10", "OBS_INDEX_MAX_TRACE_EVENTS": "10", "OBS_INDEX_MAX_THREAD_EVENTS": "10"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	var logs bytes.Buffer
	h := &Handler{
		logger: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		tracer: observability.NewService(cfg, observability.WithSink(failingToolTraceSink{})),
	}

	h.recordToolTrace(context.Background(), observability.TraceEvent{Method: "tool.call.end", Status: observability.StatusOK})

	got := logs.String()
	if !strings.Contains(got, "observability: trace record failed") || !strings.Contains(got, "toolbridge") || !strings.Contains(got, "tool.call.end") || !strings.Contains(got, "tool trace sink unavailable") {
		t.Fatalf("logs = %q, want visible trace record failure warning", got)
	}
}

type failingToolTraceSink struct{}

func (failingToolTraceSink) Append(context.Context, observability.TraceEvent) error {
	return assertErr("tool trace sink unavailable")
}

func TestHandleToolCallErrorTraceIncludesCompactStack(t *testing.T) {
	tracer := newToolbridgeTraceService(t)
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		return assertErr("peer failed with token=sk-privatepayload123456")
	}}}
	h, _ := newHandlerForTest(peer)
	h.tracer = tracer

	ctx := observability.ContextWithSpan(context.Background(), "trace-tool-error", "turn-span", "root-span")
	result, err := h.HandleToolCall(ctx, contract.ToolCallRawMessage{
		ID:     json.RawMessage(`"call-error"`),
		Method: "item/tool/call",
		Params: mustRawJSON(t, map[string]any{
			"name":       "file",
			"arguments":  map[string]any{},
			"agentId":    "agent-1",
			"threadId":   "thread-1",
			"turnId":     "turn-1",
			"callId":     "call-error",
			"clientKind": dto.ClientKindLSP,
		}),
	})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	got := result.(*ToolCallResult)
	if got.Success {
		t.Fatal("HandleToolCall() Success = true, want false")
	}

	end := requireTraceMethod(t, tracer.Query(context.Background(), observability.Query{TraceID: "trace-tool-error"}).Events, "tool.call.end")
	if end.Status != observability.StatusError {
		t.Fatalf("end status = %s, want error", end.Status)
	}
	if len(end.Stack) == 0 {
		t.Fatalf("end stack is empty: %+v", end)
	}
	if strings.Contains(mustTraceJSON(t, []observability.TraceEvent{end}), "sk-privatepayload") {
		t.Fatalf("trace JSON leaked raw peer error: %+v", end)
	}
}

func TestHandleToolCallErrorTraceUsesSafeErrorPreviewField(t *testing.T) {
	tracer := newToolbridgeTraceService(t)
	peer := &mcpcontrol.ToolInstance{Peer: &stubPeer{callbackFn: func(context.Context, string, any, any) error {
		return assertErr("peer denied request token=sk-abcdefghijklmnopqrstuvwxyz")
	}}}
	h, _ := newHandlerForTest(peer)
	h.tracer = tracer

	ctx := observability.ContextWithSpan(context.Background(), "trace-tool-error-preview", "turn-span", "root-span")
	result, err := h.HandleToolCall(ctx, contract.ToolCallRawMessage{
		ID:     json.RawMessage(`"call-error-preview"`),
		Method: "item/tool/call",
		Params: mustRawJSON(t, map[string]any{
			"name":       "file",
			"arguments":  map[string]any{},
			"agentId":    "agent-1",
			"threadId":   "thread-1",
			"turnId":     "turn-1",
			"callId":     "call-error-preview",
			"clientKind": dto.ClientKindLSP,
		}),
	})
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	if got := result.(*ToolCallResult); got.Success {
		t.Fatal("HandleToolCall() Success = true, want false")
	}

	end := requireTraceMethod(t, tracer.Query(context.Background(), observability.Query{TraceID: "trace-tool-error-preview"}).Events, "tool.call.end")
	preview, _ := end.Metadata[observability.ErrorPreviewField].(string)
	if !strings.Contains(preview, "peer denied request") || strings.Contains(preview, "sk-") {
		t.Fatalf("error_preview = %q, want sanitized peer error detail", preview)
	}
	if end.Metadata[observability.ErrorCodeField] != "tool_call_failed" {
		t.Fatalf("error_code = %#v, want tool_call_failed", end.Metadata[observability.ErrorCodeField])
	}
}

func requireObservedToolCallResult(t *testing.T, result any, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("HandleToolCall() error = %v", err)
	}
	if result == nil {
		t.Fatal("HandleToolCall() result = nil")
	}
}

func assertSummaryOnlyToolTrace(t *testing.T, tracer *observability.Service, secretOutput string, secretFile string) {
	t.Helper()
	events := tracer.Query(context.Background(), observability.Query{TraceID: "trace-tool"}).Events
	begin := requireTraceMethod(t, events, "tool.call.begin")
	end := requireTraceMethod(t, events, "tool.call.end")
	assertToolTraceScope(t, begin)
	assertToolTraceSpan(t, begin, end)
	assertToolTraceSummary(t, end)
	assertTraceDoesNotLeak(t, events, secretOutput, secretFile, "file_path")
}

func assertToolTraceScope(t *testing.T, begin observability.TraceEvent) {
	t.Helper()
	if begin.ToolName != "file" || begin.CallID != "call-1" || begin.TurnID != "turn-1" {
		t.Fatalf("begin event scope = %+v, want tool/call/turn", begin)
	}
}

func assertToolTraceSpan(t *testing.T, begin observability.TraceEvent, end observability.TraceEvent) {
	t.Helper()
	if begin.TraceID != "trace-tool" || begin.SpanID == "" || begin.ParentSpanID != "turn-span" {
		t.Fatalf("begin trace = (%q,%q,%q), want child of turn-span", begin.TraceID, begin.SpanID, begin.ParentSpanID)
	}
	if end.TraceID != begin.TraceID || end.SpanID != begin.SpanID || end.ParentSpanID != begin.ParentSpanID {
		t.Fatalf("end trace = (%q,%q,%q), want same tool span as begin (%q,%q,%q)", end.TraceID, end.SpanID, end.ParentSpanID, begin.TraceID, begin.SpanID, begin.ParentSpanID)
	}
}

func assertToolTraceSummary(t *testing.T, end observability.TraceEvent) {
	t.Helper()
	if end.Status != observability.StatusOK || end.DurationMS < 0 {
		t.Fatalf("end status/duration = %s/%d", end.Status, end.DurationMS)
	}
	if end.Metadata["success"] != true || end.Metadata["result_bytes"] == nil || end.Metadata["truncated"] == nil || end.Metadata["affected_files_count"] == nil {
		t.Fatalf("end metadata = %#v, want summary fields", end.Metadata)
	}
}

func assertTraceDoesNotLeak(t *testing.T, events []observability.TraceEvent, forbidden ...string) {
	t.Helper()
	encoded := mustTraceJSON(t, events)
	for _, value := range forbidden {
		if strings.Contains(encoded, value) {
			t.Fatalf("trace JSON leaked %q: %s", value, encoded)
		}
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

func newToolbridgeTraceService(t *testing.T) *observability.Service {
	t.Helper()
	cfg, err := observability.ParseConfig(observability.EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_INDEX_MAX_EVENTS": "20", "OBS_INDEX_MAX_TRACE_EVENTS": "20", "OBS_INDEX_MAX_THREAD_EVENTS": "20"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return observability.NewService(cfg)
}

func requireTraceMethod(t *testing.T, events []observability.TraceEvent, method string) observability.TraceEvent {
	t.Helper()
	for _, event := range events {
		if event.Method == method {
			return event
		}
	}
	t.Fatalf("missing trace method %s in %#v", method, events)
	return observability.TraceEvent{}
}

func mustTraceJSON(t *testing.T, events []observability.TraceEvent) string {
	t.Helper()
	data, err := json.Marshal(events)
	if err != nil {
		t.Fatalf("Marshal trace events: %v", err)
	}
	return string(data)
}
