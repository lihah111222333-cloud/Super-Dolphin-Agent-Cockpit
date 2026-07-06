package toolbridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/kelindar/event"
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
		Arguments: json.RawMessage(`{"command":"curl --api-key sk-test https://example.test","token":"token=abc","file_path":"/Users/mima0000/secret"}`),
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
	for _, fragment := range []string{"token=abc", "sk-test", "/Users/mima0000/secret", "--api-key"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("ArgumentsPreview = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("ArgumentsPreview = %q, want redaction marker", preview)
	}
}
