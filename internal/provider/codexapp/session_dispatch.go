package codexapp

import (
	"encoding/json"
	"errors"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/supportutil"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	maxSuppressedToolEnds = 256
	maxRolloutToolNames   = 256
)

// dispatch 派发codexapp provider。
func (s *session) dispatch(raw dto.RawProviderEvent) {
	if s.dispatcher == nil {
		pkglogger.Warn("codexapp: dispatch skipped: no dispatcher",
			"agent_id", s.agentID, "event_type", raw.EventType)
		return
	}
	payload := decodeAnyPayload(raw.Data)
	payloadChanged := false
	if len(payload) > 0 {
		if agentID := strings.TrimSpace(s.agentID); agentID != "" {
			s.remapEventIdentity(raw.EventType, payload, agentID)
			payloadChanged = true
		}
		if s.trackCodexRolloutToolName(raw.EventType, payload) {
			payloadChanged = true
		}
		if payloadChanged {
			raw.Data = payload
		}
	}
	s.dispatcher.Dispatch(raw)
}

// remapEventIdentity 处理remap事件身份。
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

// finishTurn 处理finishturn。
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
	if notice := supportutil.CodexModelUnsupportedNotice(errors.New(errText), s.runtimeConfigString("model")); notice != "" {
		errText = notice
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
	s.completeSyntheticTurn(turnID, "force_complete", "")
}

func (s *session) completeSyntheticTurn(turnID, reason, result string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	s.suppressTurn(turnID)
	payload := map[string]any{"turnId": turnID, "success": true, "status": "completed", "reason": strings.TrimSpace(reason)}
	if result = strings.TrimSpace(result); result != "" {
		payload["result"] = result
	}
	s.dispatch(dto.RawProviderEvent{EventType: "turn/completed", Data: payload})
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

func (s *session) shouldSuppressToolEndEvent(method string, params json.RawMessage) bool {
	if !isToolEndEvent(method) {
		return false
	}
	payload := decodeEventPayload(params)
	turnID, callID, toolName, ok := s.toolEndSuppressionPayload(method, payload)
	if !ok {
		return false
	}
	if !s.consumeSuppressedToolEnd(turnID, callID, toolName) {
		return false
	}
	s.suppressToolEnd("", callID, toolName)
	return true
}

func isToolEndEvent(method string) bool {
	switch strings.TrimSpace(method) {
	case "item/completed", "item_completed", "agent/event/item_completed", "rawResponseItem/completed", "tool.call.end", "event_msg", "response_item":
		return true
	default:
		return false
	}
}

// toolEndSuppressionPayload 处理工具endsuppression载荷。
func (s *session) toolEndSuppressionPayload(method string, payload map[string]any) (string, string, string, bool) {
	item := codexToolItemPayload(payload)
	switch strings.TrimSpace(stringValue(item, "type")) {
	case "mcp_tool_call_end", "tool_result", "function_call_output":
	default:
		if strings.TrimSpace(method) == "response_item" {
			return "", "", "", false
		}
		if looksLikeToolCall(payload) {
			return payloadTurnID(payload), payloadCallID(payload), payloadToolName(payload), true
		}
		return "", "", "", false
	}
	callID := shared.FirstNonEmpty(payloadCallID(payload), payloadCallID(item))
	toolName := s.rolloutEndToolName(callID, item)
	if callID == "" || toolName == "" {
		return "", "", "", false
	}
	return shared.FirstNonEmpty(payloadTurnID(payload), payloadTurnID(item)), callID, toolName, true
}

func (s *session) suppressTurn(turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.suppressed[turnID] = struct{}{}
}

func (s *session) suppressToolEnd(turnID, callID, toolName string) {
	key := toolEndSuppressionKey(turnID, callID, toolName)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.suppressedToolEnds == nil {
		s.suppressedToolEnds = map[string]struct{}{}
	}
	if _, exists := s.suppressedToolEnds[key]; exists {
		return
	}
	s.suppressedToolEnds[key] = struct{}{}
	s.suppressedToolOrder = append(s.suppressedToolOrder, key)
	for len(s.suppressedToolOrder) > maxSuppressedToolEnds {
		oldest := s.suppressedToolOrder[0]
		s.suppressedToolOrder = s.suppressedToolOrder[1:]
		delete(s.suppressedToolEnds, oldest)
	}
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

// consumeSuppressedToolEnd 处理consumesuppressed工具end。
func (s *session) consumeSuppressedToolEnd(turnID, callID, toolName string) bool {
	callID = strings.TrimSpace(callID)
	names := toolEndSuppressionToolNames(toolName)
	if callID == "" || len(names) == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.consumeSuppressedToolEndExactLocked(turnID, callID, names) {
		return true
	}
	if s.consumeSuppressedToolEndExactLocked("", callID, names) {
		return true
	}
	if strings.TrimSpace(turnID) == "" {
		return s.consumeSuppressedToolEndAnyTurnLocked(callID, names)
	}
	return false
}

func (s *session) consumeSuppressedToolEndExactLocked(turnID, callID string, names []string) bool {
	for _, name := range names {
		key := toolEndSuppressionKey(turnID, callID, name)
		if key == "" {
			continue
		}
		if _, ok := s.suppressedToolEnds[key]; ok {
			delete(s.suppressedToolEnds, key)
			return true
		}
	}
	return false
}

func (s *session) consumeSuppressedToolEndAnyTurnLocked(callID string, names []string) bool {
	for key := range s.suppressedToolEnds {
		parts := strings.Split(key, "\x00")
		if len(parts) == 3 && parts[1] == callID && stringInSet(parts[2], names) {
			delete(s.suppressedToolEnds, key)
			return true
		}
	}
	return false
}

func stringInSet(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}

func toolEndSuppressionToolNames(toolName string) []string {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" {
		return nil
	}
	names := []string{toolName}
	if short := shortMCPToolName(toolName); short != "" && short != toolName {
		names = append(names, short)
	}
	return names
}

func shortMCPToolName(toolName string) string {
	parts := strings.Split(strings.TrimSpace(toolName), "__")
	if len(parts) < 3 || !strings.EqualFold(parts[0], "mcp") {
		return ""
	}
	return strings.Join(parts[2:], "__")
}

func toolEndSuppressionKey(turnID, callID, toolName string) string {
	turnID = strings.TrimSpace(turnID)
	callID = strings.TrimSpace(callID)
	toolName = strings.TrimSpace(toolName)
	if callID == "" || toolName == "" {
		return ""
	}
	return turnID + "\x00" + callID + "\x00" + toolName
}

// trackCodexRolloutToolName 跟踪codexrollout工具名称。
func (s *session) trackCodexRolloutToolName(eventType string, payload map[string]any) bool {
	if !isCodexRolloutToolEventType(eventType) {
		return false
	}
	item := codexToolItemPayload(payload)
	if len(item) == 0 {
		return false
	}
	switch strings.TrimSpace(stringValue(item, "type")) {
	case "function_call", "tool_call":
		header := buildCodexRolloutToolCallHeader(payload, item)
		if header.CallID == "" || header.ToolName == "" {
			return false
		}
		s.rememberRolloutToolName(header.CallID, header.ToolName)
		return false
	case "mcp_tool_call_end", "function_call_output", "tool_result":
		callID := shared.FirstNonEmpty(payloadCallID(payload), payloadCallID(item))
		if callID == "" {
			return false
		}
		toolName := s.rolloutEndToolName(callID, item)
		if toolName == "" {
			return false
		}
		item["toolName"] = toolName
		s.suppressToolEnd("", callID, toolName)
		return true
	default:
		return false
	}
}

func isCodexRolloutToolEventType(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response_item", "item/started", "item_started", "agent/event/item_started",
		"event_msg", "item/completed", "item_completed", "agent/event/item_completed", "rawResponseItem/completed":
		return true
	default:
		return false
	}
}

// rememberRolloutToolName 处理rememberrollout工具名称。
func (s *session) rememberRolloutToolName(callID, toolName string) {
	callID = strings.TrimSpace(callID)
	toolName = strings.TrimSpace(toolName)
	if callID == "" || toolName == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rolloutToolNames == nil {
		s.rolloutToolNames = map[string]string{}
	}
	if _, exists := s.rolloutToolNames[callID]; !exists {
		s.rolloutToolOrder = append(s.rolloutToolOrder, callID)
	}
	s.rolloutToolNames[callID] = toolName
	for len(s.rolloutToolOrder) > maxRolloutToolNames {
		oldest := s.rolloutToolOrder[0]
		s.rolloutToolOrder = s.rolloutToolOrder[1:]
		delete(s.rolloutToolNames, oldest)
	}
}

func (s *session) rolloutToolName(callID string) (string, bool) {
	callID = strings.TrimSpace(callID)
	if callID == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	toolName, ok := s.rolloutToolNames[callID]
	if !ok {
		return "", false
	}
	return toolName, true
}

func (s *session) rolloutEndToolName(callID string, item map[string]any) string {
	if s != nil {
		if toolName, ok := s.rolloutToolName(callID); ok {
			return toolName
		}
	}
	return codexRolloutToolName(item)
}

// toolEventEndOutcome 处理工具事件endoutcome。
func toolEventEndOutcome(eventType string, payload map[string]any) (bool, string) {
	success := turnTerminalSuccess(eventType, payload)
	errorText := stringValue(payload, "error", "message", "reason")
	if errorText != "" {
		success = false
	}
	if resultSuccess, resultError, ok := toolEventResultOutcome(payload["result"]); ok {
		if !resultSuccess {
			success = false
			if errorText == "" {
				errorText = resultError
			}
		}
	}
	return success, errorText
}

// toolEventResultOutcome 处理工具事件结果outcome。
func toolEventResultOutcome(result any) (bool, string, bool) {
	switch value := result.(type) {
	case nil, string, float64, bool, []any:
		return true, "", false
	case json.RawMessage:
		if !strings.HasPrefix(strings.TrimSpace(string(value)), "{") {
			return true, "", false
		}
	case []byte:
		if !strings.HasPrefix(strings.TrimSpace(string(value)), "{") {
			return true, "", false
		}
	case map[string]any, map[string]json.RawMessage:
	default:
		return true, "", false
	}
	success, errorText := toolCallEndOutcome(result, nil)
	if success {
		return true, "", true
	}
	return false, errorText, true
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
