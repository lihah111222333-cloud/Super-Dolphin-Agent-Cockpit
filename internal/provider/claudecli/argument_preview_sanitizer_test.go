package claudecli

import (
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
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

func sensitiveClaudeArgumentsPreview() string {
	return `{"command":"curl --api-key sk-test https://example.test","token":"token=abc","file_path":"/Users/mima0000/secret"}`
}

func assertClaudeArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"token=abc", "sk-test", "/Users/mima0000/secret", "--api-key"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("ArgumentsPreview = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("ArgumentsPreview = %q, want redaction marker", preview)
	}
}
