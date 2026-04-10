package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func (s *session) startReadLoop(tr *transport) {
	shared.SafeGo(s.logger, func() {
		for {
			line, err := tr.Receive()
			if err != nil {
				s.handleReceiveExit(tr, err)
				return
			}
			rawEvents, err := decodeClaudeLine(line, s.rawBase())
			if err != nil {
				s.logger.Debug("claudecli: decode line failed", "error", err)
				continue
			}
			for _, raw := range rawEvents {
				s.applyRaw(tr, raw)
			}
		}
	})
}

func (s *session) rawBase() rawBase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rawBase{
		AgentID:   s.agentID,
		ThreadID:  s.eventThreadIDLocked(),
		SessionID: s.sessionID,
		TurnID:    currentTurnID(s.activeTurn),
		CWD:       s.cwd,
		Model:     s.model,
	}
}

func (s *session) applyRaw(tr *transport, raw dto.RawProviderEvent) {
	if raw.EventType == "system:init" {
		resolvedID := dataString(raw.Data, "session_id", "thread_id")
		prevID := s.ThreadID()
		s.setResolvedThreadIDForTransport(tr, resolvedID)
		// When system:init resolves a real UUID that differs from the
		// initial placeholder, re-dispatch an agent:launched event so
		// downstream subscribers (thread module, UI projector) can update
		// the binding session_uuid and provider_thread_id.
		if newID := s.ThreadID(); newID != prevID && newID != "" {
			s.dispatch(dto.RawProviderEvent{
				EventType: "agent:launched",
				Data: map[string]any{
					"agent_id":   s.agentID,
					"thread_id":  s.EventThreadID(),
					"session_id": newID,
					"cwd":        s.cwd,
					"model":      s.model,
				},
			})
		}
	}
	if !s.isCurrentTransport(tr) {
		if s.logger != nil {
			s.logger.Warn("claudecli: applyRaw: transport mismatch, event dropped",
				"event_type", raw.EventType,
				"thread_id", dataString(raw.Data, "thread_id"),
				"agent_id", s.agentID,
			)
		}
		return
	}
	if s.shouldSuppressTurn(raw) {
		if s.logger != nil {
			s.logger.Warn("claudecli: applyRaw: turn suppressed",
				"event_type", raw.EventType,
				"thread_id", dataString(raw.Data, "thread_id"),
				"turn_id", dataString(raw.Data, "turn_id"),
				"agent_id", s.agentID,
			)
		}
		return
	}
	s.dispatch(raw)
	if raw.EventType == "turn:complete" || raw.EventType == "turn:interrupted" {
		s.finishTurnFromRaw(raw)
	}
}

func (s *session) isCurrentTransport(tr *transport) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport == tr
}

func (s *session) handleReceiveExit(tr *transport, err error) {
	finishErr := err
	if finishErr == nil || errors.Is(finishErr, io.EOF) {
		finishErr = io.EOF
	}
	s.mu.Lock()
	if s.transport != tr {
		s.mu.Unlock()
		return
	}
	handle := s.takeActiveTurnLocked()
	s.mu.Unlock()
	s.finishTurnWithError(handle, finishErr)
}

func (s *session) finishTurnFromRaw(raw dto.RawProviderEvent) {
	s.mu.Lock()
	handle := s.takeActiveTurnLocked()
	s.mu.Unlock()
	if handle == nil {
		return
	}
	if raw.EventType == "turn:interrupted" {
		handle.finish(context.Canceled)
		return
	}
	if dataBool(raw.Data, "success") {
		handle.finish(nil)
		return
	}
	handle.finish(errors.New(dataString(raw.Data, "error")))
}

func (s *session) shouldSuppressTurn(raw dto.RawProviderEvent) bool {
	switch raw.EventType {
	case "turn:complete", "turn:interrupted":
		return s.consumeSuppressedTurn(dataString(raw.Data, "turn_id"))
	default:
		return false
	}
}

type rawBase struct {
	AgentID   string
	ThreadID  string
	SessionID string
	TurnID    string
	CWD       string
	Model     string
}

type streamEvent struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype"`
	Message    json.RawMessage `json:"message"`
	SessionID  string          `json:"session_id"`
	Timestamp  string          `json:"timestamp"`
	Result     string          `json:"result"`
	StopReason string          `json:"stop_reason"`
	IsError    bool            `json:"is_error"`
}

type contentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Input    json.RawMessage `json:"input"`
}

func decodeClaudeLine(line []byte, base rawBase) ([]dto.RawProviderEvent, error) {
	var raw streamEvent
	if err := json.Unmarshal(line, &raw); err != nil {
		return nil, err
	}
	switch strings.TrimSpace(raw.Type) {
	case "system":
		return decodeSystemEvent(raw, base), nil
	case "assistant":
		return decodeAssistantEvent(raw, base)
	case "user":
		return decodeUserEvent(raw, base)
	case "result":
		return decodeResultEvent(raw, base), nil
	default:
		return nil, nil
	}
}

func decodeSystemEvent(raw streamEvent, base rawBase) []dto.RawProviderEvent {
	data := baseData(base, raw.SessionID, raw.Timestamp)
	data["cwd"] = base.CWD
	data["model"] = base.Model
	return []dto.RawProviderEvent{{EventType: "system:" + strings.TrimSpace(raw.Subtype), Data: data}}
}

func decodeAssistantEvent(raw streamEvent, base rawBase) ([]dto.RawProviderEvent, error) {
	return decodeMessageEvents(raw, base, "assistant")
}

func decodeUserEvent(raw streamEvent, base rawBase) ([]dto.RawProviderEvent, error) {
	return decodeMessageEvents(raw, base, "user")
}

func decodeResultEvent(raw streamEvent, base rawBase) []dto.RawProviderEvent {
	data := baseData(base, raw.SessionID, raw.Timestamp)
	success := !raw.IsError && !strings.EqualFold(strings.TrimSpace(raw.Subtype), "error")
	data["success"] = success
	if success {
		if result := strings.TrimSpace(raw.Result); result != "" {
			data["result"] = result
			data["summary"] = result
			data["message"] = result
		}
		if stopReason := strings.TrimSpace(raw.StopReason); stopReason != "" {
			data["stop_reason"] = stopReason
		}
	} else {
		errMsg := strings.TrimSpace(shared.FirstNonEmpty(raw.Result, raw.StopReason))
		if errMsg == "" {
			errMsg = "claude result error"
		}
		data["error"] = errMsg
	}
	return []dto.RawProviderEvent{{EventType: "turn:complete", Data: data}}
}

func baseData(base rawBase, sessionID, timestamp string) map[string]any {
	return buildEventData(base, sessionID, timestamp, nil)
}

func dataString(data any, keys ...string) string {
	if m, ok := data.(map[string]any); ok {
		for _, key := range keys {
			if text, ok := m[key].(string); ok {
				return strings.TrimSpace(text)
			}
		}
		return ""
	}
	if m, ok := data.(map[string]string); ok {
		for _, key := range keys {
			if text := strings.TrimSpace(m[key]); text != "" {
				return text
			}
		}
	}
	return ""
}

func dataBool(data any, key string) bool {
	m, _ := data.(map[string]any)
	value, _ := m[key].(bool)
	return value
}
