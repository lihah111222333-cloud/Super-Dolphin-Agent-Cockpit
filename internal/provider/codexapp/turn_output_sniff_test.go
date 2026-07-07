package codexapp

import (
	"encoding/json"
	"strings"
	"testing"
)

// makeDeltaParams builds a TurnOutputDelta-shaped payload (stream="message").
func makeDeltaParams(t *testing.T, turnID, delta string) json.RawMessage {
	t.Helper()
	buf, err := json.Marshal(map[string]any{
		"turnId": turnID,
		"stream": "message",
		"delta":  delta,
	})
	if err != nil {
		t.Fatalf("marshal delta: %v", err)
	}
	return buf
}

// makeTerminalParams builds a turn/completed-shaped payload.
func makeTerminalParams(t *testing.T, turnID string) json.RawMessage {
	t.Helper()
	buf, err := json.Marshal(map[string]any{
		"turnId":  turnID,
		"success": true,
	})
	if err != nil {
		t.Fatalf("marshal terminal: %v", err)
	}
	return buf
}

func TestSniffTurnOutput_AccumulatesAndMerges(t *testing.T) {
	s := newAccumulatorTestSession()

	// First delta.
	// NOTE: stringValue trims surrounding whitespace, so deltas with only
	// trailing/leading whitespace would not survive payload decoding. The
	// accumulator therefore concatenates the trimmed delta tokens as the
	// provider exposes them; tests use non-whitespace tokens.
	out := s.sniffTurnOutput("item/agentMessage/delta", makeDeltaParams(t, "T1", "hello"))
	// Sniff returns the original params unchanged for deltas.
	if string(out) == "" {
		t.Fatalf("expected delta params returned, got empty")
	}
	// Second delta.
	_ = s.sniffTurnOutput("agent_message_delta", makeDeltaParams(t, "T1", "-world"))

	// Terminal event — should produce merged "result".
	merged := s.sniffTurnOutput("turn/completed", makeTerminalParams(t, "T1"))
	var payload map[string]any
	if err := json.Unmarshal(merged, &payload); err != nil {
		t.Fatalf("unmarshal merged: %v", err)
	}
	if got, _ := payload["result"].(string); got != "hello-world" {
		t.Fatalf("expected merged result 'hello-world', got %q", got)
	}
	if _, ok := payload["truncated"]; ok {
		t.Fatalf("did not expect truncated flag")
	}
	// Buffer must have been consumed.
	if _, ok := s.turnOutputAccumulator["T1"]; ok {
		t.Fatalf("expected accumulator dropped on terminal merge")
	}
}

func TestSniffTurnOutput_PassThroughUnrelatedMethods(t *testing.T) {
	s := newAccumulatorTestSession()
	raw := json.RawMessage(`{"foo":"bar"}`)
	out := s.sniffTurnOutput("unrelated/method", raw)
	if string(out) != string(raw) {
		t.Fatalf("expected unrelated params unchanged, got %q", string(out))
	}
}

func TestSniffTurnOutput_TerminalWithoutBufferLeavesPayloadUntouched(t *testing.T) {
	s := newAccumulatorTestSession()
	raw := makeTerminalParams(t, "T-empty")
	out := s.sniffTurnOutput("turn/completed", raw)
	if string(out) != string(raw) {
		t.Fatalf("expected terminal params unchanged when no buffer, got %q", string(out))
	}
}

func TestSniffTurnOutput_TruncatedFlagPropagated(t *testing.T) {
	s := newAccumulatorTestSession()
	// Force-truncate by exceeding cap with one oversized delta.
	big := strings.Repeat("x", turnOutputAccumulatorMaxBytes+1)
	_ = s.sniffTurnOutput("item/agentMessage/delta", makeDeltaParams(t, "T-trunc", big))
	out := s.sniffTurnOutput("turn/completed", makeTerminalParams(t, "T-trunc"))
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v, _ := payload["truncated"].(bool); !v {
		t.Fatalf("expected truncated=true in merged payload, got %v", payload["truncated"])
	}
}

func TestSniffTurnOutput_DoesNotClobberExistingResult(t *testing.T) {
	s := newAccumulatorTestSession()
	_ = s.sniffTurnOutput("item/agentMessage/delta", makeDeltaParams(t, "T-pre", "buffered"))
	// Provider already provided a result field.
	raw, err := json.Marshal(map[string]any{
		"turnId":  "T-pre",
		"success": true,
		"result":  "from-provider",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := s.sniffTurnOutput("turn/completed", raw)
	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if payload["result"] != "from-provider" {
		t.Fatalf("expected provider result preserved, got %v", payload["result"])
	}
}

// --- cleanup hook coverage ----------------------------------------------

// fakeTurnHandle is a minimal turnHandle replacement for cleanup hook tests.
// We only need takeTurn/failTurns/applyReplayedTurn to manipulate the maps
// and call dropTurnOutputAccumulator — neither path exercises h.complete in
// a way that depends on transport.

func TestCleanupHook_TakeTurnDropsAccumulator(t *testing.T) {
	s := newAccumulatorTestSession()
	s.turns = map[string]*turnHandle{}
	s.suppressed = map[string]struct{}{}
	tid := "turn-take"
	s.turns[tid] = &turnHandle{providerID: tid, done: make(chan struct{})}
	s.appendTurnOutputDelta(tid, "lingering")

	s.takeTurn(tid)
	if _, ok := s.turnOutputAccumulator[tid]; ok {
		t.Fatalf("expected takeTurn to drop accumulator buffer")
	}
}

func TestCleanupHook_FailTurnsDropsAllAccumulators(t *testing.T) {
	s := newAccumulatorTestSession()
	s.turns = map[string]*turnHandle{}
	for _, tid := range []string{"t-a", "t-b", "t-c"} {
		s.turns[tid] = &turnHandle{providerID: tid, done: make(chan struct{})}
		s.appendTurnOutputDelta(tid, "data")
	}

	s.failTurns(nil)
	if len(s.turnOutputAccumulator) != 0 {
		t.Fatalf("expected failTurns to drop all accumulators, remaining=%d", len(s.turnOutputAccumulator))
	}
}

func TestCleanupHook_ApplyReplayedTurnDropsStaleBuffer(t *testing.T) {
	s := newAccumulatorTestSession()
	s.turns = map[string]*turnHandle{}
	oldID := "turn-old"
	newID := "turn-new"
	h := &turnHandle{providerID: oldID, done: make(chan struct{})}
	s.turns[oldID] = h
	s.appendTurnOutputDelta(oldID, "stale")

	snapshot := &turnReplayState{providerID: oldID, handle: h}
	s.applyReplayedTurn(snapshot, newID)

	if _, ok := s.turnOutputAccumulator[oldID]; ok {
		t.Fatalf("expected applyReplayedTurn to drop stale accumulator under %q", oldID)
	}
	if _, ok := s.turns[newID]; !ok {
		t.Fatalf("expected new providerID %q registered in turns map", newID)
	}
}

// Smoke: concurrent sniff (delta + terminal) on disjoint turn-ids — verifies
// no data race when multiple goroutines drive onNotification's sniff path.
func TestSniffTurnOutput_ConcurrentPerTurn(t *testing.T) {
	s := newAccumulatorTestSession()
	const turns = 8
	goroutines := newTestGoroutineGroup(t)
	for i := range turns {
		goroutines.Go(func() {
			tid := "turn-" + string(rune('A'+i))
			for range 4 {
				_ = s.sniffTurnOutput("message.delta", makeDeltaParams(t, tid, "d"))
			}
			out := s.sniffTurnOutput("turn/completed", makeTerminalParams(t, tid))
			var payload map[string]any
			if err := json.Unmarshal(out, &payload); err != nil {
				t.Errorf("unmarshal: %v", err)
				return
			}
			if payload["result"] != "dddd" {
				t.Errorf("turn %s expected result 'dddd', got %v", tid, payload["result"])
			}
		})
	}
	goroutines.Wait()
}
