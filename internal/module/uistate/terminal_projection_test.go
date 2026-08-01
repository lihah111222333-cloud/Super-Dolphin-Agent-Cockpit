package uistate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

const terminalProjectionRawSecret = "Authorization: Bearer terminal-projection-secret"

func TestCanonicalTerminalIsTheOnlyFailureTextInUIStatePatchAndSnapshot(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	patches := make([]uidto.UIThreadPatch, 0, 2)
	svc.emitThreadPatch = func(patch uidto.UIThreadPatch) { patches = append(patches, patch) }

	completed := canonicalTestTurnCompleted(t)
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: completed.TurnHeader})
	svc.applyTurnCompleted(completed)

	patch := canonicalCompletionPatch(t, patches)
	assertNoTerminalProjectionRawSecret(t, patch)
	snapshot, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	assertTerminalProjectionHasNoRawSecret(t, snapshot)
	if len(snapshot.RecentTurns) != 1 || snapshot.RecentTurns[0].Error != "Provider 未能完成本次执行。" {
		t.Fatalf("RecentTurns = %#v, want canonical public error", snapshot.RecentTurns)
	}
}

func canonicalTestTurnCompleted(t *testing.T) turndto.TurnCompleted {
	t.Helper()
	ev := turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{EventHeader: shareddto.EventHeader{Timestamp: time.Now().UTC()}, ThreadID: "thread-terminal-projection"},
				AgentID:      "agent-terminal-projection",
			},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-terminal-projection"},
		},
		Success: false,
		Status:  "failed",
		Error:   terminalProjectionRawSecret,
	}
	terminal, err := turndto.NewTurnTerminalV2(ev, "terminal-projection-event")
	if err != nil {
		t.Fatalf("NewTurnTerminalV2() error = %v", err)
	}
	attached, err := turndto.AttachCanonicalTurnTerminal(ev, terminal)
	if err != nil {
		t.Fatalf("AttachCanonicalTurnTerminal() error = %v", err)
	}
	return attached
}

func canonicalCompletionPatch(t *testing.T, patches []uidto.UIThreadPatch) uidto.UIThreadPatch {
	t.Helper()
	for _, patch := range patches {
		if patch.Source == "turn/completed" {
			return patch
		}
	}
	t.Fatal("canonical completion snapshot patch was not emitted")
	return uidto.UIThreadPatch{}
}

func assertTerminalProjectionHasNoRawSecret(t *testing.T, value any) {
	t.Helper()
	assertNoTerminalProjectionRawSecret(t, value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal terminal projection: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, "Provider 未能完成本次执行。") {
		t.Fatalf("terminal projection omitted canonical public error: %s", text)
	}
}

func assertNoTerminalProjectionRawSecret(t *testing.T, value any) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal terminal projection: %v", err)
	}
	text := string(encoded)
	if strings.Contains(text, terminalProjectionRawSecret) {
		t.Fatalf("terminal projection leaked raw provider secret: %s", text)
	}
}
