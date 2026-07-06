package codexapp

import (
	"strings"
	"testing"
)

// newAccumulatorTestSession returns a bare session with only the fields the
// accumulator API touches. Avoids the heavyweight newSession path (transport
// / runtime / pool) which would require a live websocket.
func newAccumulatorTestSession() *session {
	return &session{
		turnOutputAccumulator: map[string]*turnOutputBuffer{},
	}
}

func TestTurnOutputAccumulator_AppendConsumeDrop(t *testing.T) {
	s := newAccumulatorTestSession()

	// Empty turnID / delta are ignored.
	s.appendTurnOutputDelta("", "ignored")
	s.appendTurnOutputDelta("turn-1", "")
	if got, _ := s.consumeTurnOutputAccumulator("turn-1"); got != "" {
		t.Fatalf("expected empty result for unused turn, got %q", got)
	}

	// Append two deltas; consume should merge.
	s.appendTurnOutputDelta("turn-A", "hello, ")
	s.appendTurnOutputDelta("turn-A", "world")
	merged, truncated := s.consumeTurnOutputAccumulator("turn-A")
	if merged != "hello, world" {
		t.Fatalf("expected merged 'hello, world', got %q", merged)
	}
	if truncated {
		t.Fatalf("did not expect truncated flag")
	}
	// Consume is destructive.
	if _, ok := s.turnOutputAccumulator["turn-A"]; ok {
		t.Fatalf("expected buffer removed after consume")
	}
	// Second consume returns empty.
	if got, trunc := s.consumeTurnOutputAccumulator("turn-A"); got != "" || trunc {
		t.Fatalf("expected empty/false on second consume, got %q/%v", got, trunc)
	}

	// Drop removes a pending buffer without consuming.
	s.appendTurnOutputDelta("turn-B", "abc")
	s.dropTurnOutputAccumulator("turn-B")
	if got, _ := s.consumeTurnOutputAccumulator("turn-B"); got != "" {
		t.Fatalf("expected drop to clear buffer, got %q", got)
	}
}

func TestTurnOutputAccumulator_HardCapTruncates(t *testing.T) {
	s := newAccumulatorTestSession()
	// Use 100 KiB chunks so the cap (1 MiB) falls between chunk boundaries:
	// 10 chunks = 1000 KiB fits, 11 chunks = 1100 KiB would exceed the cap.
	chunkSize := 100 * 1024
	chunk := strings.Repeat("x", chunkSize)
	turnID := "turn-big"
	// Fill to just under cap.
	for range 10 {
		s.appendTurnOutputDelta(turnID, chunk)
	}
	// This append would push past 1 MiB → must be dropped + latched.
	s.appendTurnOutputDelta(turnID, chunk)
	// Any subsequent append after truncation is also ignored.
	s.appendTurnOutputDelta(turnID, "tail")

	merged, truncated := s.consumeTurnOutputAccumulator(turnID)
	if !truncated {
		t.Fatalf("expected truncated=true after cap exceeded")
	}
	if len(merged) > turnOutputAccumulatorMaxBytes {
		t.Fatalf("merged size %d exceeds cap %d", len(merged), turnOutputAccumulatorMaxBytes)
	}
	if strings.HasSuffix(merged, "tail") {
		t.Fatalf("tail delta should have been dropped after truncation latch")
	}
	if len(merged) != 10*chunkSize {
		t.Fatalf("expected pre-cap merged size %d, got %d", 10*chunkSize, len(merged))
	}
}

func TestTurnOutputAccumulator_PerTurnIsolationConcurrent(t *testing.T) {
	s := newAccumulatorTestSession()

	const turns = 16
	const deltasPerTurn = 32
	goroutines := newTestGoroutineGroup(t)
	for i := range turns {
		goroutines.Go(func() {
			turnID := "turn-" + string(rune('A'+i))
			for range deltasPerTurn {
				s.appendTurnOutputDelta(turnID, "d")
			}
		})
	}
	goroutines.Wait()

	for i := range turns {
		turnID := "turn-" + string(rune('A'+i))
		merged, truncated := s.consumeTurnOutputAccumulator(turnID)
		if truncated {
			t.Fatalf("turn %s unexpectedly truncated", turnID)
		}
		if len(merged) != deltasPerTurn {
			t.Fatalf("turn %s expected %d bytes, got %d", turnID, deltasPerTurn, len(merged))
		}
	}
	if len(s.turnOutputAccumulator) != 0 {
		t.Fatalf("expected accumulator drained, got %d entries", len(s.turnOutputAccumulator))
	}
}

func TestTurnOutputAccumulator_NilSafe(t *testing.T) {
	var s *session
	// All three methods must tolerate nil receiver for defensive callers.
	s.appendTurnOutputDelta("t", "x")
	if got, trunc := s.consumeTurnOutputAccumulator("t"); got != "" || trunc {
		t.Fatalf("nil session should return zero values")
	}
	s.dropTurnOutputAccumulator("t")
}
