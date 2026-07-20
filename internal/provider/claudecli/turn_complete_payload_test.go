package claudecli

import (
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestDecodeResultEventSuccessIncludesCompletionFields(t *testing.T) {
	t.Parallel()

	events := decodeResultEvent(streamEvent{
		Type:       "result",
		Subtype:    "success",
		SessionID:  "session-1",
		Result:     "done",
		StopReason: "end_turn",
	}, rawBase{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	})
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	if events[0].EventType != "turn:complete" {
		t.Fatalf("events[0].EventType = %q, want turn:complete", events[0].EventType)
	}
	payload, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("events[0].Data type = %T, want map[string]any", events[0].Data)
	}
	if payload["success"] != true {
		t.Fatalf("payload[success] = %v, want true", payload["success"])
	}
	if payload["error"] != nil {
		t.Fatalf("payload[error] = %v, want omitted", payload["error"])
	}
	if payload["result"] != "done" || payload["summary"] != "done" || payload["message"] != "done" {
		t.Fatalf("payload = %#v, want result/summary/message=done", payload)
	}
	if payload["stop_reason"] != "end_turn" {
		t.Fatalf("payload[stop_reason] = %v, want end_turn", payload["stop_reason"])
	}
}

func TestDecodeResultEventFailureUsesErrorOnly(t *testing.T) {
	t.Parallel()

	events := decodeResultEvent(streamEvent{
		Type:      "result",
		Subtype:   "error",
		SessionID: "session-1",
		Result:    "fatal issue",
	}, rawBase{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	})
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	payload, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("events[0].Data type = %T, want map[string]any", events[0].Data)
	}
	if payload["success"] != false {
		t.Fatalf("payload[success] = %v, want false", payload["success"])
	}
	if payload["error"] != "fatal issue" {
		t.Fatalf("payload[error] = %v, want fatal issue", payload["error"])
	}
	if payload["result"] != nil || payload["summary"] != nil || payload["message"] != nil || payload["stop_reason"] != nil {
		t.Fatalf("payload = %#v, want completion fields omitted on error", payload)
	}
}

func TestDecodeResultEventFailureFallsBackToDefaultError(t *testing.T) {
	t.Parallel()

	events := decodeResultEvent(streamEvent{
		Type:      "result",
		Subtype:   "error",
		SessionID: "session-1",
	}, rawBase{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		TurnID:   "turn-1",
	})
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}

	payload, ok := events[0].Data.(map[string]any)
	if !ok {
		t.Fatalf("events[0].Data type = %T, want map[string]any", events[0].Data)
	}
	if payload["success"] != false {
		t.Fatalf("payload[success] = %v, want false", payload["success"])
	}
	if payload["error"] != "Claude API temporarily unavailable" {
		t.Fatalf("payload[error] = %v, want Claude API temporarily unavailable", payload["error"])
	}
}

func TestDecodeResultEventTerminalTruthReachesTranslatorAndHandle(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		raw        streamEvent
		wantStatus string
		wantErr    bool
	}{
		{name: "success", raw: streamEvent{Type: "result", Subtype: "success", Result: "done"}, wantStatus: "completed"},
		{name: "failure", raw: streamEvent{Type: "result", Subtype: "error", Result: "provider failed"}, wantStatus: "failed", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := decodeResultEvent(test.raw, rawBase{AgentID: "agent-1", ThreadID: "thread-1", TurnID: "turn-1"})
			if len(events) != 1 {
				t.Fatalf("len(events) = %d, want 1", len(events))
			}
			payload, ok := events[0].Data.(map[string]any)
			if !ok || payload["status"] != test.wantStatus {
				t.Fatalf("decoded terminal payload = %#v, want status %q", events[0].Data, test.wantStatus)
			}
			events[0] = attachClaudeTerminalOutcome(events[0])
			translated, ok := translateTurnEvent(events[0])
			if !ok {
				t.Fatal("translateTurnEvent() rejected decoded result")
			}
			completed, ok := translated.(turndto.TurnCompleted)
			if !ok || completed.Status != test.wantStatus || completed.Success == test.wantErr {
				t.Fatalf("translated terminal = %#v, want status=%q wantErr=%v", translated, test.wantStatus, test.wantErr)
			}

			handle := newTurnHandle("local-1", "turn-1")
			s := &session{activeTurn: handle}
			s.finishTurnFromRaw(nil, events[0])
			<-handle.Done()
			if (handle.Err() != nil) != test.wantErr {
				t.Fatalf("handle error = %v, wantErr %v", handle.Err(), test.wantErr)
			}
		})
	}
}

func TestTranslateTurnEventCompleteMapsCompletionFields(t *testing.T) {
	t.Parallel()

	got, ok := translateCanonicalClaudeTerminal(dto.RawProviderEvent{
		EventType: "turn:complete",
		Data: map[string]any{
			"agent_id":    "agent-1",
			"thread_id":   "thread-1",
			"session_id":  "thread-1",
			"turn_id":     "turn-1",
			"success":     true,
			"status":      "completed",
			"result":      "done",
			"summary":     "done",
			"message":     "done",
			"stop_reason": "end_turn",
		},
	})
	if !ok {
		t.Fatal("translateTurnEvent(...) ok = false, want true")
	}

	completed, ok := got.(turndto.TurnCompleted)
	if !ok {
		t.Fatalf("translateTurnEvent(...) type = %T, want turn.TurnCompleted", got)
	}
	if !completed.Success {
		t.Fatal("TurnCompleted.Success = false, want true")
	}
	if completed.Error != "" {
		t.Fatalf("TurnCompleted.Error = %q, want empty", completed.Error)
	}
	if completed.Result != "done" || completed.Summary != "done" || completed.Message != "done" {
		t.Fatalf("TurnCompleted = %#v, want result/summary/message=done", completed)
	}
	if completed.StopReason != "end_turn" {
		t.Fatalf("TurnCompleted.StopReason = %q, want end_turn", completed.StopReason)
	}
}
