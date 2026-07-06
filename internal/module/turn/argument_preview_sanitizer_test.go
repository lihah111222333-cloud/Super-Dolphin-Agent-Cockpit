package turn

import (
	"strings"
	"testing"
	"time"

	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
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
	return `{"command":"curl --api-key sk-test https://example.test","token":"token=abc","file_path":"/Users/mima0000/secret"}`
}

func assertTrajectoryArgumentsPreviewSanitized(t *testing.T, preview string) {
	t.Helper()
	for _, fragment := range []string{"token=abc", "sk-test", "/Users/mima0000/secret", "--api-key"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("trajectory args = %q, must not contain sensitive fragment %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("trajectory args = %q, want redaction marker", preview)
	}
}
