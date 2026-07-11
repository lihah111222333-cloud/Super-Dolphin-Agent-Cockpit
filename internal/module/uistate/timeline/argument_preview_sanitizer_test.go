package timeline_test

import (
	"strings"
	"testing"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
)

func TestToolCallBeginSanitizesRunningPreview(t *testing.T) {
	t.Parallel()
	requirePhase2TimelineShape(t)

	svc, dispatcher, cleanup := newPhase2TimelineHarness(t)
	defer cleanup()

	event.Publish(dispatcher, tooldto.ToolCallBegin{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: phase2TurnHeader("thread-1", "agent-1", "turn-1"),
			CallID:     "call-tool-sensitive",
			ToolName:   "shell",
		},
		ArgumentsPreview: sensitiveTimelineArgumentsPreview(),
	})

	waitForCondition(t, func() bool { return len(svc.GetByThread("thread-1")) == 1 }, "expected tool timeline item")
	items := svc.GetByThread("thread-1")
	assertTimelineArgumentsPreviewSanitized(t, items[0].Preview)
}

func sensitiveTimelineArgumentsPreview() string {
	return `{"command":"curl --api-key sk-test https://example.test","token":"token=abc","file_path":"/Users/alice/secret"}`
}

func assertTimelineArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"token=abc", "sk-test", "/Users/alice/secret", "--api-key"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("timeline Preview = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("timeline Preview = %q, want redaction marker", preview)
	}
}
