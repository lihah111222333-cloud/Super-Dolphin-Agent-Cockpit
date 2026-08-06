package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
)

type cwdOptionalHostToolRegistry struct {
	result any
	calls  int
	last   HostToolCall
}

func (r *cwdOptionalHostToolRegistry) ListHostTools() []mcpdto.MCPTool {
	return nil
}

func (r *cwdOptionalHostToolRegistry) HasTool(name string) bool {
	return name == "observability_trace_get"
}

func (r *cwdOptionalHostToolRegistry) RequiresCWD(name string) bool {
	return name != "observability_trace_get"
}

func (r *cwdOptionalHostToolRegistry) CallHostTool(_ context.Context, call HostToolCall) (any, error) {
	r.calls++
	r.last = call
	return r.result, nil
}

func TestCallHostToolCWDOptionalSkipsResolverAndMirrorsStructuredContent(t *testing.T) {
	host := &cwdOptionalHostToolRegistry{result: map[string]any{"trace_id": "trace-1", "source": "memory"}}
	resolver := &stubCWDResolver{err: errors.New("resolver should not be called")}
	h := &Handler{resolver: resolver, hostTools: host, skillMetrics: skillmetrics.NewRegistry()}

	got, err := h.callHostTool(context.Background(), ToolCallRequest{Name: "observability_trace_get", Arguments: json.RawMessage(`{"trace_id":"trace-1"}`)})
	if err != nil {
		t.Fatalf("callHostTool() error = %v", err)
	}

	assertCWDOptionalHostCall(t, got, host, resolver)
	assertHostToolStructuredContent(t, got, "trace-1")
}

func TestHostToolErrorMirrorsValidStructuredContent(t *testing.T) {
	err := contractErrorForDispatchTest()
	got := hostToolErrorResult(ToolCallRequest{Name: "observability_trace_get"}, err)

	textEnvelope := decodeToolResultEnvelope(t, got)
	assertHostToolErrorLegacyEnvelope(t, textEnvelope)
	assertHostToolErrorMeta(t, textEnvelope)
	assertHostToolErrorStructuredContent(t, got, textEnvelope)
}

func TestHostToolErrorDoesNotExposeRawCause(t *testing.T) {
	const marker = "token=sk-test-secret dsn=postgres://alice:secret@db/private path=/Users/private/secret.go"
	got := hostToolErrorResult(ToolCallRequest{Name: "observability_trace_get"}, errors.New(marker))
	if got == nil || got.Success || len(got.ContentItems) != 1 {
		t.Fatalf("hostToolErrorResult() = %#v, want failed tool result", got)
	}
	if strings.Contains(got.ContentItems[0].Text, marker) || strings.Contains(string(got.StructuredContent), marker) {
		t.Fatalf("hostToolErrorResult() leaked marker: %#v", got)
	}
	envelope := decodeToolResultEnvelope(t, got)
	message, ok := envelope["error"].(string)
	if !ok || !strings.HasPrefix(message, "Tool execution failed. Diagnostic ID: ") || envelope["code"] == "" || envelope["hint"] == "" {
		t.Fatalf("host error envelope = %#v, want stable code/hint and public diagnostic error", envelope)
	}
}

func assertHostToolErrorLegacyEnvelope(t *testing.T, textEnvelope map[string]any) {
	t.Helper()
	if textEnvelope["kind"] != "host_tool_error" {
		t.Fatalf("inputText envelope = %#v, want legacy host_tool_error", textEnvelope)
	}
	if textEnvelope["error"] == "" {
		t.Fatalf("inputText envelope = %#v, want non-empty error", textEnvelope)
	}
	if textEnvelope["success"] != false {
		t.Fatalf("inputText envelope = %#v, want success=false", textEnvelope)
	}
	if textEnvelope["code"] == "" || textEnvelope["hint"] == "" {
		t.Fatalf("inputText envelope = %#v, want stable code and hint", textEnvelope)
	}
}

func assertHostToolErrorMeta(t *testing.T, textEnvelope map[string]any) {
	t.Helper()
	meta, ok := textEnvelope["meta"].(map[string]any)
	if !ok || meta["tool"] != "observability_trace_get" || meta["kind"] != "host_tool_error" {
		t.Fatalf("inputText meta = %#v, want tool/kind metadata", textEnvelope["meta"])
	}
}

func assertHostToolErrorStructuredContent(t *testing.T, got *ToolCallResult, textEnvelope map[string]any) {
	t.Helper()
	var structured map[string]any
	if err := json.Unmarshal(got.StructuredContent, &structured); err != nil {
		t.Fatalf("StructuredContent = %s, want valid object: %v", got.StructuredContent, err)
	}
	if structured["kind"] != "host_tool_error" || structured["error"] != textEnvelope["error"] {
		t.Fatalf("StructuredContent = %#v, want mirrored error envelope %#v", structured, textEnvelope)
	}
	if structured["success"] != false {
		t.Fatalf("StructuredContent = %#v, want success=false", structured)
	}
}

func contractErrorForDispatchTest() error {
	return errors.New("trace diagnosis unavailable")
}

func assertCWDOptionalHostCall(t *testing.T, got *ToolCallResult, host *cwdOptionalHostToolRegistry, resolver *stubCWDResolver) {
	t.Helper()
	if got == nil || !got.Success {
		t.Fatalf("callHostTool() result = %#v, want success", got)
	}
	if host.calls != 1 || host.last.CWD != "" {
		t.Fatalf("host calls=%d cwd=%q, want one call with empty cwd", host.calls, host.last.CWD)
	}
	if resolver.callSeen {
		t.Fatal("resolver was called for cwd-optional host tool")
	}
}

func assertHostToolStructuredContent(t *testing.T, got *ToolCallResult, wantTraceID string) {
	t.Helper()
	textEnvelope := decodeToolResultEnvelope(t, got)
	if textEnvelope["trace_id"] != wantTraceID {
		t.Fatalf("inputText envelope = %#v, want trace_id %q", textEnvelope, wantTraceID)
	}
	if len(got.StructuredContent) == 0 {
		t.Fatal("StructuredContent is empty, want mirrored host payload")
	}
	var structured map[string]any
	if err := json.Unmarshal(got.StructuredContent, &structured); err != nil {
		t.Fatalf("StructuredContent = %s, want object: %v", got.StructuredContent, err)
	}
	if structured["trace_id"] != wantTraceID {
		t.Fatalf("StructuredContent = %#v, want trace_id %q", structured, wantTraceID)
	}
}
