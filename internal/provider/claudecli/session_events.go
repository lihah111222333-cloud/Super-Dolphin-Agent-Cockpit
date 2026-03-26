package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func (s *session) startReadLoop(tr *transport) {
	platformshared.SafeGo(s.logger, func() {
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
		s.setResolvedThreadIDForTransport(tr, dataString(raw.Data, "session_id", "thread_id"))
	}
	if !s.isCurrentTransport(tr) {
		return
	}
	if s.shouldSuppressTurn(raw) {
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
	handle := s.activeTurn
	s.activeTurn = nil
	s.mu.Unlock()
	if handle == nil {
		return
	}
	turnID := currentTurnID(handle)
	handle.finish(finishErr)
	s.dispatch(s.turnRawEvent("turn:complete", turnID, map[string]any{
		"success": false,
		"error":   finishErr.Error(),
	}))
}

func (s *session) finishTurnFromRaw(raw dto.RawProviderEvent) {
	s.mu.Lock()
	handle := s.activeTurn
	s.activeTurn = nil
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

type assistantMessage struct {
	Content []contentBlock `json:"content"`
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
	var msg assistantMessage
	if len(raw.Message) > 0 {
		if err := json.Unmarshal(raw.Message, &msg); err != nil {
			return nil, err
		}
	}
	out := make([]dto.RawProviderEvent, 0, len(msg.Content))
	for _, block := range msg.Content {
		data := baseData(base, raw.SessionID, raw.Timestamp)
		switch strings.TrimSpace(block.Type) {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				data["stream"], data["delta"] = "message", block.Text
				out = append(out, dto.RawProviderEvent{EventType: "assistant:message_delta", Data: data})
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				data["stream"], data["delta"] = "reasoning", block.Thinking
				out = append(out, dto.RawProviderEvent{EventType: "assistant:message_delta", Data: data})
			}
		case "tool_use":
			data["call_id"] = strings.TrimSpace(block.ID)
			data["tool_name"] = strings.TrimSpace(block.Name)
			data["arguments_preview"] = strings.TrimSpace(string(block.Input))
			out = append(out, dto.RawProviderEvent{EventType: "tool:use_begin", Data: data})
		}
	}
	return out, nil
}

func decodeUserEvent(raw streamEvent, base rawBase) ([]dto.RawProviderEvent, error) {
	var msg struct {
		Content []map[string]any `json:"content"`
	}
	if len(raw.Message) > 0 {
		if err := json.Unmarshal(raw.Message, &msg); err != nil {
			return nil, err
		}
	}
	out := make([]dto.RawProviderEvent, 0, len(msg.Content))
	for _, block := range msg.Content {
		if dataString(block, "type") != "tool_result" {
			continue
		}
		data := baseData(base, raw.SessionID, raw.Timestamp)
		data["call_id"] = dataString(block, "tool_use_id")
		data["tool_name"] = dataString(block, "tool_name")
		data["success"] = true
		out = append(out, dto.RawProviderEvent{EventType: "tool:use_end", Data: data})
	}
	return out, nil
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
		errMsg := strings.TrimSpace(firstNonEmpty(raw.Result, raw.StopReason))
		if errMsg == "" {
			errMsg = "claude result error"
		}
		data["error"] = errMsg
	}
	return []dto.RawProviderEvent{{EventType: "turn:complete", Data: data}}
}

func baseData(base rawBase, sessionID, timestamp string) map[string]any {
	threadID := strings.TrimSpace(base.ThreadID)
	sessionID = strings.TrimSpace(firstNonEmpty(sessionID, threadID))
	data := map[string]any{
		"agent_id":   strings.TrimSpace(base.AgentID),
		"thread_id":  threadID,
		"session_id": sessionID,
		"turn_id":    strings.TrimSpace(base.TurnID),
	}
	if timestamp = strings.TrimSpace(timestamp); timestamp != "" {
		data["timestamp"] = timestamp
	}
	return data
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
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
