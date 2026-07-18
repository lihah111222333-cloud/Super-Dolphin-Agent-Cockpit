package toolbridge

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
)

func TestPublishProxyToolCallBeginSanitizesArgumentsPreview(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)

	h := &Handler{dispatcher: dispatcher}
	h.publishProxyToolCallBegin(ToolCallRequest{
		Name:      "shell",
		Arguments: json.RawMessage(`{"command":"curl --api-key sk-test https://example.test","token":"token=abc","file_path":"/Users/alice/secret"}`),
		AgentID:   "agent-1",
		ThreadID:  "thread-1",
		TurnID:    "turn-1",
		CallID:    "call-1",
	}, time.Now())

	begin := waitProxyToolBegin(t, beginCh)
	assertToolbridgeArgumentsPreviewSanitized(t, begin.ArgumentsPreview)
}

func assertToolbridgeArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"token=abc", "sk-test", "/Users/alice/secret", "--api-key"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("ArgumentsPreview = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("ArgumentsPreview = %q, want redaction marker", preview)
	}
}

func TestProxyToolResultPreviewSanitizesStructuredResult(t *testing.T) {
	result := &ToolCallResult{StructuredContent: json.RawMessage(`{
		"ok":true,
		"credential":"credential-result-leak",
		"certificate":"-----BEGIN CERTIFICATE-----\ncertificate-result-leak\n-----END CERTIFICATE-----",
		"envelope":"{\"session\":\"nested-session-leak\"}"
	}`)}

	preview := proxyToolResultPreview(result)
	for _, fragment := range []string{"credential-result-leak", "certificate-result-leak", "nested-session-leak", "BEGIN CERTIFICATE"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("proxyToolResultPreview() = %q, must not contain %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, `"ok":true`) || !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("proxyToolResultPreview() = %q, want safe context and redaction marker", preview)
	}
}

func TestPublishProxyToolCallBeginSanitizesOversizedStructuredArguments(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)

	const sensitiveValue = "request-value-7f89a3"
	h := &Handler{dispatcher: dispatcher}
	h.publishProxyToolCallBegin(ToolCallRequest{
		Name:      "oversized",
		Arguments: oversizedStructuredPreviewPayload(sensitiveValue),
		ThreadID:  "thread-oversized",
		CallID:    "call-oversized",
	}, time.Now())

	preview := waitProxyToolBegin(t, beginCh).ArgumentsPreview
	assertOversizedStructuredPreviewSanitized(t, preview, sensitiveValue)
}

func TestProxyToolResultPreviewSanitizesOversizedStructuredResult(t *testing.T) {
	const sensitiveValue = "result-value-1c42de"
	result := &ToolCallResult{
		StructuredContent: oversizedStructuredPreviewPayload(sensitiveValue),
	}

	preview := proxyToolResultPreview(result)
	assertOversizedStructuredPreviewSanitized(t, preview, sensitiveValue)
}

func oversizedStructuredPreviewPayload(sensitiveValue string) json.RawMessage {
	return json.RawMessage(`{"credentials":"` + sensitiveValue + `","padding":"` + strings.Repeat("x", 17*1024) + `"}`)
}

func assertOversizedStructuredPreviewSanitized(t *testing.T, preview, sensitiveValue string) {
	t.Helper()
	if strings.Contains(preview, sensitiveValue) {
		t.Fatalf("preview = %q, must not contain credentials value %q", preview, sensitiveValue)
	}
	if !strings.Contains(preview, "[REDACTED]") || !strings.Contains(preview, "[truncated]") {
		t.Fatalf("preview = %q, want redaction and truncation markers", preview)
	}
}

func TestCodexSurfaceValidationFailureSanitizesPreValidationLifecycleEvent(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	cancelBegin := event.Subscribe(dispatcher, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)

	owner := newTask4BAuthorityOwner()
	executor := &task4BSchemaExecutor{}
	client := &task4BMCPClient{tools: []mcpdto.MCPTool{task4BTool(
		"strict",
		`{"type":"object","additionalProperties":false,"properties":{"query":{"type":"string"}}}`,
	)}}
	h := task4BHandler(owner, executor, client)
	h.dispatcher = dispatcher
	tools, err := h.PrepareCodexToolSurface(context.Background(), task4BScope(task4BExternalBinary("external")))
	if err != nil {
		t.Fatalf("PrepareCodexToolSurface() error = %v", err)
	}
	if len(tools) != 1 {
		t.Fatalf("PrepareCodexToolSurface() tools = %d, want 1", len(tools))
	}

	_, err = h.callCodexSurfaceTool(context.Background(), h.lookupCodexToolSurface(ToolCallRequest{AgentID: "task4b-agent"}), ToolCallRequest{
		Name:      tools[0].Name,
		Arguments: json.RawMessage(`{"privateKey":"pre-validation-private-key-leak"}`),
		AgentID:   "task4b-agent",
		CallID:    "pre-validation-call",
	})
	if err == nil {
		t.Fatal("callCodexSurfaceTool() error = nil, want schema validation failure")
	}
	if client.callCount() != 0 {
		t.Fatalf("MCP client calls = %d, want 0 after validation failure", client.callCount())
	}

	begin := waitProxyToolBegin(t, beginCh)
	if strings.Contains(begin.ArgumentsPreview, "pre-validation-private-key-leak") || !strings.Contains(begin.ArgumentsPreview, "[REDACTED]") {
		t.Fatalf("pre-validation ArgumentsPreview = %q, want redacted value", begin.ArgumentsPreview)
	}
}
