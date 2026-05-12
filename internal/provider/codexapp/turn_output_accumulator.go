package codexapp

import (
	"strings"
)

// turnOutputAccumulatorMaxBytes is the per-turn hard cap on accumulated
// TurnOutputDelta payload bytes. When exceeded, additional deltas are dropped
// and the buffer is marked truncated (ADR-015 v4.1 §2.1 + §2.2 buffer cap).
const turnOutputAccumulatorMaxBytes = 1 << 20 // 1 MiB

// turnOutputBuffer holds per-turn accumulated message-stream deltas plus a
// truncation flag. The buffer is owned by session.turnOutputAccumulator and
// guarded by session.accumulatorMu.
type turnOutputBuffer struct {
	parts     []string
	size      int
	truncated bool
}

// appendTurnOutputDelta appends a TurnOutputDelta payload (stream="message")
// to the per-turn buffer. Empty turnID / delta are ignored. Once the buffer
// crosses the 1 MiB cap, further deltas are dropped and truncated=true is
// latched.
func (s *session) appendTurnOutputDelta(turnID, delta string) {
	if s == nil || turnID == "" || delta == "" {
		return
	}
	s.accumulatorMu.Lock()
	defer s.accumulatorMu.Unlock()
	if s.turnOutputAccumulator == nil {
		s.turnOutputAccumulator = map[string]*turnOutputBuffer{}
	}
	buf, ok := s.turnOutputAccumulator[turnID]
	if !ok {
		buf = &turnOutputBuffer{}
		s.turnOutputAccumulator[turnID] = buf
	}
	if buf.truncated {
		return
	}
	if buf.size+len(delta) > turnOutputAccumulatorMaxBytes {
		// Latch truncation; drop the over-cap delta entirely to keep the
		// cap a hard bound (callers receive a clear signal via the flag
		// rather than a partial last chunk).
		buf.truncated = true
		return
	}
	buf.parts = append(buf.parts, delta)
	buf.size += len(delta)
}

// consumeTurnOutputAccumulator merges and removes the buffer for turnID,
// returning the merged content and whether truncation occurred. Returns
// ("", false) when no buffer exists for the turn (no message-stream deltas
// were observed for this turn).
func (s *session) consumeTurnOutputAccumulator(turnID string) (string, bool) {
	if s == nil || turnID == "" {
		return "", false
	}
	s.accumulatorMu.Lock()
	defer s.accumulatorMu.Unlock()
	buf, ok := s.turnOutputAccumulator[turnID]
	if !ok {
		return "", false
	}
	delete(s.turnOutputAccumulator, turnID)
	if buf == nil {
		return "", false
	}
	return strings.Join(buf.parts, ""), buf.truncated
}

// dropTurnOutputAccumulator removes any pending buffer for turnID. Used by
// takeTurn / failTurns / applyReplayedTurn cleanup hooks to avoid buffer
// leaks (ADR-015 v4.1 §2.1 cleanup hooks).
func (s *session) dropTurnOutputAccumulator(turnID string) {
	if s == nil || turnID == "" {
		return
	}
	s.accumulatorMu.Lock()
	defer s.accumulatorMu.Unlock()
	delete(s.turnOutputAccumulator, turnID)
}
