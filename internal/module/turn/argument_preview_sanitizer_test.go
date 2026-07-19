package turn

import (
	"strings"
	"testing"
	"time"

	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestTrajectoryCollectorSanitizesToolCallArgumentsPreview(t *testing.T) {
	c, _ := newTrajectoryFixture()
	now := time.Now()
	c.onTurnStarted(turndto.TurnStarted{TurnHeader: makeTurnHeader("turn-1", "thread-1", "agent-1", now)})
	c.onToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader:   makeToolHeader("turn-1", "thread-1", "agent-1", "call-1", "shell", now),
		ArgumentsPreview: sensitiveTrajectoryArgumentsPreview(),
	})

	snap, ok := c.Snapshot("turn-1")
	if !ok || len(snap.ToolCalls) != 1 {
		t.Fatalf("Snapshot ok=%v toolCalls=%d, want one tool call", ok, len(snap.ToolCalls))
	}
	assertTrajectoryArgumentsPreviewSanitized(t, snap.ToolCalls[0].Args)
}

func sensitiveTrajectoryArgumentsPreview() string {
	return `run password="trajectory-password-value" TOKEN='trajectory-token-value' --password=\"trajectory-escaped-value\" keep=trajectory-visible`
}

func assertTrajectoryArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"trajectory-password-value", "trajectory-token-value", "trajectory-escaped-value"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("trajectory args = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") || !strings.Contains(preview, "keep=trajectory-visible") {
		t.Fatalf("trajectory args = %q, want redaction marker and ordinary context", preview)
	}
}
