package claudecli

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
	return s.rawBaseLocked()
}
func (s *session) rawBaseLocked() rawBase {
	return rawBase{
		AgentID:   s.agentID,
		ThreadID:  s.eventThreadIDLocked(),
		SessionID: s.sessionID,
		TurnID:    currentTurnID(s.activeTurn),
		CWD:       s.cwd,
		Model:     s.currentTransportModelLocked(),
	}
}
func (s *session) turnRawEventLocked(eventType, turnID string, extras map[string]any) dto.RawProviderEvent {
	base := s.rawBaseLocked()
	data := buildEventData(base, base.SessionID, time.Now().Format(time.RFC3339Nano), extras)
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		data["turn_id"] = turnID
	}
	return dto.RawProviderEvent{EventType: eventType, Data: data}
}
func (s *session) dispatch(raw dto.RawProviderEvent) {
	if s.eventDispatcher != nil {
		s.eventDispatcher.Dispatch(raw)
	}
}
func (s *session) turnRawEvent(eventType, turnID string, extras map[string]any) dto.RawProviderEvent {
	base := s.rawBase()
	data := buildEventData(base, base.SessionID, time.Now().Format(time.RFC3339Nano), extras)
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		data["turn_id"] = turnID
	}
	return dto.RawProviderEvent{EventType: eventType, Data: data}
}
func currentTurnID(handle *turnHandle) string {
	if handle == nil {
		return ""
	}
	if providerID := handle.ProviderID(); providerID != "" {
		return providerID
	}
	return handle.LocalID()
}
func (s *session) applyRaw(tr *transport, raw dto.RawProviderEvent) {
	s.handleSystemInitRaw(tr, raw)
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
	if s.isSilentTurn(raw) {
		if shouldFinishTurnRaw(raw) {
			s.finishSilentTurn(raw)
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
	s.trackToolEvent(raw)
	s.dispatchToolInterruptEvents(raw)
	if shouldFinishTurnRaw(raw) && s.shouldRetryTransientError(raw) {
		return
	}
	s.dispatch(raw)
	if shouldFinishTurnRaw(raw) {
		s.finishTurnFromRaw(raw)
	}
}
func (s *session) handleSystemInitRaw(tr *transport, raw dto.RawProviderEvent) {
	if raw.EventType != "system:init" {
		return
	}
	if contextWindow, ok := dataInt(raw.Data, "context_window", "contextWindow", "context_window_tokens", "contextWindowTokens"); ok {
		s.setContextWindowForTransport(tr, contextWindow)
	}
	resolvedID := dataString(raw.Data, "session_id", "thread_id")
	prevID := s.ThreadID()
	s.setResolvedThreadIDForTransport(tr, resolvedID)
	if s.isCurrentTransport(tr) {
		shared.SafeGo(s.logger, func() {
			s.startLogWatcherIfCurrent(tr)
		})
	}
	if newID := s.ThreadID(); newID != "" {
		eventThreadID := s.EventThreadID()
		if newID != prevID || eventThreadID != newID {
			s.dispatch(dto.RawProviderEvent{
				EventType: "agent:launched",
				Data: map[string]any{
					"agent_id":   s.agentID,
					"thread_id":  eventThreadID,
					"session_id": newID,
					"cwd":        s.cwd,
					"model":      s.currentTransportModel(),
				},
			})
		}
	}
}
func (s *session) dispatchToolInterruptEvents(raw dto.RawProviderEvent) {
	if raw.EventType != "turn:interrupted" {
		return
	}
	for _, event := range s.takeActiveToolInterruptEvents(dataString(raw.Data, "turn_id"), "provider_interrupt") {
		s.dispatch(event)
	}
}
func shouldFinishTurnRaw(raw dto.RawProviderEvent) bool {
	return raw.EventType == "turn:complete" || raw.EventType == "turn:interrupted"
}
func (s *session) isCurrentTransport(tr *transport) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transport == tr
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
	return (raw.EventType == "turn:complete" || raw.EventType == "turn:interrupted") &&
		s.consumeSuppressedTurn(dataString(raw.Data, "turn_id"))
}
func (s *session) trackToolEvent(raw dto.RawProviderEvent) {
	callID := strings.TrimSpace(dataString(raw.Data, "call_id"))
	if callID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch raw.EventType {
	case "tool:use_begin":
		if s.activeToolCalls == nil {
			s.activeToolCalls = map[string]string{}
		}
		s.activeToolCalls[callID] = strings.TrimSpace(dataString(raw.Data, "tool_name"))
	case "tool:use_end":
		delete(s.activeToolCalls, callID)
	}
}
func (s *session) takeActiveToolInterruptEvents(turnID, reason string) []dto.RawProviderEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takeActiveToolInterruptEventsLocked(turnID, reason)
}
func (s *session) takeActiveToolInterruptEventsLocked(turnID, reason string) []dto.RawProviderEvent {
	if len(s.activeToolCalls) == 0 {
		return nil
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		turnID = currentTurnID(s.activeTurn)
	}
	errText := "tool interrupted by turn interrupt"
	if reason = strings.TrimSpace(reason); reason != "" {
		errText += ": " + reason
	}
	timestamp := time.Now().Format(time.RFC3339Nano)
	callIDs := make([]string, 0, len(s.activeToolCalls))
	for callID := range s.activeToolCalls {
		callIDs = append(callIDs, callID)
	}
	sort.Strings(callIDs)
	events := make([]dto.RawProviderEvent, 0, len(callIDs))
	for _, callID := range callIDs {
		events = append(events, dto.RawProviderEvent{EventType: "tool:use_end", Data: map[string]any{
			"timestamp":  timestamp,
			"agent_id":   s.agentID,
			"thread_id":  s.eventThreadIDLocked(),
			"session_id": s.sessionID,
			"turn_id":    turnID,
			"call_id":    callID,
			"tool_name":  s.activeToolCalls[callID],
			"success":    false,
			"error":      errText,
		}})
	}
	s.activeToolCalls = nil
	return events
}

type rawBase struct {
	AgentID, ThreadID, SessionID, TurnID, CWD, Model string
}
type streamEvent struct {
	Type           string          `json:"type"`
	Subtype        string          `json:"subtype"`
	Message        json.RawMessage `json:"message"`
	SessionID      string          `json:"session_id"`
	Timestamp      string          `json:"timestamp"`
	Result         string          `json:"result"`
	StopReason     string          `json:"stop_reason"`
	TerminalReason string          `json:"terminal_reason"`
	IsError        bool            `json:"is_error"`
	Error          json.RawMessage `json:"error"`
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
	t := strings.TrimSpace(raw.Type)
	switch t {
	case "system":
		return decodeSystemEvent(raw, base), nil
	case "assistant":
		return decodeMessageEvents(raw, base, "assistant")
	case "user":
		pkglogger.Get().Warn("claudecli: user event received", "line", string(line))
		return decodeMessageEvents(raw, base, "user")
	case "result":
		return decodeResultEvent(raw, base), nil
	case "rate_limit_event":
		pkglogger.Get().Info("claudecli: rate_limit_event received", "agent_id", base.AgentID, "session_id", raw.SessionID)
		return nil, nil
	default:
		// Attempt to parse out streaming deltas to not break streaming
		// Log the unparsed events to confirm if they contain the real deltas!
		pkglogger.Get().Warn("claudecli: unknown stream event type dropped", "raw_type", t, "line", string(line))
		return nil, nil
	}
}
func decodeSystemEvent(raw streamEvent, base rawBase) []dto.RawProviderEvent {
	data := baseData(base, raw.SessionID, raw.Timestamp)
	data["cwd"] = base.CWD
	data["model"] = base.Model
	return []dto.RawProviderEvent{{EventType: "system:" + strings.TrimSpace(raw.Subtype), Data: data}}
}
func decodeResultEvent(raw streamEvent, base rawBase) []dto.RawProviderEvent {
	data := baseData(base, raw.SessionID, raw.Timestamp)
	success := !raw.IsError && !strings.EqualFold(strings.TrimSpace(raw.Subtype), "error")
	terminalReason := strings.TrimSpace(raw.TerminalReason)
	data["success"] = success
	if terminalReason != "" {
		data["terminal_reason"] = terminalReason
	}
	if success {
		if r := strings.TrimSpace(raw.Result); r != "" {
			data["result"] = r
			data["summary"] = r
			data["message"] = r
		}
		if sr := strings.TrimSpace(raw.StopReason); sr != "" {
			data["stop_reason"] = sr
		}
	} else {
		errStr := strings.TrimSpace(shared.FirstNonEmpty(raw.Result, raw.StopReason))
		var objReq struct {
			Message string `json:"message"`
		}
		var plainStr string
		_ = json.Unmarshal(raw.Error, &objReq)
		_ = json.Unmarshal(raw.Error, &plainStr)
		errStr = strings.TrimSpace(shared.FirstNonEmpty(errStr, objReq.Message, plainStr))
		if errStr == "" {
			errStr = errorMessageFromTerminalReason(terminalReason)
			pkglogger.Get().Warn("claudecli: stream error result missing message", "agent_id", base.AgentID, "terminal_reason", terminalReason, "raw_error", string(raw.Error), "raw_message", string(raw.Message))
		}
		data["error"] = errStr
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
func dataInt(data any, keys ...string) (int, bool) {
	m, _ := data.(map[string]any)
	for _, key := range keys {
		value, ok := m[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed), true
		case int:
			return typed, true
		case int64:
			return int(typed), true
		case json.Number:
			parsed, err := typed.Int64()
			return int(parsed), err == nil
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			return parsed, err == nil
		}
	}
	return 0, false
}
