package toolbridge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
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
