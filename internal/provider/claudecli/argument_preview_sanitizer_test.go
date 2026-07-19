package claudecli

import (
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
)

func TestToolCallBeginSanitizesArgumentsPreview(t *testing.T) {
	ev, ok := translateToolEvent(dto.RawProviderEvent{
		EventType: "tool:use_begin",
		Data: map[string]any{
			"thread_id":         "thread-1",
			"agent_id":          "agent-1",
			"turn_id":           "turn-1",
			"call_id":           "call-1",
			"tool_name":         "shell",
			"arguments_preview": sensitiveClaudeArgumentsPreview(),
		},
	})
	if !ok {
		t.Fatal("translateToolEvent() ok=false, want ToolCallBegin")
	}
	begin, ok := ev.(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", ev)
	}
	assertClaudeArgumentsPreviewSanitized(t, begin.ArgumentsPreview)
}

func TestToolCallBeginFailsClosedForOversizedPrefixedArguments(t *testing.T) {
	const sensitiveValue = "claude-oversized-secret-4e82c1"
	raw := "arguments: " + `{"password":"` + sensitiveValue + `","padding":"` + strings.Repeat("x", 17*1024)

	ev, ok := translateToolEvent(dto.RawProviderEvent{
		EventType: "tool:use_begin",
		Data: map[string]any{
			"call_id":           "call-oversized",
			"tool_name":         "shell",
			"arguments_preview": raw,
		},
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

func sensitiveClaudeArgumentsPreview() string {
	return `run password="claude-password-value" TOKEN='claude-token-value' --password=\"claude-escaped-value\" keep=claude-visible`
}

func assertClaudeArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"claude-password-value", "claude-token-value", "claude-escaped-value"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("ArgumentsPreview = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") || !strings.Contains(preview, "keep=claude-visible") {
		t.Fatalf("ArgumentsPreview = %q, want redaction marker and ordinary context", preview)
	}
}
