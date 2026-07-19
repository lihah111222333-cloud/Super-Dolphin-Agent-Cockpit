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
	return `run password="timeline-password-value" TOKEN='timeline-token-value' --password=\"timeline-escaped-value\" keep=timeline-visible`
}

func assertTimelineArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"timeline-password-value", "timeline-token-value", "timeline-escaped-value"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("timeline Preview = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") || !strings.Contains(preview, "keep=timeline-visible") {
		t.Fatalf("timeline Preview = %q, want redaction marker and ordinary context", preview)
	}
}
