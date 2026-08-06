package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestTranslateToolCallBeginSanitizesArgumentsPreview(t *testing.T) {
	ev, ok := translateToolEvent("tool.call.begin", map[string]any{
		"threadId":  "thread-1",
		"agentId":   "agent-1",
		"turnId":    "turn-1",
		"callId":    "call-1",
		"toolName":  "shell",
		"arguments": sensitiveCodexArguments(),
	})
	if !ok {
		t.Fatal("translateToolEvent() ok=false, want ToolCallBegin")
	}
	begin, ok := ev.(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", ev)
	}
	assertCodexArgumentsPreviewSanitized(t, begin.ArgumentsPreview)
}

func TestTranslateToolCallBeginFailsClosedForOversizedEncodedArguments(t *testing.T) {
	const sensitiveValue = "codex-oversized-secret-3a71b9"
	raw := `{"password":"` + sensitiveValue + `","padding":"` + strings.Repeat("x", 17*1024)

	ev, ok := translateToolEvent("tool.call.begin", map[string]any{
		"callId":    "call-oversized",
		"toolName":  "shell",
		"arguments": raw,
	})
	if !ok {
		t.Fatal("translateToolEvent() ok=false, want ToolCallBegin")
	}
	begin, ok := ev.(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", ev)
	}
	if strings.Contains(begin.ArgumentsPreview, sensitiveValue) {
		t.Fatalf("ToolCallBegin.ArgumentsPreview = %q, must not expose %q to event consumers", begin.ArgumentsPreview, sensitiveValue)
	}
	if !strings.Contains(begin.ArgumentsPreview, "[REDACTED]") || !strings.Contains(begin.ArgumentsPreview, "[truncated]") {
		t.Fatalf("ToolCallBegin.ArgumentsPreview = %q, want fail-closed markers", begin.ArgumentsPreview)
	}
}

func TestPublishToolCallBeginSanitizesArgumentsPreview(t *testing.T) {
	bus := event.NewDispatcher()
	t.Cleanup(func() { _ = bus.Close() })
	dispatcher := unified.NewEventDispatcher(bus, nil)
	beginCh := make(chan tooldto.ToolCallBegin, 1)
	cancelBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { beginCh <- ev })
	t.Cleanup(cancelBegin)

	s := newInboundTestSession(t, context.Background(), nil, &ServerManager{})
	s.dispatcher = dispatcher
	msg := rawParams(t, map[string]any{
		"name":      "shell",
		"arguments": sensitiveCodexArguments(),
	})
	s.publishToolCallBegin(preparedToolCall{
		header:  toolCallHeader("agent-1", "turn-1", "call-1", "shell", time.Now()),
		params:  msg,
		started: time.Now(),
	})

	begin := waitToolCallBegin(t, beginCh)
	assertCodexArgumentsPreviewSanitized(t, begin.ArgumentsPreview)
}

func TestTranslateCodexRolloutFunctionCallSanitizesArgumentsPreview(t *testing.T) {
	rawArguments, err := json.Marshal(sensitiveCodexArguments())
	if err != nil {
		t.Fatalf("marshal sensitive arguments: %v", err)
	}
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "response_item",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"type":      "function_call",
			"name":      "shell",
			"arguments": string(rawArguments),
			"call_id":   "call-1",
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))
	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	begin, ok := got[0].(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", got[0])
	}
	assertCodexArgumentsPreviewSanitized(t, begin.ArgumentsPreview)
}

func sensitiveCodexArguments() map[string]any {
	return map[string]any{
		"command": `run password="codex-password-value" TOKEN='codex-token-value' --password=\"codex-escaped-value\" keep=codex-visible`,
	}
}

func assertCodexArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"codex-password-value", "codex-token-value", "codex-escaped-value"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("ArgumentsPreview = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") || !strings.Contains(preview, "keep=codex-visible") {
		t.Fatalf("ArgumentsPreview = %q, want redaction marker and ordinary context", preview)
	}
}
