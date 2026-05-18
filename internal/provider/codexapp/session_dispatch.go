package codexapp

import (
	"encoding/json"
	"errors"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func (s *session) dispatch(raw dto.RawProviderEvent) {
	if s.dispatcher == nil {
		pkglogger.Warn("codexapp: dispatch skipped: no dispatcher",
			"agent_id", s.agentID, "event_type", raw.EventType)
		return
	}
	payload := decodeAnyPayload(raw.Data)
	if len(payload) > 0 {
		if agentID := strings.TrimSpace(s.agentID); agentID != "" {
			s.remapEventIdentity(raw.EventType, payload, agentID)
			raw.Data = payload
		}
	}
	s.dispatcher.Dispatch(raw)
}

func (s *session) remapEventIdentity(eventType string, payload map[string]any, hostAgentID string) {
	pid := payloadAgentID(payload)
	tid := payloadThreadID(payload)
	// Always forcefully remap both agent identity fields and thread identity
	// fields to the host's public agentID. Codex/claudecli uses transient
	// providerThreadIDs (UUIDs) which cause duplicate phantom cards in the UI.
	if pid != "" && pid != hostAgentID && s.logger != nil {
		s.logger.Warn("codexapp: remapped alien agent_id in event",
			"event_type", eventType, "original", pid, "mapped", hostAgentID)
	}
	if tid != "" && tid != hostAgentID && s.logger != nil {
		s.logger.Warn("codexapp: remapped alien thread_id in event",
			"event_type", eventType, "original", tid, "mapped", hostAgentID)
	}
	payload["agentId"] = hostAgentID
	payload["agent_id"] = hostAgentID
	payload["threadId"] = hostAgentID
	payload["thread_id"] = hostAgentID
}

func (s *session) finishTurn(params json.RawMessage, optimistic bool) {
	payload := decodeEventPayload(params)
	turnID := payloadTurnID(payload)
	if turnID == "" {
		return
	}
	h := s.takeTurn(turnID)
	if h == nil {
		return
	}
	errText := strings.TrimSpace(shared.FirstNonEmpty(
		stringValue(payload, "error", "message", "reason"),
		stringValue(nestedValue(payload, "error"), "message"),
	))
	if errText == "" && optimistic {
		h.complete(nil)
		return
	}
	if errText == "" {
		errText = "turn failed"
	}
	h.complete(errors.New(errText))
}

func (s *session) takeTurn(turnID string) *turnHandle {
	s.mu.Lock()
	if turnID == "" {
		turnID = s.activeTurnID
	}
	h := s.turns[turnID]
	delete(s.turns, turnID)
	if turnID == s.activeTurnID {
		s.activeTurnID = ""
	}
	if s.pendingTurn != nil && s.pendingTurn.handle == h {
		s.pendingTurn = nil
	}
	s.mu.Unlock()
	// ADR-015 v4.1 §2.1 cleanup hook: drop the per-turn output accumulator
	// outside s.mu (accumulator uses its own accumulatorMu to avoid nested
	// locking).
	s.dropTurnOutputAccumulator(turnID)
	return h
}

func (s *session) forceCompleteTargetTurnID(providerID string) (string, bool) {
	if s == nil {
		return "", false
	}
	requested := strings.TrimSpace(providerID)
	s.mu.Lock()
	defer s.mu.Unlock()
	active := strings.TrimSpace(s.activeTurnID)
	if requested != "" {
		return requested, requested == active
	}
	return active, active != ""
}

func (s *session) forceCompleteTurn(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	s.suppressTurn(turnID)
	s.dispatch(dto.RawProviderEvent{EventType: "turn/completed", Data: map[string]any{
		"turnId":  turnID,
		"success": true,
		"status":  "completed",
		"reason":  "force_complete",
	}})
	if h := s.takeTurn(turnID); h != nil {
		h.complete(nil)
	}
}

func (s *session) shouldSuppressTurnEvent(method string, params json.RawMessage) bool {
	if !isTurnTerminalEvent(method) {
		return false
	}
	turnID := payloadTurnID(decodeEventPayload(params))
	return s.consumeSuppressedTurn(turnID)
}

func (s *session) suppressTurn(turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suppressed[turnID] = struct{}{}
}

func (s *session) consumeSuppressedTurn(turnID string) bool {
	if turnID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.suppressed[turnID]; !ok {
		return false
	}
	delete(s.suppressed, turnID)
	return true
}

func (s *session) failTurns(err error) {
	s.mu.Lock()
	turns := s.turns
	s.turns = map[string]*turnHandle{}
	s.activeTurnID = ""
	s.pendingTurn = nil
	s.mu.Unlock()
	// ADR-015 v4.1 §2.1 cleanup hook: drop accumulators for every aborted
	// turn so shutdownSession does not leak per-turn buffers. Use the keys
	// (provider IDs) we just removed from s.turns.
	for providerID, h := range turns {
		s.dropTurnOutputAccumulator(providerID)
		h.complete(err)
	}
}
