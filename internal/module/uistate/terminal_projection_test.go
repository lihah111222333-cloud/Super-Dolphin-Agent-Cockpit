package uistate

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

const terminalProjectionRawSecret = "Authorization: Bearer terminal-projection-secret"

func TestCanonicalTerminalIsTheOnlyFailureTextInUIStatePatchAndSnapshot(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := newProjectionTestService(t)
	svc.bindDispatcher(dispatcher)
	cancelSubscriptions := registerProjectionSubscriptions(dispatcher, svc)
	t.Cleanup(func() { cancelAll(cancelSubscriptions) })
	patches := make(chan uidto.UIThreadPatch, 4)
	cancelPatches := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { patches <- ev })
	t.Cleanup(cancelPatches)

	completed := canonicalTestTurnCompleted(t)
	event.Publish(dispatcher, turndto.TurnStarted{TurnHeader: completed.TurnHeader})
	event.Publish(dispatcher, completed)

	patch := waitForCanonicalCompletionPatch(t, patches)
	assertNoTerminalProjectionRawSecret(t, patch)
	snapshot, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	assertTerminalProjectionHasNoRawSecret(t, snapshot)
	if len(snapshot.RecentTurns) != 1 || snapshot.RecentTurns[0].Error != "The provider could not complete this turn." {
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

func waitForCanonicalCompletionPatch(t *testing.T, patches <-chan uidto.UIThreadPatch) uidto.UIThreadPatch {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case patch := <-patches:
			if patch.Source == "turn/completed" {
				return patch
			}
		case <-deadline:
			t.Fatal("timed out waiting for canonical completion snapshot patch")
		}
	}
}

func assertTerminalProjectionHasNoRawSecret(t *testing.T, value any) {
	t.Helper()
	assertNoTerminalProjectionRawSecret(t, value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal terminal projection: %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, "The provider could not complete this turn.") {
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
