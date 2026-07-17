package codexapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/supportutil"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	maxSuppressedToolEnds = 256
	maxRolloutToolNames   = 256
	turnToolFailurePrefix = "\x00turn_tool_failure\x00"
)

// dispatch 统一发出 Codex provider 事件，并在发送前修正宿主可见身份。
// Codex rollout 事件可能携带 provider 内部 UUID，这里会改写为宿主 agentID 以避免 UI 出现幽灵线程。
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
		if isTurnTerminalEvent(raw.EventType) && s.applyAcceptedInterruptRequest(raw.EventType, payload) {
			payloadChanged = true
		}
		s.recordToolFailureFromRaw(raw.EventType, payload)
		if payloadChanged {
			raw.Data = payload
		}
	}
	s.dispatcher.Dispatch(raw)
}

// remapEventIdentity 将 provider 事件中的 agent/thread 身份映射为宿主公开 ID。
// 只要发现外来 ID 就记录告警，便于排查 provider UUID 泄漏到 UI 的来源。
func (s *session) remapEventIdentity(eventType string, payload map[string]any, hostAgentID string) {
	pid := payloadAgentID(payload)
	tid := payloadThreadID(payload)
	// agent 与 thread 两套字段都强制改写，避免旧 payload 混用 snake/camel 字段时漏映射。
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

// finishTurn 根据 provider 终态事件关闭本地 turn handle。
// optimistic=true 表示外层已确认成功；否则会从 payload 提取错误文本并转成 handle error。
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

func (s *session) failMalformedTerminalNotification(method string) {
	h := s.takeTurn("")
	if h == nil {
		pkglogger.Warn("codexapp: malformed terminal notification without active turn",
			"agent_id", s.agentID,
			"method", method,
			"thread_id", s.ThreadID(),
		)
		return
	}

	turnID := strings.TrimSpace(h.ProviderID())
	errText := fmt.Sprintf("malformed terminal notification: method=%s turn_id=%s", method, turnID)
	if threadID := s.ThreadID(); threadID != "" {
		errText += " thread_id=" + threadID
	}
	pkglogger.Warn("codexapp: malformed terminal notification failed active turn",
		"agent_id", s.agentID,
		"method", method,
		"thread_id", s.ThreadID(),
		"turn_id", turnID,
	)
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
		s.setActiveTurnLocked("")
	}
	if s.pendingTurn != nil && s.pendingTurn.handle == h {
		s.pendingTurn = nil
	}
	s.mu.Unlock()
	// turn 结束后必须在 session 锁外清理输出累积器，避免与 accumulatorMu 形成嵌套锁。
	s.dropTurnOutputAccumulator(turnID)
	s.discardTurnToolFailures(turnID)
	return h
}

// applyAcceptedInterruptRequest 消费已接受的 Stop claim，仅把真实取消或中断终态归因给该请求。
func (s *session) applyAcceptedInterruptRequest(method string, payload map[string]any) bool {
	turnID := strings.TrimSpace(payloadTurnID(payload))
	if turnID == "" {
		return false
	}
	s.mu.Lock()
	requestID := strings.TrimSpace(s.interruptRequests[turnID])
	delete(s.interruptRequests, turnID)
	s.mu.Unlock()
	if requestID == "" {
		return false
	}
	outcome := resolveTurnTerminalOutcome(method, payload)
	if outcome.contractError != "" || (outcome.status != "interrupted" && outcome.status != "cancelled") {
		return false
	}
	payload["termination_cause"] = "user_request"
	payload["termination_request_id"] = requestID
	return true
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
	s.completeSyntheticTurn(turnID, "force_complete", "", nil)
}

// completeSyntheticTurn 在 Codex 只给出 assistant message 时合成 turn 终态。
// 若同一 active turn 已记录工具失败，终态必须标为 failed，避免 UI 误显示干净完成。
func (s *session) completeSyntheticTurn(turnID, reason, result string, acceptedItemIDs []string) {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	s.suppressTurn(turnID)
	failures := s.takeTurnToolFailures(turnID)
	success := len(failures) == 0
	status := "completed"
	if !success {
		status = "failed"
	}
	payload := map[string]any{"turnId": turnID, "success": success, "status": status, "reason": strings.TrimSpace(reason)}
	if len(acceptedItemIDs) > 0 {
		payload["accepted_partial_item_ids"] = slices.Clone(acceptedItemIDs)
	}
	if !success {
		payload["error"] = toolFailureSummary(failures)
		payload["tool_failure_count"] = len(failures)
	}
	result = strings.TrimSpace(result)
	if result == "" {
		result, _ = s.consumeTurnOutputAccumulator(turnID)
		result = strings.TrimSpace(result)
	}
	if result != "" {
		payload["result"] = result
	}
	s.dispatch(dto.RawProviderEvent{EventType: "turn/completed", Data: payload})
	if h := s.takeTurn(turnID); h != nil {
		if !success {
			h.complete(errors.New(toolFailureSummary(failures)))
			return
		}
		h.complete(nil)
	}
}

type turnToolFailure struct {
	TurnID   string
	CallID   string
	ToolName string
	Error    string
}

type toolEndTranslator func(map[string]any) (any, bool)

// recordToolFailureFromRaw 从 Codex 原始事件中提取工具失败并挂到当前 turn。
// 这里只记录失败关联，不改变事件分发本身，避免 rollout assistant completion 覆盖真实工具错误。
func (s *session) recordToolFailureFromRaw(eventType string, payload map[string]any) {
	if len(payload) == 0 {
		return
	}
	if eventType == "item/completed" || eventType == "tool.call.end" {
		s.recordDirectToolFailure(eventType, payload)
		return
	}
	if s.recordTranslatedToolFailure(payload, translateCodexMCPToolCallEnd) {
		return
	}
	s.recordTranslatedToolFailure(payload, translateCodexFunctionCallOutputEnd)
}

func (s *session) recordDirectToolFailure(eventType string, payload map[string]any) {
	if !looksLikeToolCall(payload) {
		return
	}
	header := buildToolCallHeader(payload)
	success, errorText := toolEventEndOutcome(eventType, payload)
	if !success {
		s.recordTurnToolFailure(header, errorText)
	}
}

func (s *session) recordTranslatedToolFailure(payload map[string]any, translate toolEndTranslator) bool {
	ev, ok := translate(payload)
	if !ok {
		return false
	}
	end, ok := ev.(tooldto.ToolCallEnd)
	if ok && !end.Success {
		s.recordTurnToolFailure(end.ToolCallHeader, end.Error)
	}
	return true
}

// recordTurnToolFailure 将工具失败写入 session-local turn 关联表。
// 它只接受仍在 active turns 中的 turn，避免迟到事件污染下一轮 synthetic completion。
func (s *session) recordTurnToolFailure(header shareddto.ToolCallHeader, errorText string) {
	if s == nil {
		return
	}
	turnID := strings.TrimSpace(header.TurnID)
	errorText = strings.TrimSpace(errorText)
	if errorText == "" {
		errorText = "tool call failed"
	}
	s.mu.Lock()
	if turnID == "" {
		turnID = strings.TrimSpace(s.activeTurnID)
	}
	if turnID == "" || s.turns[turnID] == nil {
		s.mu.Unlock()
		return
	}
	if s.suppressed == nil {
		s.suppressed = map[string]struct{}{}
	}
	s.suppressed[encodeTurnToolFailure(turnToolFailure{
		TurnID:   turnID,
		CallID:   strings.TrimSpace(header.CallID),
		ToolName: strings.TrimSpace(header.ToolName),
		Error:    errorText,
	})] = struct{}{}
	s.mu.Unlock()
}

// takeTurnToolFailures 取出并删除指定 turn 的工具失败。
// 删除发生在锁内，确保 synthetic completion 只消费一次失败状态。
func (s *session) takeTurnToolFailures(turnID string) []turnToolFailure {
	if s == nil {
		return nil
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	failures := make([]turnToolFailure, 0)
	for key := range s.suppressed {
		failure, ok := decodeTurnToolFailure(key)
		if !ok || failure.TurnID != turnID {
			continue
		}
		failures = append(failures, failure)
		delete(s.suppressed, key)
	}
	return failures
}

func (s *session) discardTurnToolFailures(turnID string) {
	_ = s.takeTurnToolFailures(turnID)
}

func encodeTurnToolFailure(failure turnToolFailure) string {
	return turnToolFailurePrefix + strings.Join([]string{
		strings.TrimSpace(failure.TurnID),
		strings.TrimSpace(failure.CallID),
		strings.TrimSpace(failure.ToolName),
		strings.TrimSpace(failure.Error),
	}, "\x00")
}

func decodeTurnToolFailure(key string) (turnToolFailure, bool) {
	if !strings.HasPrefix(key, turnToolFailurePrefix) {
		return turnToolFailure{}, false
	}
	parts := strings.SplitN(strings.TrimPrefix(key, turnToolFailurePrefix), "\x00", 4)
	if len(parts) != 4 {
		return turnToolFailure{}, false
	}
	return turnToolFailure{TurnID: parts[0], CallID: parts[1], ToolName: parts[2], Error: parts[3]}, true
}

// toolFailureSummary 汇总失败 callID、工具名和错误文本。
// 结果写入 TurnCompleted.Error，供 UI 与日志保留可追踪的失败来源。
func toolFailureSummary(failures []turnToolFailure) string {
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		callID := strings.TrimSpace(failure.CallID)
		toolName := strings.TrimSpace(failure.ToolName)
		errorText := strings.TrimSpace(failure.Error)
		switch {
		case callID != "" && toolName != "":
			parts = append(parts, callID+"/"+toolName+": "+errorText)
		case callID != "":
			parts = append(parts, callID+": "+errorText)
		case toolName != "":
			parts = append(parts, toolName+": "+errorText)
		default:
			parts = append(parts, errorText)
		}
	}
	return strings.Join(parts, "; ")
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

// toolEndSuppressionPayload 从 provider 事件中提取可用于去重的工具结束标识。
// rollout response_item 与普通 tool.call.end 字段形态不同，缺 callID 或工具名时必须放弃抑制。
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

// consumeSuppressedToolEnd 消费一次已登记的工具结束抑制记录。
// 支持 turnID 精确匹配、无 turnID 匹配和 MCP 长短工具名匹配，避免 forceComplete 后重复展示结束事件。
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
	return slices.Contains(candidates, value)
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

// trackCodexRolloutToolName 在 rollout 事件流中补齐工具结束事件的 toolName。
// Codex 的结束事件常只给 callID，因此需要从同一 callID 的开始事件缓存名称后再派发给 UI。
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

// rememberRolloutToolName 记录 rollout callID 到工具名的短期映射。
// 缓存有固定上限，避免长会话大量工具调用导致 map 无限增长。
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

// toolEventEndOutcome 从工具结束事件中推导成功状态和错误文本。
// 顶层错误优先，嵌套 result 若声明失败也会覆盖 success，保证 UI 看到真实失败。
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

// toolEventResultOutcome 解析工具 result 包装并判断它是否携带失败信息。
// 只有对象形态才进入 toolCallEndOutcome，纯文本或标量结果按成功透传。
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
	s.setActiveTurnLocked("")
	s.interruptRequests = nil
	s.pendingTurn = nil
	s.mu.Unlock()
	// 失败所有 turn 时同步丢弃 providerID 对应的输出累积器，避免关闭后残留大文本缓冲。
	for providerID, h := range turns {
		s.dropTurnOutputAccumulator(providerID)
		h.complete(err)
	}
}
