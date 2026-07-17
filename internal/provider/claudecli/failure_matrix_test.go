package claudecli

import (
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestFailureMatrixClaudeTerminalCases(t *testing.T) {
	t.Parallel()

	t.Run("FM-08", func(t *testing.T) {
		ev, ok := translateTurnEvent(dto.RawProviderEvent{EventType: "turn:interrupted", Data: map[string]any{
			"thread_id": "thread-1",
			"agent_id":  "agent-1",
			"turn_id":   "turn-1",
			"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
			"reason":    "provider interrupted before completed",
		}})
		if !ok {
			t.Fatal("translateTurnEvent() ok = false")
		}
		terminal, ok := ev.(turndto.TurnCompleted)
		if !ok {
			t.Fatalf("translated event type = %T, want TurnCompleted", ev)
		}
		if terminal.Success || terminal.Status != "interrupted" {
			t.Fatalf("terminal = %#v, want interrupted terminal without completed event", terminal)
		}
	})
}
