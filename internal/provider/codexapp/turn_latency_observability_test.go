package codexapp

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/kelindar/event"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestCodexTurnLatencyMilestoneFor(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		payload  map[string]any
		want     string
		wantTTFT int64
	}{
		{
			name:    "task started",
			method:  "event_msg",
			payload: map[string]any{"type": "task_started", "turn_id": "turn-1"},
			want:    "provider_task_started",
		},
		{
			name:    "context ready",
			method:  "turn_context",
			payload: map[string]any{"turn_id": "turn-1"},
			want:    "provider_context_ready",
		},
		{
			name:   "user entered provider",
			method: "response_item",
			payload: map[string]any{"turn_id": "turn-1", "item": map[string]any{
				"type": "message", "role": "user",
			}},
			want: "provider_user_message",
		},
		{
			name:   "first commentary",
			method: "item/completed",
			payload: map[string]any{"turn_id": "turn-1", "item": map[string]any{
				"type": "message", "role": "assistant", "phase": "commentary",
			}},
			want: "provider_commentary",
		},
		{
			name:   "final answer",
			method: "item/completed",
			payload: map[string]any{"turn_id": "turn-1", "item": map[string]any{
				"type": "message", "role": "assistant", "phase": "final_answer",
			}},
			want: "provider_final_answer",
		},
		{
			name:     "task complete",
			method:   "event_msg",
			payload:  map[string]any{"type": "task_complete", "turn_id": "turn-1", "duration_ms": int64(30187), "time_to_first_token_ms": int64(22300)},
			want:     "provider_task_complete",
			wantTTFT: 22300,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := codexTurnLatencyMilestoneFor(tt.method, tt.payload)
			if !ok {
				t.Fatal("codexTurnLatencyMilestoneFor() ok = false")
			}
			if got.Phase != tt.want || got.TurnID != "turn-1" || got.ProviderFirstTokenMS != tt.wantTTFT {
				t.Fatalf("milestone = %+v, want phase=%q turn-1 ttft=%d", got, tt.want, tt.wantTTFT)
			}
		})
	}
}

func TestCodexTurnLatencyMilestoneForIgnoresNonFinalAssistantPhase(t *testing.T) {
	for _, phase := range []string{"", "preview"} {
		_, ok := codexTurnLatencyMilestoneFor("response_item", map[string]any{
			"turn_id": "turn-1",
			"item": map[string]any{
				"type": "message", "role": "assistant", "phase": phase,
			},
		})
		if ok {
			t.Fatalf("phase %q produced a latency milestone", phase)
		}
	}
}

type terminalOwnershipFixture struct {
	session     *session
	active      *turnHandle
	completedCh chan turndto.TurnCompleted
	toolBeginCh chan tooldto.ToolCallBegin
	toolEndCh   chan tooldto.ToolCallEnd
}

func newTerminalOwnershipFixture(t *testing.T) terminalOwnershipFixture {
	t.Helper()
	bus := event.NewDispatcher()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher)
	fixture := terminalOwnershipFixture{
		completedCh: make(chan turndto.TurnCompleted, 2),
		toolBeginCh: make(chan tooldto.ToolCallBegin, 1),
		toolEndCh:   make(chan tooldto.ToolCallEnd, 1),
	}
	cancelCompleted := event.Subscribe(bus, func(ev turndto.TurnCompleted) { fixture.completedCh <- ev })
	cancelToolBegin := event.Subscribe(bus, func(ev tooldto.ToolCallBegin) { fixture.toolBeginCh <- ev })
	cancelToolEnd := event.Subscribe(bus, func(ev tooldto.ToolCallEnd) { fixture.toolEndCh <- ev })
	t.Cleanup(func() {
		cancelCompleted()
		cancelToolBegin()
		cancelToolEnd()
		_ = bus.Close()
	})
	fixture.session = newInboundTestSession(context.Background(), nil, &ServerManager{})
	fixture.session.dispatcher = dispatcher
	fixture.active = configureSingleForceCompleteTurn(fixture.session, "turn-1")
	return fixture
}

func TestOnNotification_CommentaryToolFinalAnswerCompletesOnlyAtFinalAnswer(t *testing.T) {
	fixture := newTerminalOwnershipFixture(t)
	publishCommentaryItem(fixture.session)
	assertNoRolloutTurnCompleted(t, fixture.completedCh)
	assertTurnActive(t, fixture.session, fixture.active, "commentary completed")

	publishToolRoundTrip(t, fixture)
	assertNoRolloutTurnCompleted(t, fixture.completedCh)

	publishFinalAnswerItem(fixture.session, "final-1", "这是完整最终回复。")
	completed := waitRolloutTurnCompleted(t, fixture.completedCh)
	if completed.Result != "这是完整最终回复。" || !slices.Equal(completed.PartialItemIDs, []string{"final-1"}) {
		t.Fatalf("TurnCompleted = %+v, want complete final answer and final-1", completed)
	}
	assertCanonicalPublicSummary(t, completed, "这是完整最终回复。")
	assertTurnDone(t, fixture.active, "final answer did not complete active turn")

	publishNativeTerminal(fixture.session, "这是完整最终回复。")
	assertNoRolloutTurnCompleted(t, fixture.completedCh)
}

func TestOnNotification_NonFinalAssistantPhaseDoesNotCompleteTurn(t *testing.T) {
	for _, phase := range []string{"", "commentary", "preview"} {
		t.Run("phase_"+phase, func(t *testing.T) {
			fixture := newTerminalOwnershipFixture(t)
			publishAssistantItem(fixture.session, "assistant-1", phase, "not final")
			assertNoRolloutTurnCompleted(t, fixture.completedCh)
			assertTurnActive(t, fixture.session, fixture.active, "non-final phase "+phase)
		})
	}
}

func TestOnNotification_NativeTerminalBeforeFinalItemPublishesOnce(t *testing.T) {
	fixture := newTerminalOwnershipFixture(t)
	publishNativeTerminal(fixture.session, "native final")
	completed := waitRolloutTurnCompleted(t, fixture.completedCh)
	if completed.Result != "native final" {
		t.Fatalf("TurnCompleted.Result = %q, want native final", completed.Result)
	}
	assertCanonicalPublicSummary(t, completed, "native final")
	assertTurnDone(t, fixture.active, "native terminal did not complete active turn")
	publishFinalAnswerItem(fixture.session, "late-final", "late duplicate")
	assertNoRolloutTurnCompleted(t, fixture.completedCh)
}

func publishCommentaryItem(s *session) {
	publishAssistantItem(s, "commentary-1", "commentary", "我先检查配置。")
}

func publishFinalAnswerItem(s *session, itemID, text string) {
	publishAssistantItem(s, itemID, "final_answer", text)
}

func publishAssistantItem(s *session, itemID, phase, text string) {
	item := map[string]any{
		"id": itemID, "type": "message", "role": "assistant",
		"content": []any{map[string]any{"type": "output_text", "text": text}},
	}
	if phase != "" {
		item["phase"] = phase
	}
	s.onNotification("item/completed", mustJSON(map[string]any{
		"agentId": "agent-1", "threadId": "provider-thread-1", "turnId": "turn-1", "item": item,
	}))
}

func publishToolRoundTrip(t *testing.T, fixture terminalOwnershipFixture) {
	t.Helper()
	fixture.session.onNotification("response_item", json.RawMessage(`{
		"agentId":"agent-1","threadId":"provider-thread-1","turnId":"turn-1",
		"item":{"type":"function_call","id":"tool-item-1","call_id":"call-1","name":"exec_command","arguments":"{}"}
	}`))
	begin := waitToolCallBegin(t, fixture.toolBeginCh)
	if begin.CallID != "call-1" || begin.ToolName != "exec_command" {
		t.Fatalf("ToolCallBegin = %+v, want call-1/exec_command", begin)
	}
	fixture.session.onNotification("response_item", json.RawMessage(`{
		"agentId":"agent-1","threadId":"provider-thread-1","turnId":"turn-1",
		"item":{"type":"function_call_output","call_id":"call-1","output":"ok"}
	}`))
	end := waitToolCallEnd(t, fixture.toolEndCh)
	if end.CallID != "call-1" || end.ToolName != "exec_command" || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful call-1/exec_command", end)
	}
}

func publishNativeTerminal(s *session, result string) {
	s.onNotification("turn/completed", mustJSON(map[string]any{
		"agentId": "agent-1", "threadId": "provider-thread-1", "turnId": "turn-1",
		"timestamp": "2026-07-23T09:11:22.831Z", "success": true, "status": "completed", "result": result, "summary": result,
	}))
}

func assertTurnActive(t *testing.T, s *session, active *turnHandle, context string) {
	t.Helper()
	select {
	case <-active.Done():
		t.Fatalf("%s completed active turn: %v", context, active.Err())
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeTurnID != "turn-1" || s.turns["turn-1"] != active {
		t.Fatalf("%s active turn state = id:%q handle:%p, want turn-1/%p", context, s.activeTurnID, s.turns["turn-1"], active)
	}
}
