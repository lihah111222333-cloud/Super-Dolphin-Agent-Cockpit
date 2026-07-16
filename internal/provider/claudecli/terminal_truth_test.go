package claudecli

import (
	"testing"
	"time"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestClaudeInterruptedIsTerminalWithoutCompletedEvent(t *testing.T) {
	t.Parallel()

	ev, ok := translateTurnEvent(dto.RawProviderEvent{EventType: "turn:interrupted", Data: map[string]any{
		"thread_id": "thread-1",
		"agent_id":  "agent-1",
		"turn_id":   "turn-1",
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"reason":    "provider interrupted",
	}})
	if !ok {
		t.Fatal("translateTurnEvent() ok = false")
	}
	terminal, ok := ev.(turndto.TurnCompleted)
	if !ok {
		t.Fatalf("translated event type = %T, want canonical TurnCompleted terminal", ev)
	}
	if terminal.Success || terminal.Status != "interrupted" {
		t.Fatalf("terminal = %#v, want interrupted non-success", terminal)
	}
}
