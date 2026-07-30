package turn

import (
	"reflect"
	"testing"
	"time"

	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
)

func TestCanonicalTurnTerminalRoundTripPreservesAllFieldsAndCopies(t *testing.T) {
	t.Parallel()
	terminal := TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "event-full",
		ThreadID:      "thread-1",
		TurnID:        "turn-1",
		Outcome:       "failed",
		PublicError: &PublicErrorV1{
			Code:            "PROVIDER_FAILED",
			Title:           "Turn failed",
			Message:         "provider unavailable",
			DiagnosticID:    "diag-1",
			Retryable:       false,
			RecoveryActions: []string{"copy_diagnostics"},
		},
		PartialItemIDs: []string{"item-1", "item-2"},
		OccurredAt:     "2026-07-17T01:02:03.456Z",
	}
	want := cloneTerminalFixture(terminal)
	event := TurnCompleted{TurnHeader: terminalTestHeader(t, terminal)}

	attached, err := AttachCanonicalTurnTerminal(event, terminal)
	if err != nil {
		t.Fatalf("AttachCanonicalTurnTerminal() error = %v", err)
	}
	terminal.PublicError.Message = "mutated source"
	terminal.PublicError.RecoveryActions[0] = "source-mutation"
	terminal.PartialItemIDs[0] = "mutated-item"

	got, ok, err := CanonicalTurnTerminal(attached)
	if err != nil || !ok {
		t.Fatalf("CanonicalTurnTerminal() = (%#v, %v, %v), want canonical terminal", got, ok, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical roundtrip = %#v, want %#v", got, want)
	}
	got.PublicError.RecoveryActions[0] = "copy-mutation"
	got.PartialItemIDs[0] = "mutated-copy"
	gotAgain, ok, err := CanonicalTurnTerminal(attached)
	if err != nil || !ok || !reflect.DeepEqual(gotAgain, want) {
		t.Fatalf("canonical terminal alias leaked: got=%#v ok=%v err=%v want=%#v", gotAgain, ok, err, want)
	}
}

func TestAttachCanonicalTurnTerminalRejectsIdentityMismatch(t *testing.T) {
	t.Parallel()
	terminal := TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "event-identity",
		ThreadID:      "thread-1",
		TurnID:        "turn-1",
		Outcome:       "success",
		PublicSummary: "completed",
		OccurredAt:    "2026-07-17T01:02:03Z",
	}
	matching := terminalTestHeader(t, terminal)
	tests := []struct {
		name   string
		header shareddto.TurnHeader
	}{
		{name: "thread", header: turnHeaderWith(matching.Timestamp, "thread-2", matching.TurnID)},
		{name: "turn", header: turnHeaderWith(matching.Timestamp, matching.ThreadID, "turn-2")},
		{name: "occurred at", header: turnHeaderWith(matching.Timestamp.Add(time.Second), matching.ThreadID, matching.TurnID)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := AttachCanonicalTurnTerminal(TurnCompleted{TurnHeader: test.header}, terminal); err == nil {
				t.Fatal("AttachCanonicalTurnTerminal() accepted mismatched identity")
			}
		})
	}
}

func TestNewTurnTerminalV2PreservesAcceptedPartialItemIDs(t *testing.T) {
	t.Parallel()
	event := TurnCompleted{
		TurnHeader:     turnHeaderWith(time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC), "thread-1", "turn-1"),
		Success:        true,
		Status:         "completed",
		Summary:        "public success summary",
		PartialItemIDs: []string{"item-accepted-1"},
	}
	terminal, err := NewTurnTerminalV2(event, "event-local")
	if err != nil {
		t.Fatalf("NewTurnTerminalV2() error = %v", err)
	}
	event.PartialItemIDs[0] = "mutated"
	if !reflect.DeepEqual(terminal.PartialItemIDs, []string{"item-accepted-1"}) {
		t.Fatalf("PartialItemIDs = %#v, want accepted item id", terminal.PartialItemIDs)
	}
}

func TestNewTurnTerminalV2AdvertisesOnlyImplementedRecoveryActions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		status      string
		reason      string
		wantTitle   string
		wantMessage string
	}{
		{name: "provider failure", status: "failed", wantTitle: "本次执行失败", wantMessage: "Provider 未能完成本次执行。"},
		{name: "system termination", status: "interrupted", reason: "provider", wantTitle: "本次执行已结束", wantMessage: "Provider 或系统已结束本次执行。"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			eventID := "terminal:thread-1:turn-1:2026-07-17T01:02:03Z"
			terminal, err := NewTurnTerminalV2(TurnCompleted{
				TurnHeader: turnHeaderWith(time.Date(2026, 7, 17, 1, 2, 3, 0, time.UTC), "thread-1", "turn-1"),
				Status:     test.status,
				Reason:     test.reason,
			}, eventID)
			if err != nil {
				t.Fatalf("NewTurnTerminalV2() error = %v", err)
			}
			if terminal.PublicError == nil {
				t.Fatal("NewTurnTerminalV2() omitted public error")
			}
			if terminal.PublicError.Retryable {
				t.Fatal("terminal public error advertised unavailable retry capability")
			}
			if terminal.PublicError.Title != test.wantTitle || terminal.PublicError.Message != test.wantMessage {
				t.Fatalf("PublicError = (%q, %q), want (%q, %q)", terminal.PublicError.Title, terminal.PublicError.Message, test.wantTitle, test.wantMessage)
			}
			if !reflect.DeepEqual(terminal.PublicError.RecoveryActions, []string{"copy_diagnostics"}) {
				t.Fatalf("RecoveryActions = %#v, want only copy_diagnostics", terminal.PublicError.RecoveryActions)
			}
			if terminal.PublicError.DiagnosticID != diagnosticIDForEventID(eventID) || terminal.PublicError.DiagnosticID == terminal.EventID {
				t.Fatalf("DiagnosticID = %q, EventID = %q, want independent canonical diagnostic ID", terminal.PublicError.DiagnosticID, terminal.EventID)
			}
		})
	}
}

func terminalTestHeader(t *testing.T, terminal TurnTerminalV2) shareddto.TurnHeader {
	t.Helper()
	timestamp, err := time.Parse(time.RFC3339Nano, terminal.OccurredAt)
	if err != nil {
		t.Fatalf("parse terminal occurredAt: %v", err)
	}
	return turnHeaderWith(timestamp, terminal.ThreadID, terminal.TurnID)
}

func turnHeaderWith(timestamp time.Time, threadID, turnID string) shareddto.TurnHeader {
	return shareddto.TurnHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{
				EventHeader: shareddto.EventHeader{Timestamp: timestamp},
				ThreadID:    threadID,
			},
		},
		TurnIDHeader: shareddto.TurnIDHeader{TurnID: turnID},
	}
}

func cloneTerminalFixture(terminal TurnTerminalV2) TurnTerminalV2 {
	clone := terminal
	if terminal.PublicError != nil {
		publicError := *terminal.PublicError
		publicError.RecoveryActions = append([]string(nil), terminal.PublicError.RecoveryActions...)
		clone.PublicError = &publicError
	}
	clone.PartialItemIDs = append([]string(nil), terminal.PartialItemIDs...)
	return clone
}
