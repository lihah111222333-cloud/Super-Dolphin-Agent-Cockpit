package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func (s *session) startReadLoop(tr *transport) {
	go func() {
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
				s.applyRaw(raw)
			}
		}
	}()
}

func (s *session) rawBase() rawBase {
	s.mu.Lock()
	defer s.mu.Unlock()
	return rawBase{
		AgentID:   s.agentID,
		ThreadID:  s.threadID,
		SessionID: s.sessionID,
		TurnID:    currentTurnID(s.activeTurn),
		CWD:       s.cwd,
		Model:     s.model,
	}
}

func (s *session) applyRaw(raw dto.RawProviderEvent) {
	if raw.Type == "system:init" {
		if threadID := dataString(raw.Data, "thread_id", "session_id"); threadID != "" {
			s.mu.Lock()
			s.threadID = threadID
			s.sessionID = threadID
			s.mu.Unlock()
		}
	}
	s.dispatch(raw)
	if raw.Type == "turn:complete" || raw.Type == "turn:interrupted" {
		s.finishTurnFromRaw(raw)
	}
}

func (s *session) handleReceiveExit(tr *transport, err error) {
	if err == nil || errors.Is(err, io.EOF) {
		return
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
	handle.finish(err)
	s.dispatch(s.turnRawEvent("turn:complete", turnID, map[string]any{
		"success": false,
		"error":   err.Error(),
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
	if raw.Type == "turn:interrupted" {
		handle.finish(context.Canceled)
		return
	}
	if dataBool(raw.Data, "success") {
		handle.finish(nil)
		return
	}
	handle.finish(errors.New(dataString(raw.Data, "error")))
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
	data := baseData(base, raw.SessionID)
	data["cwd"] = base.CWD
	data["model"] = base.Model
	return []dto.RawProviderEvent{{Type: "system:" + strings.TrimSpace(raw.Subtype), Data: data}}
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
		data := baseData(base, raw.SessionID)
		switch strings.TrimSpace(block.Type) {
		case "text":
			if strings.TrimSpace(block.Text) != "" {
				data["stream"], data["delta"] = "message", block.Text
				out = append(out, dto.RawProviderEvent{Type: "assistant:message_delta", Data: data})
			}
		case "thinking":
			if strings.TrimSpace(block.Thinking) != "" {
				data["stream"], data["delta"] = "reasoning", block.Thinking
				out = append(out, dto.RawProviderEvent{Type: "assistant:message_delta", Data: data})
			}
		case "tool_use":
			data["call_id"] = strings.TrimSpace(block.ID)
			data["tool_name"] = strings.TrimSpace(block.Name)
			data["arguments_preview"] = strings.TrimSpace(string(block.Input))
			out = append(out, dto.RawProviderEvent{Type: "tool:use_begin", Data: data})
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
		data := baseData(base, raw.SessionID)
		data["call_id"] = dataString(block, "tool_use_id")
		data["tool_name"] = dataString(block, "tool_name")
		data["success"] = true
		out = append(out, dto.RawProviderEvent{Type: "tool:use_end", Data: data})
	}
	return out, nil
}

func decodeResultEvent(raw streamEvent, base rawBase) []dto.RawProviderEvent {
	data := baseData(base, raw.SessionID)
	data["success"] = !raw.IsError && !strings.EqualFold(strings.TrimSpace(raw.Subtype), "error")
	data["error"] = strings.TrimSpace(firstNonEmpty(raw.Result, raw.StopReason))
	return []dto.RawProviderEvent{{Type: "turn:complete", Data: data}}
}

func baseData(base rawBase, sessionID string) map[string]any {
	threadID := strings.TrimSpace(firstNonEmpty(sessionID, base.ThreadID))
	return map[string]any{
		"agent_id":   strings.TrimSpace(base.AgentID),
		"thread_id":  threadID,
		"session_id": threadID,
		"turn_id":    strings.TrimSpace(base.TurnID),
	}
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
