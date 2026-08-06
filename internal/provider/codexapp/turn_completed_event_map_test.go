package codexapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// sniffAndTranslate 解码 sniffTurnOutput 改写后的参数，并转成 TurnCompleted DTO。
func sniffAndTranslate(t *testing.T, s *session, method string, raw json.RawMessage) (turndto.TurnCompleted, bool) {
	t.Helper()
	merged := s.sniffTurnOutput(method, raw)
	payload := decodeEventPayload(merged)
	outcome := canonicalTurnTerminalOutcome(method, payload)
	ev, ok := translateTurnEventWithRuntimeHooks(testRuntimeHooks(t), method, payload, &outcome)
	if !ok {
		return turndto.TurnCompleted{}, false
	}
	completed, ok := ev.(turndto.TurnCompleted)
	return completed, ok
}

// TestTurnCompleted_EndToEnd_AccumulatedResult 验证消息 delta 到终态事件的端到端链路会合并 Result。
func TestTurnCompleted_EndToEnd_AccumulatedResult(t *testing.T) {
	s := newAccumulatorTestSession()

	// 同一 turn 下连续写入三段 message delta，终态翻译时应按顺序合并。
	for _, chunk := range []string{"part-1", "part-2", "part-3"} {
		raw, err := json.Marshal(map[string]any{
			"turnId": "T-e2e",
			"stream": "message",
			"delta":  chunk,
		})
		if err != nil {
			t.Fatalf("marshal delta: %v", err)
		}
		_ = s.sniffTurnOutput("item/agentMessage/delta", raw)
	}

	terminal, err := json.Marshal(map[string]any{
		"turnId":  "T-e2e",
		"success": true,
		"status":  "completed",
		"summary": "public accumulated success",
	})
	if err != nil {
		t.Fatalf("marshal terminal: %v", err)
	}
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected turn/completed to translate into TurnCompleted DTO")
	}
	if !completed.Success {
		t.Fatalf("expected Success=true")
	}
	if completed.Result != "part-1part-2part-3" {
		t.Fatalf("expected merged Result, got %q", completed.Result)
	}
	if completed.Status != "completed" {
		t.Fatalf("expected status preserved, got %q", completed.Status)
	}
}

func TestOnNotificationFirstTerminalWinsBeforeDispatch(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher, testRuntimeHooks(t))
	completedEvents := make(chan turndto.TurnCompleted, 2)
	cancelSub := event.Subscribe(bus, func(ev turndto.TurnCompleted) {
		completedEvents <- ev
	})
	defer cancelSub()

	h := newTurnHandle("turn-local", "turn-provider")
	s := &session{
		agentID:      "agent-public",
		dispatcher:   dispatcher,
		turns:        map[string]*turnHandle{"turn-provider": h},
		activeTurnID: "turn-provider",
	}
	s.onNotification("turn/completed", json.RawMessage(`{"turnId":"turn-provider","threadId":"thread-provider","timestamp":"2026-07-16T10:11:12.123Z","success":true,"status":"completed","summary":"public first success"}`))
	s.onNotification("turn/failed", json.RawMessage(`{"turnId":"turn-provider","threadId":"thread-provider","timestamp":"2026-07-16T10:11:12.123Z","success":false,"status":"failed","error":"late failure"}`))

	select {
	case completed := <-completedEvents:
		if !completed.Success || completed.Status != "completed" {
			t.Fatalf("first terminal = %#v, want completed success", completed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for first terminal")
	}
	select {
	case duplicate := <-completedEvents:
		t.Fatalf("conflicting terminal was dispatched: %#v", duplicate)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("first terminal did not finish turn handle")
	}
	if err := h.Err(); err != nil {
		t.Fatalf("turn handle error = %v, want nil", err)
	}
}

func TestOnNotificationOwnerlessTerminalIsRejected(t *testing.T) {
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher, testRuntimeHooks(t))
	completedEvents := make(chan turndto.TurnCompleted, 1)
	cancelSub := event.Subscribe(bus, func(ev turndto.TurnCompleted) {
		completedEvents <- ev
	})
	defer cancelSub()

	s := &session{agentID: "agent-public", dispatcher: dispatcher}
	s.onNotification("turn/completed", json.RawMessage(`{"turnId":"turn-provider","threadId":"thread-provider","timestamp":"2026-07-16T10:11:12.123Z","success":true,"status":"completed","summary":"public ownerless success"}`))

	select {
	case terminal := <-completedEvents:
		t.Fatalf("ownerless terminal was dispatched: %#v", terminal)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestOnNotificationOldOwnerlessTerminalIsRejectedAfterChurn(t *testing.T) {
	const liveTerminalCount = 257
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher, testRuntimeHooks(t))
	completedEvents := make(chan turndto.TurnCompleted, liveTerminalCount+1)
	cancelSub := event.Subscribe(bus, func(ev turndto.TurnCompleted) {
		completedEvents <- ev
	})
	defer cancelSub()

	s := &session{agentID: "agent-public", dispatcher: dispatcher, turns: map[string]*turnHandle{}}
	for i := range liveTerminalCount {
		turnID := fmt.Sprintf("turn-%d", i)
		s.turns[turnID] = newTurnHandle("local-"+turnID, turnID)
		s.onNotification("turn/completed", json.RawMessage(`{"turnId":"`+turnID+`","timestamp":"2026-07-16T10:11:12.123Z","success":true,"status":"completed","summary":"public live success"}`))
	}
	for range liveTerminalCount {
		select {
		case <-completedEvents:
		case <-time.After(time.Second):
			t.Fatal("timed out draining live terminal events")
		}
	}

	s.onNotification("turn/failed", json.RawMessage(`{"turnId":"turn-0","timestamp":"2026-07-16T10:11:12.123Z","success":false,"status":"failed","error":"late failure"}`))
	select {
	case duplicate := <-completedEvents:
		t.Fatalf("old ownerless terminal was dispatched after churn: %#v", duplicate)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTranslateTurnEvent_MessageAliasPayloadReasoningStream(t *testing.T) {
	payload := map[string]any{
		"threadId": "thread-1",
		"turnId":   "T-reasoning",
		"itemId":   "assistant-reasoning-1",
		"stream":   "reasoning",
		"delta":    "thinking text",
	}

	ev, ok := translateTurnEvent("message.delta", payload)
	if !ok {
		t.Fatal("expected message.delta to translate into TurnOutputDelta")
	}
	delta, ok := ev.(turndto.TurnOutputDelta)
	if !ok {
		t.Fatalf("event type = %T, want TurnOutputDelta", ev)
	}
	if delta.Stream != "reasoning" {
		t.Fatalf("TurnOutputDelta.Stream = %q, want reasoning", delta.Stream)
	}
	if delta.Delta != "thinking text" {
		t.Fatalf("TurnOutputDelta.Delta = %q, want reasoning text", delta.Delta)
	}
	if delta.ItemID != "assistant-reasoning-1" {
		t.Fatalf("TurnOutputDelta.ItemID = %q, want assistant-reasoning-1", delta.ItemID)
	}
}

func TestTurnCompleted_EndToEnd_ReasoningDeltaNotAccumulatedAsResult(t *testing.T) {
	s := newAccumulatorTestSession()
	rawDelta, err := json.Marshal(map[string]any{
		"turnId": "T-reasoning",
		"stream": "reasoning",
		"delta":  "thinking text",
	})
	if err != nil {
		t.Fatalf("marshal delta: %v", err)
	}
	_ = s.sniffTurnOutput("message.delta", rawDelta)

	terminal, err := json.Marshal(map[string]any{
		"turnId":  "T-reasoning",
		"success": true,
		"summary": "public reasoning success",
	})
	if err != nil {
		t.Fatalf("marshal terminal: %v", err)
	}
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected turn/completed to translate into TurnCompleted DTO")
	}
	if completed.Result != "" {
		t.Fatalf("TurnCompleted.Result = %q, want no reasoning text in final result", completed.Result)
	}
}

// TestTurnCompleted_EndToEnd_TruncatedPropagatesButResultStillSet 验证超限 delta 会设置截断信号。
// 已落入缓冲区的内容仍按上限参与 DTO 翻译。
func TestTurnCompleted_EndToEnd_TruncatedPropagatesButResultStillSet(t *testing.T) {
	s := newAccumulatorTestSession()
	big := strings.Repeat("x", turnOutputAccumulatorMaxBytes+1)
	rawDelta, _ := json.Marshal(map[string]any{
		"turnId": "T-trunc",
		"stream": "message",
		"delta":  big,
	})
	_ = s.sniffTurnOutput("item/agentMessage/delta", rawDelta)

	terminal, _ := json.Marshal(map[string]any{
		"turnId":  "T-trunc",
		"success": true,
		"summary": "public truncated success",
	})
	merged := s.sniffTurnOutput("turn/completed", terminal)
	payload := decodeEventPayload(merged)
	if v, _ := payload["truncated"].(bool); !v {
		t.Fatalf("expected truncated=true in payload, got %v", payload["truncated"])
	}
	// DTO 当前没有 Truncated 字段；超限 delta 在进入缓冲前被丢弃，因此 Result 为空。
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected TurnCompleted DTO on second sniff")
	}
	if completed.Result != "" {
		t.Fatalf("expected empty Result (cap dropped first oversized delta), got len=%d", len(completed.Result))
	}
}

// TestTurnCompleted_EndToEnd_ProviderProvidedFieldsPreserved 验证 provider 直接提供的新字段会原样透传。
func TestTurnCompleted_EndToEnd_ProviderProvidedFieldsPreserved(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId":      "T-direct",
		"success":     true,
		"result":      "from-provider",
		"summary":     "brief",
		"message":     "all good",
		"stop_reason": "end_turn",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected DTO translation")
	}
	if completed.Result != "from-provider" {
		t.Fatalf("Result mismatch: %q", completed.Result)
	}
	if completed.Summary != "brief" {
		t.Fatalf("Summary mismatch: %q", completed.Summary)
	}
	if completed.Message != "all good" {
		t.Fatalf("Message mismatch: %q", completed.Message)
	}
	if completed.StopReason != "end_turn" {
		t.Fatalf("StopReason mismatch: %q", completed.StopReason)
	}
}

func TestTurnCompleted_EndToEnd_SuccessReasonDoesNotPopulateError(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId":  "T-synthetic",
		"success": true,
		"status":  "completed",
		"summary": "public synthetic success",
		"reason":  "assistant_message_completed",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected DTO translation")
	}
	if !completed.Success {
		t.Fatalf("expected Success=true")
	}
	if completed.Reason != "assistant_message_completed" {
		t.Fatalf("Reason = %q, want assistant_message_completed", completed.Reason)
	}
	if completed.Error != "" {
		t.Fatalf("Error = %q, want empty for successful terminal reason", completed.Error)
	}
}

// TestTurnCompleted_EndToEnd_NoDeltaNoResult 验证没有 message delta 的工具型 turn 会产生空 Result。
func TestTurnCompleted_EndToEnd_NoDeltaNoResult(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId":  "T-quiet",
		"success": true,
		"status":  "completed",
		"summary": "public quiet success",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected DTO translation")
	}
	if completed.Result != "" {
		t.Fatalf("expected empty Result for tool-only turn, got %q", completed.Result)
	}
	if !completed.Success {
		t.Fatalf("expected Success=true")
	}
}

// TestTurnCompleted_EndToEnd_FailedTurnCarriesError 覆盖失败终态：Codex turn 失败时必须携带 Error。
// orchestration report fallback 依赖该字段，避免失败子 agent 返回空报告。
func TestTurnCompleted_EndToEnd_FailedTurnCarriesError(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId":  "T-fail",
		"success": false,
		"status":  "failed",
		"error":   "codex tool call denied",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected turn/completed to translate into TurnCompleted DTO")
	}
	if completed.Success {
		t.Fatalf("expected Success=false for a failed turn")
	}
	if completed.Error != "codex tool call denied" {
		t.Fatalf("TurnCompleted.Error = %q, want the failure detail", completed.Error)
	}
}

func TestTurnCompleted_EndToEnd_FailedTurnReasonCarriesError(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId":  "T-fail-reason",
		"success": false,
		"status":  "failed",
		"reason":  "tool rejected",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/completed", terminal)
	if !ok {
		t.Fatalf("expected turn/completed to translate into TurnCompleted DTO")
	}
	if completed.Success {
		t.Fatalf("expected Success=false for a failed turn")
	}
	if completed.Error != "tool rejected" {
		t.Fatalf("TurnCompleted.Error = %q, want reason as failure detail", completed.Error)
	}
}

func TestTurnCompleted_EndToEnd_TurnFailedEventCarriesError(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId": "T-failed-event",
		"status": "failed",
		"error":  "The 'gpt-5' model is not supported when using Codex with a ChatGPT account.",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/failed", terminal)
	if !ok {
		t.Fatalf("expected turn/failed to translate into TurnCompleted DTO")
	}
	if completed.Success {
		t.Fatalf("expected Success=false for a turn/failed event")
	}
	if !strings.Contains(completed.Error, "gpt-5") {
		t.Fatalf("TurnCompleted.Error = %q, want the provider failure detail", completed.Error)
	}
}

func TestFinishTurn_ModelUnsupportedErrorCompletesHandleWithNotice(t *testing.T) {
	h := newTurnHandle("local-1", "T-model-error")
	s := &session{
		turns: map[string]*turnHandle{
			"T-model-error": h,
		},
		activeTurnID: "T-model-error",
		runtimeConfig: map[string]any{
			"model": "gpt-5",
		},
	}
	terminal, _ := json.Marshal(map[string]any{
		"turnId": "T-model-error",
		"error":  "The 'gpt-5' model is not supported when using Codex with a ChatGPT account.",
	})

	s.finishTurn(dto.RawProviderEvent{EventType: "turn/failed", Data: terminal, Terminal: &dto.TerminalOutcome{Status: "failed"}})

	select {
	case <-h.Done():
	default:
		t.Fatal("expected failed turn to complete the handle")
	}
	if h.Err() == nil {
		t.Fatal("expected failed turn to complete with an error")
	}
	if !strings.Contains(h.Err().Error(), `Codex model "gpt-5" is not supported`) {
		t.Fatalf("turn handle error = %q, want actionable model notice", h.Err().Error())
	}
	if s.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want cleared", s.activeTurnID)
	}
	if _, ok := s.turns["T-model-error"]; ok {
		t.Fatal("expected completed turn to be removed")
	}
}

// TestTurnCompleted_EndToEnd_AbortedTurnIsUnsuccessful 覆盖 provider abort 不能伪装成宿主确认的用户取消。
func TestTurnCompleted_EndToEnd_AbortedTurnIsUnsuccessful(t *testing.T) {
	s := newAccumulatorTestSession()
	terminal, _ := json.Marshal(map[string]any{
		"turnId": "T-abort",
		"reason": "interrupted by user",
	})
	completed, ok := sniffAndTranslate(t, s, "turn/aborted", terminal)
	if !ok {
		t.Fatalf("expected turn/aborted to translate into TurnCompleted DTO")
	}
	if completed.Success {
		t.Fatalf("expected Success=false for an aborted turn")
	}
	if completed.Reason != "provider" {
		t.Fatalf("TurnCompleted.Reason = %q, want provider cause", completed.Reason)
	}
}

func TestOnNotification_MalformedTerminalFailsActiveTurnExactlyOnceWithoutPayloadLeak(t *testing.T) {
	tests := []struct {
		name   string
		method string
		params json.RawMessage
	}{
		{name: "missing outcome", method: "turn/completed", params: json.RawMessage(`{"turnId":"T-malformed","prompt":"raw-secret"}`)},
		{name: "unknown outcome", method: "turn/completed", params: json.RawMessage(`{"turnId":"T-malformed","success":false,"status":"raw-secret"}`)},
		{name: "conflicting outcome", method: "turn/completed", params: json.RawMessage(`{"turnId":"T-malformed","success":true,"status":"failed","prompt":"raw-secret"}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTurnHandle("local-malformed", "T-malformed")
			s := &session{
				turns: map[string]*turnHandle{
					"T-malformed": h,
				},
				activeTurnID: "T-malformed",
			}

			s.onNotification(tt.method, tt.params)
			s.onNotification(tt.method, tt.params)

			select {
			case <-h.Done():
			default:
				t.Fatal("malformed terminal notification did not finish active turn")
			}
			if h.Err() == nil {
				t.Fatal("malformed terminal notification completed without an error")
			}
			errText := h.Err().Error()
			if errText != "terminal contract: malformed terminal payload" {
				t.Fatalf("turn error = %q, want safe canonical contract failure", errText)
			}
			if strings.Contains(errText, "raw-secret") {
				t.Fatalf("turn error leaked raw payload: %q", errText)
			}
			if s.activeTurnID != "" {
				t.Fatalf("activeTurnID = %q, want cleared", s.activeTurnID)
			}
			if _, ok := s.turns["T-malformed"]; ok {
				t.Fatal("active turn remained registered after malformed terminal notification")
			}
		})
	}
}

func TestOnNotification_StaleMalformedOutcomeDoesNotFinishActiveTurn(t *testing.T) {
	tests := []struct {
		name   string
		params json.RawMessage
	}{
		{name: "missing", params: json.RawMessage(`{"turnId":"raw-turn","agentId":"raw-agent","prompt":"raw-secret"}`)},
		{name: "unknown", params: json.RawMessage(`{"turnId":"raw-turn","agentId":"raw-agent","success":false,"status":"raw-secret"}`)},
		{name: "conflicting", params: json.RawMessage(`{"turnId":"raw-turn","agentId":"raw-agent","success":true,"status":"failed","prompt":"raw-secret"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertStaleMalformedTerminalDoesNotFinishActiveTurn(t, tt.params)
		})
	}
}

func assertStaleMalformedTerminalDoesNotFinishActiveTurn(t *testing.T, params json.RawMessage) {
	t.Helper()
	bus := event.NewDispatcher()
	defer func() { _ = bus.Close() }()
	dispatcher := unified.NewEventDispatcher(bus, nil)
	RegisterTranslators(dispatcher, testRuntimeHooks(t))
	surface := make(chan turndto.TurnTerminalV2, 1)
	for _, cancel := range eventsurface.Bind(bus, nil, func(method string, payload any) {
		if method == eventsurface.MethodTurnTerminal {
			surface <- payload.(turndto.TurnTerminalV2)
		}
	}) {
		defer cancel()
	}
	h := newTurnHandle("local-trusted", "T-trusted")
	s := &session{agentID: "trusted-agent", dispatcher: dispatcher, turns: map[string]*turnHandle{"T-trusted": h}, activeTurnID: "T-trusted"}
	s.onNotification("turn/completed", params)
	select {
	case terminal := <-surface:
		t.Fatalf("stale malformed terminal published as current turn: %#v", terminal)
	default:
	}
	select {
	case <-h.Done():
		t.Fatalf("stale malformed terminal finished active turn: %v", h.Err())
	default:
	}
	if s.activeTurnID != "T-trusted" {
		t.Fatalf("activeTurnID = %q, want retained", s.activeTurnID)
	}
	if s.turns["T-trusted"] != h {
		t.Fatal("stale malformed terminal removed active turn handle")
	}
}

func TestOnNotification_StaleMalformedTerminalDoesNotLeakRawTurnIDToLogs(t *testing.T) {
	const rawTurnID = "sk-live-token-1234567890/private/stack"
	var logs bytes.Buffer
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{Mode: pkglogger.Production, Level: slog.LevelInfo})
	runtime.InitWithConsoleWriter(&logs)
	runtime.BindDefault()

	h := newTurnHandle("local-trusted", "T-trusted")
	s := &session{agentID: "trusted-agent", turns: map[string]*turnHandle{"T-trusted": h}, activeTurnID: "T-trusted"}
	s.onNotification("turn/completed", json.RawMessage(`{"turnId":"`+rawTurnID+`","prompt":"raw-secret"}`))

	if strings.Contains(logs.String(), rawTurnID) || strings.Contains(logs.String(), "sk-live-token") || strings.Contains(logs.String(), "/private/stack") {
		t.Fatalf("stale terminal log leaked raw turn identity: %s", logs.String())
	}
	if s.activeTurnID != "T-trusted" || s.turns["T-trusted"] != h {
		t.Fatal("stale malformed terminal changed active turn state")
	}
}

func TestTakeActiveTurnIfMatchesRequiresLiveHandle(t *testing.T) {
	s := &session{turns: map[string]*turnHandle{}, activeTurnID: "T-inconsistent"}

	h, matched := s.takeActiveTurnIfMatches("T-inconsistent")

	if matched || h != nil {
		t.Fatalf("takeActiveTurnIfMatches() = (%#v, %t), want (nil, false)", h, matched)
	}
	if s.activeTurnID != "T-inconsistent" {
		t.Fatalf("activeTurnID = %q, want retained", s.activeTurnID)
	}
}

func TestOnNotification_UnattributableMalformedTerminalFailsConnectionWithoutForgingTurn(t *testing.T) {
	h := newTurnHandle("local-unattributable", "T-unattributable")
	s := &session{
		agentID:      "trusted-agent",
		turns:        map[string]*turnHandle{"T-unattributable": h},
		activeTurnID: "T-unattributable",
	}
	s.threadID.Store("trusted-thread")

	s.onNotification("turn/completed", json.RawMessage(`{"prompt":"raw-secret"`))

	select {
	case <-h.Done():
		if h.Err() == nil {
			t.Fatal("unattributable malformed terminal completed without connection failure")
		}
	default:
		t.Fatal("unattributable malformed terminal did not fail connection")
	}
	if s.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want cleared by connection failure", s.activeTurnID)
	}
	if _, ok := s.turns["T-unattributable"]; ok {
		t.Fatal("connection failure retained active turn handle")
	}
}

func TestOnNotification_MalformedNonTerminalDoesNotFinishActiveTurn(t *testing.T) {
	h := newTurnHandle("local-non-terminal", "T-non-terminal")
	s := &session{
		turns: map[string]*turnHandle{
			"T-non-terminal": h,
		},
		activeTurnID: "T-non-terminal",
	}

	s.onNotification("item/agentMessage/delta", json.RawMessage(`{"prompt":"raw-secret"`))

	select {
	case <-h.Done():
		t.Fatalf("malformed non-terminal notification finished active turn: %v", h.Err())
	default:
	}
	if s.activeTurnID != "T-non-terminal" {
		t.Fatalf("activeTurnID = %q, want retained", s.activeTurnID)
	}
	if s.turns["T-non-terminal"] != h {
		t.Fatal("malformed non-terminal notification removed active turn handle")
	}
}

func TestOnNotification_MalformedTerminalFromAlienThreadDoesNotFinishActiveTurn(t *testing.T) {
	h := newTurnHandle("local-own", "T-own")
	s := &session{
		turns: map[string]*turnHandle{
			"T-own": h,
		},
		activeTurnID: "T-own",
	}
	s.threadID.Store("thread-own")
	params := json.RawMessage(`{"threadId":"thread-alien","prompt":"raw-secret"}`)

	s.onNotification("turn/completed", params)

	select {
	case <-h.Done():
		t.Fatalf("alien malformed terminal notification finished active turn: %v", h.Err())
	default:
	}
	if s.activeTurnID != "T-own" {
		t.Fatalf("activeTurnID = %q, want retained", s.activeTurnID)
	}
	if s.turns["T-own"] != h {
		t.Fatal("alien malformed terminal notification removed active turn handle")
	}
}

func TestOnNotification_PublicThreadTerminalCompletesActiveTurn(t *testing.T) {
	h := newTurnHandle("local-own", "T-own")
	s := &session{
		agentID:      "thread-public",
		turns:        map[string]*turnHandle{"T-own": h},
		activeTurnID: "T-own",
	}
	s.threadID.Store("thread-provider")

	s.onNotification("turn/completed", json.RawMessage(
		`{"threadId":"thread-public","turnId":"T-own","timestamp":"2026-07-25T04:16:48Z","success":true,"status":"completed","summary":"public thread success"}`,
	))

	select {
	case <-h.Done():
	case <-time.After(time.Second):
		t.Fatal("public-thread terminal did not finish active turn")
	}
	if err := h.Err(); err != nil {
		t.Fatalf("public-thread terminal error = %v, want nil", err)
	}
	if s.activeTurnID != "" || s.turns["T-own"] != nil {
		t.Fatal("public-thread terminal did not clear active turn ownership")
	}
}

func TestOnNotification_PublicThreadMalformedTerminalFailsActiveTurn(t *testing.T) {
	h := newTurnHandle("local-own", "T-own")
	s := &session{
		agentID:      "thread-public",
		turns:        map[string]*turnHandle{"T-own": h},
		activeTurnID: "T-own",
	}
	s.threadID.Store("thread-provider")

	s.onNotification("turn/completed", json.RawMessage(
		`{"thread_id":"thread-public","turn_id":"T-own","prompt":"raw-secret"}`,
	))

	select {
	case <-h.Done():
	case <-time.After(time.Second):
		t.Fatal("public-thread malformed terminal did not fail active turn")
	}
	if err := h.Err(); err == nil || !strings.Contains(err.Error(), "terminal contract: malformed terminal payload") {
		t.Fatalf("public-thread malformed terminal error = %v", err)
	}
	if s.activeTurnID != "" || s.turns["T-own"] != nil {
		t.Fatal("public-thread malformed terminal did not clear active turn ownership")
	}
}
