package codexapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// RegisterTranslators 将 Codex raw event 翻译器注册到统一事件分发器。
func RegisterTranslators(dispatcher *unified.EventDispatcher) {
	if dispatcher != nil {
		dispatcher.Register(translateCodexEvent)
	}
}

var outputDeltaTranslateLogSampler = pkglogger.NewEverySampler(1000)
var retryProgressMessagePattern = regexp.MustCompile(`(?i)^\s*(reconnecting|retrying)(\.\.\.)?\s+\d+\s*/\s*\d+\s*$`)

func buildAgentSessionHeader(payload map[string]any) shareddto.AgentSessionHeader {
	agentID := payloadAgentID(payload)
	threadID := shared.FirstNonEmpty(agentID, payloadThreadID(payload))
	return shareddto.AgentSessionHeader{AgentHeader: shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{EventHeader: shareddto.EventHeader{Timestamp: eventTime(payload)}, ThreadID: threadID}, AgentID: agentID}, SessionID: stringValue(payload, "sessionId", "session_id")}
}

func buildTurnHeader(payload map[string]any) shareddto.TurnHeader {
	return shareddto.TurnHeader{AgentHeader: buildAgentSessionHeader(payload).AgentHeader, TurnIDHeader: shareddto.TurnIDHeader{TurnID: payloadTurnID(payload)}}
}

func buildToolCallHeader(payload map[string]any) shareddto.ToolCallHeader {
	return shareddto.ToolCallHeader{TurnHeader: buildTurnHeader(payload), CallID: payloadCallID(payload), ToolName: payloadToolName(payload)}
}

func buildToolApprovalHeader(payload map[string]any) shareddto.ToolApprovalHeader {
	return shareddto.ToolApprovalHeader{ToolCallHeader: buildToolCallHeader(payload), ApprovalID: stringValue(payload, "approvalId", "approval_id")}
}

// translateCodexEvent 把 Codex app-server raw event 分派到 agent、turn、tool 三类统一事件。
// 未识别事件只在排除 token usage、重试进度和已知噪声后告警，避免日志被高频流事件淹没。
func translateCodexEvent(raw dto.RawProviderEvent, publish func(ev any)) {
	eventType := strings.TrimSpace(raw.EventType)
	payload, err := decodeRawEventPayload(raw.Data)
	if err != nil {
		pkglogger.Get().Warn("codexapp: invalid raw event payload", "raw_type", eventType, "error", err, "payload_metadata", raw.SanitizedCopy().Data)
		return
	}
	unified.PublishUITokensUpdated(payload, publish)
	if ev, ok := translateAgentEvent(eventType, payload); ok {
		publishCodexTranslatedEvent(eventType, ev, publish)
		return
	}
	if ev, ok := translateTurnEvent(eventType, payload); ok {
		publishCodexTranslatedEvent(eventType, ev, publish)
		return
	}
	if ev, ok := translateCodexRolloutToolEvent(eventType, payload); ok {
		publishCodexTranslatedEvent(eventType, ev, publish)
		return
	}
	if ev, ok := translateToolEvent(eventType, payload); ok {
		publishCodexTranslatedEvent(eventType, ev, publish)
		return
	}
	if logCodexMCPStartupStatus(eventType, payload) {
		return
	}
	if shouldWarnUnknownRawEvent(eventType, payload) {
		pkglogger.Get().Warn("codexapp: unknown raw event", "raw_type", eventType, "payload_metadata", raw.SanitizedCopy().Data)
	}
}

func publishCodexTranslatedEvent(eventType string, ev any, publish func(ev any)) {
	if err := validateCodexTranslatedEventIDs(eventType, ev); err != nil {
		pkglogger.Get().Warn("codexapp: invalid translated event", "raw_type", eventType, "error", err)
		return
	}
	publish(ev)
}

// validateCodexTranslatedEventIDs 在事件发布前阻断缺少关键 ID 的 DTO。
// 这里保持 fail-fast 记录并丢弃坏事件，避免坏 JSON 或缺 ID 被转换成零值 DTO 写入下游。
func validateCodexTranslatedEventIDs(eventType string, ev any) error {
	if err, ok := validateCodexAgentEventIDs(ev); ok {
		return err
	}
	if err, ok := validateCodexTurnEventIDs(ev); ok {
		return err
	}
	if err, ok := validateCodexToolEventIDs(eventType, ev); ok {
		return err
	}
	return nil
}

// validateCodexAgentEventIDs 校验 agent 级事件必须带 agent_id 和 thread_id。
// 返回的 bool 表示当前事件是否属于 agent 分类，方便主校验函数保持低复杂度。
func validateCodexAgentEventIDs(ev any) (error, bool) {
	switch typed := ev.(type) {
	case agentdto.AgentLaunched:
		return validateAgentSessionHeader(typed.AgentSessionHeader), true
	case agentdto.StateChanged:
		return validateAgentSessionHeader(typed.AgentSessionHeader), true
	case agentdto.AgentStopped:
		return validateAgentSessionHeader(typed.AgentSessionHeader), true
	case agentdto.AgentRecovering:
		return validateAgentSessionHeader(typed.AgentSessionHeader), true
	case agentdto.AgentFailed:
		return validateAgentSessionHeader(typed.AgentSessionHeader), true
	default:
		return nil, false
	}
}

// validateCodexTurnEventIDs 校验 turn 级事件必须保留完整线程和轮次上下文。
func validateCodexTurnEventIDs(ev any) (error, bool) {
	switch typed := ev.(type) {
	case turndto.TurnStarted:
		return validateTurnHeader(typed.TurnHeader), true
	case turndto.TurnCompleted:
		return validateTurnHeader(typed.TurnHeader), true
	case turndto.TurnInterrupted:
		return validateTurnHeader(typed.TurnHeader), true
	case turndto.TurnOutputDelta:
		return validateTurnHeader(typed.TurnHeader), true
	default:
		return nil, false
	}
}

// validateCodexToolEventIDs 校验工具事件必须带调用上下文。
// tool diff 事件没有通用 ToolCallHeader，单独校验 agent/thread 边界。
func validateCodexToolEventIDs(eventType string, ev any) (error, bool) {
	switch typed := ev.(type) {
	case tooldto.ToolCallBegin:
		return validateToolCallHeaderForEvent(eventType, typed.ToolCallHeader), true
	case tooldto.ToolCallEnd:
		return validateToolCallHeaderForEvent(eventType, typed.ToolCallHeader), true
	case tooldto.ToolApprovalRequested:
		return validateToolCallHeader(typed.ToolCallHeader), true
	case tooldto.ToolApprovalResolved:
		return validateToolCallHeader(typed.ToolCallHeader), true
	case tooldto.ToolDiffUpdated:
		return validateToolDiffUpdatedIDs(typed), true
	default:
		return nil, false
	}
}

// validateToolCallHeaderForEvent 校验工具事件头；rollout 工具帧可能没有 turn_id。
// 这类事件仍必须带 agent/thread/call/tool，不能发布完全零值的工具 DTO。
func validateToolCallHeaderForEvent(eventType string, header shareddto.ToolCallHeader) error {
	if isCodexRolloutToolEventType(eventType) && strings.TrimSpace(header.TurnID) == "" {
		return validateRolloutToolCallHeader(header)
	}
	return validateToolCallHeader(header)
}

// validateRolloutToolCallHeader 校验 Codex rollout 工具帧的最小可追踪 ID。
func validateRolloutToolCallHeader(header shareddto.ToolCallHeader) error {
	if err := validateAgentSessionHeader(shareddto.AgentSessionHeader{AgentHeader: header.AgentHeader}); err != nil {
		return err
	}
	if strings.TrimSpace(header.CallID) == "" {
		return fmt.Errorf("call_id is required")
	}
	if strings.TrimSpace(header.ToolName) == "" {
		return fmt.Errorf("tool_name is required")
	}
	return nil
}

func validateToolDiffUpdatedIDs(event tooldto.ToolDiffUpdated) error {
	if strings.TrimSpace(event.ThreadID) == "" {
		return fmt.Errorf("thread_id is required")
	}
	if strings.TrimSpace(event.AgentID) == "" {
		return fmt.Errorf("agent_id is required")
	}
	return nil
}

func validateAgentSessionHeader(header shareddto.AgentSessionHeader) error {
	if strings.TrimSpace(header.AgentID) == "" {
		return fmt.Errorf("agent_id is required")
	}
	if strings.TrimSpace(header.ThreadID) == "" {
		return fmt.Errorf("thread_id is required")
	}
	return nil
}

func validateTurnHeader(header shareddto.TurnHeader) error {
	if err := validateAgentSessionHeader(shareddto.AgentSessionHeader{AgentHeader: header.AgentHeader}); err != nil {
		return err
	}
	if strings.TrimSpace(header.TurnID) == "" {
		return fmt.Errorf("turn_id is required")
	}
	return nil
}

func validateToolCallHeader(header shareddto.ToolCallHeader) error {
	if err := validateTurnHeader(header.TurnHeader); err != nil {
		return err
	}
	if strings.TrimSpace(header.CallID) == "" {
		return fmt.Errorf("call_id is required")
	}
	if strings.TrimSpace(header.ToolName) == "" {
		return fmt.Errorf("tool_name is required")
	}
	return nil
}

// logCodexMCPStartupStatus 将 Codex MCP server 启动状态降噪写入日志。
// 失败或带错误消息的状态用 Warn，其余状态只 Debug，不继续进入 unknown raw event 告警。
func logCodexMCPStartupStatus(eventType string, payload map[string]any) bool {
	switch strings.TrimSpace(eventType) {
	case "mcpServer/startupStatus/update", "mcpServer/startupStatus/updated":
		status := stringValue(payload, "status")
		errMsg := stringValue(payload, "error", "message")
		attrs := []any{
			"agent_id", payloadAgentID(payload),
			"name", stringValue(payload, "name"),
			"status", status,
			"error", errMsg,
		}
		status = strings.ToLower(strings.TrimSpace(status))
		if status == "error" || status == "failed" || status == "failure" || strings.TrimSpace(errMsg) != "" {
			pkglogger.Get().Warn("codexapp: mcp server startup status", attrs...)
		} else {
			pkglogger.Get().Debug("codexapp: mcp server startup status", attrs...)
		}
		return true
	default:
		return false
	}
}

// shouldWarnUnknownRawEvent 判断未翻译 raw event 是否值得记录告警。
// token usage 和已登记的 UI 噪声事件会被静默忽略，真正未知 payload 才暴露给维护者。
func shouldWarnUnknownRawEvent(eventType string, payload map[string]any) bool {
	if isRetryProgressRawError(eventType, payload) {
		return false
	}
	switch strings.TrimSpace(eventType) {
	case "item/started", "item_started", "agent/event/item_started",
		"item/completed", "item_completed", "agent/event/item_completed", "rawResponseItem/completed",
		"item/plan/delta", "item_plan_delta", "agent/event/item_plan_delta",
		"item/plan/updated", "item_plan_updated", "agent/event/item_plan_updated",
		"thread/tokenUsage/updated",
		"account/rateLimits/updated",
		"tool:use_begin", "tool:use_end":
		return false
	}
	if len(payload) == 0 {
		return true
	}
	usage := nestedValue(payload, "usage")
	return !hasAnyKey(usage,
		"inputTokens", "input_tokens",
		"promptTokens", "prompt_tokens",
		"outputTokens", "output_tokens",
		"completionTokens", "completion_tokens",
		"totalTokens", "total_tokens",
		"contextWindowTokens", "context_window_tokens",
	) && !hasAnyKey(payload,
		"inputTokens", "input_tokens",
		"promptTokens", "prompt_tokens",
		"outputTokens", "output_tokens",
		"completionTokens", "completion_tokens",
		"totalTokens", "total_tokens",
		"contextWindowTokens", "context_window_tokens",
	)
}

func isRetryProgressRawError(eventType string, payload map[string]any) bool {
	message := shared.FirstNonEmpty(stringValue(payload, "message", "error", "reason"), stringValue(nestedValue(payload, "error"), "message"))
	return strings.TrimSpace(eventType) == "error" && boolValue(payload, "willRetry", "will_retry") && retryProgressMessagePattern.MatchString(message)
}

// translateAgentEvent 将 Codex 会话级事件转换为 agent DTO。
// 状态变化必须先经过枚举校验，防止 provider 新状态直接污染前端状态机。
func translateAgentEvent(eventType string, payload map[string]any) (any, bool) {
	switch eventType {
	case "thread/started", "session.configured", "agent:launched":
		return agentdto.AgentLaunched{
			AgentSessionHeader: buildAgentSessionHeader(payload),
			Model:              stringValue(payload, "model"),
			CWD:                stringValue(payload, "cwd"),
			Name:               stringValue(payload, "name"),
			Provider:           stringValue(payload, "provider"),
		}, true
	case "thread/status/changed":
		return validatedStateChangedEvent(payload)
	case "shutdown.complete", "shutdown_complete":
		return agentdto.AgentStopped{
			AgentSessionHeader: buildAgentSessionHeader(payload),
			Reason:             stringValue(payload, "reason", "message"),
		}, true
	case "recovery.attempt":
		return agentdto.AgentRecovering{
			AgentSessionHeader: buildAgentSessionHeader(payload),
			Reason:             shared.FirstNonEmpty(stringValue(payload, "reason", "message"), "reconnecting"),
			Attempt:            int(int64Value(payload, "attempt")),
		}, true
	case "connection.dead":
		safeReason := observability.SafeProviderErrorReason(shared.FirstNonEmpty(stringValue(payload, "error", "message"), "connection lost"))
		return agentdto.AgentFailed{
			AgentSessionHeader: buildAgentSessionHeader(payload),
			Error:              safeReason.Message,
			Recoverable:        boolValue(payload, "recoverable", "willRetry", "will_retry"),
		}, true
	default:
		return nil, false
	}
}

// translateTurnEvent 将 Codex turn 生命周期和输出 delta 转换为统一 turn DTO。
// terminal event 会先重置 tool result scope，避免下一轮复用上一轮的工具结果缓存。
func translateTurnEvent(eventType string, payload map[string]any) (any, bool) {
	if isTurnTerminalEvent(eventType) {
		return translateTurnTerminalEvent(eventType, payload), true
	}
	switch eventType {
	case "turn/started", "turn.started":
		return turndto.TurnStarted{TurnHeader: buildTurnHeader(payload)}, true
	case "turn/interrupted", "turn.interrupted":
		return translateTurnTerminalEvent(eventType, payload), true
	case "item/agentMessage/delta", "message.delta", "agent_message_delta":
		if outputDeltaTranslateLogSampler.ShouldLog("message") {
			pkglogger.Get().Debug("codexapp: translateTurnEvent: outputDelta",
				"sample_rate", "0.1%",
				"event_type", eventType,
				"stream", "message",
				"thread_id", payloadThreadID(payload),
				"agent_id", payloadAgentID(payload),
				"delta_len", len(stringValue(payload, "delta", "content")),
			)
		}
		return turnOutputDelta(payload, "message"), true
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta", "reasoning.delta":
		if outputDeltaTranslateLogSampler.ShouldLog("reasoning") {
			pkglogger.Get().Debug("codexapp: translateTurnEvent: outputDelta",
				"sample_rate", "0.1%",
				"event_type", eventType,
				"stream", "reasoning",
				"thread_id", payloadThreadID(payload),
				"agent_id", payloadAgentID(payload),
				"delta_len", len(stringValue(payload, "delta", "content")),
			)
		}
		return turnOutputDelta(payload, "reasoning"), true
	case "item/commandExecution/outputDelta", "exec_output_delta":
		if outputDeltaTranslateLogSampler.ShouldLog("stdout") {
			pkglogger.Get().Debug("codexapp: translateTurnEvent: outputDelta",
				"sample_rate", "0.1%",
				"event_type", eventType,
				"stream", "stdout",
				"thread_id", payloadThreadID(payload),
				"agent_id", payloadAgentID(payload),
				"delta_len", len(stringValue(payload, "delta", "content")),
			)
		}
		return turnOutputDelta(payload, "stdout"), true
	default:
		return nil, false
	}
}

// translateTurnTerminalEvent 构造终态事件，并把作用域清理失败提升为 turn 失败。
func translateTurnTerminalEvent(eventType string, payload map[string]any) turndto.TurnCompleted {
	header := buildTurnHeader(payload)
	outcome := resolveTurnTerminalOutcome(eventType, payload)
	errorText := turnTerminalError(outcome.success, payload)
	if outcome.contractError != "" {
		if errorText != "" {
			errorText += "; "
		}
		errorText += "terminal contract: " + outcome.contractError
	}
	if err := providershared.ResetToolResultScope(header.ThreadID, header.TurnID); err != nil {
		outcome.success = false
		outcome.status = "failed"
		errorText = appendProviderRuntimeError(errorText, err)
	}
	return turndto.TurnCompleted{
		TurnHeader:           header,
		Success:              outcome.success,
		Error:                errorText,
		Status:               outcome.status,
		Reason:               outcome.reason,
		TerminationRequestID: outcome.requestID,
		PartialItemIDs:       acceptedPartialItemIDs(payload),
		// result 由 session 的 per-turn 输出累积器在分发前合并进 payload。
		// 其他字段保留兼容读取，覆盖未来 Codex 直接携带 terminal 文本的 wire 形态。
		Result:     stringValue(payload, "result"),
		Summary:    stringValue(payload, "summary"),
		Message:    stringValue(payload, "message"),
		StopReason: stringValue(payload, "stop_reason"),
	}
}

// acceptedPartialItemIDs 只读取 session 为同一 active TurnRef 注入的已验收 assistant item ID。
func acceptedPartialItemIDs(payload map[string]any) []string {
	raw, ok := payload["accepted_partial_item_ids"]
	if !ok {
		return nil
	}
	switch values := raw.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		out := make([]string, len(values))
		for index, value := range values {
			itemID, ok := value.(string)
			if !ok || strings.TrimSpace(itemID) == "" {
				return []string{""}
			}
			out[index] = itemID
		}
		return out
	default:
		return []string{""}
	}
}

// turnTerminalError 将失败终态的错误字段映射到 Error。
// 成功终态的 reason 是结束原因，不能误填到 Error 字段影响 UI 和编排判断。
func turnTerminalError(success bool, payload map[string]any) string {
	if explicit := shared.FirstNonEmpty(
		stringValue(payload, "error"),
		stringValue(nestedValue(payload, "error"), "message"),
	); explicit != "" {
		return explicit
	}
	if success {
		return ""
	}
	return stringValue(payload, "message", "reason")
}

// validatedStateChangedEvent 规范化并校验 Codex state changed payload。
// 只发布已知 agent 状态；未知状态返回 false 交给 raw event 告警路径处理。
func validatedStateChangedEvent(payload map[string]any) (any, bool) {
	newState := stringValue(payload, "newState", "new_state", "status")
	if newState == "" {
		newState = stringValue(nestedValue(payload, "status"), "type")
	}
	switch strings.TrimSpace(newState) {
	case "active":
		newState = string(agentdto.StateTurnRunning)
	case "idle":
		newState = string(agentdto.StateIdle)
	}
	if !isKnownAgentState(newState) {
		return nil, false
	}
	oldState := stringValue(payload, "oldState", "old_state")
	if oldState == "" {
		oldState = stringValue(nestedValue(payload, "oldStatus"), "type")
	}
	switch strings.TrimSpace(oldState) {
	case "active":
		oldState = string(agentdto.StateTurnRunning)
	case "idle":
		oldState = string(agentdto.StateIdle)
	}
	if oldState != "" && !isKnownAgentState(oldState) {
		return nil, false
	}
	return agentdto.StateChanged{
		AgentSessionHeader: buildAgentSessionHeader(payload),
		OldState:           oldState,
		NewState:           newState,
		Trigger:            stringValue(payload, "trigger"),
	}, true
}

func isKnownAgentState(state string) bool {
	state = strings.TrimSpace(state)
	if state == "" {
		return false
	}
	for _, candidate := range agentdto.StateDefinitions {
		if string(candidate.Name) == state {
			return true
		}
	}
	return false
}

// translateToolEvent 将 Codex tool/approval/diff 事件转换为统一 tool DTO。
// tool end 会捕获结果预览并落盘大结果，避免 UI 事件携带过大的原始 payload。
func translateToolEvent(eventType string, payload map[string]any) (any, bool) {
	if isApprovalBridgeMethod(eventType) {
		return tooldto.ToolApprovalRequested{
			ToolApprovalHeader: buildToolApprovalHeader(payload),
			RequestID:          int64Value(payload, "requestId"),
			Reason:             stringValue(payload, "reason", "message"),
		}, true
	}
	switch eventType {
	case "item/tool/call", "dynamic_tool_call", "tool.call.begin":
		return tooldto.ToolCallBegin{
			ToolCallHeader:   buildToolCallHeader(payload),
			RequestID:        int64Value(payload, "requestId"),
			ArgumentsPreview: providershared.SafeToolArgumentsPreviewString(jsonPreview(payload, "arguments", "args")),
		}, true
	case "item/completed", "tool.call.end":
		if !looksLikeToolCall(payload) {
			return nil, false
		}
		header := buildToolCallHeader(payload)
		success, errorText := toolEventEndOutcome(eventType, payload)
		result, captureErr := providershared.CaptureToolResult(providershared.ToolResultMeta{
			ThreadID:  header.ThreadID,
			TurnID:    header.TurnID,
			CallID:    header.CallID,
			ToolName:  header.ToolName,
			Timestamp: eventTime(payload),
		}, jsonPreview(payload, "result", "content"))
		if captureErr != nil {
			success = false
			errorText = appendProviderRuntimeError(errorText, captureErr)
		}
		return tooldto.ToolCallEnd{
			ToolCallHeader: header,
			Success:        success,
			Error:          errorText,
			Result:         result.Preview,
			PersistedPath:  result.PersistedPath,
			PersistFailed:  result.PersistFailed,
			PersistError:   result.PersistError,
			Truncated:      result.Truncated,
			OriginalSize:   result.OriginalSize,
			ElapsedMS:      int64Value(payload, "elapsedMs", "elapsed_ms"),
		}, true
	case "approval/resolved", "tool.approval.resolved":
		return tooldto.ToolApprovalResolved{
			ToolApprovalHeader: buildToolApprovalHeader(payload),
			RequestID:          int64Value(payload, "requestId", "request_id"),
			Approved:           boolValue(payload, "approved"),
			Decision:           stringValue(payload, "decision"),
			ReviewedBy:         stringValue(payload, "reviewedBy", "reviewed_by"),
		}, true
	case "turn/diff/updated":
		return tooldto.ToolDiffUpdated{
			Timestamp: eventTime(payload),
			ThreadID:  shared.FirstNonEmpty(payloadAgentID(payload), payloadThreadID(payload)),
			AgentID:   payloadAgentID(payload),
			DiffText:  stringValue(payload, "diff"),
		}, true
	default:
		return nil, false
	}
}

// appendProviderRuntimeError 保留 provider 原始失败，并附加运行时依赖错误。
func appendProviderRuntimeError(current string, err error) string {
	if err == nil {
		return current
	}
	if strings.TrimSpace(current) == "" {
		return err.Error()
	}
	return current + "; " + err.Error()
}

func decodeRawEventPayload(data any) (map[string]any, error) {
	switch value := data.(type) {
	case map[string]any:
		return value, nil
	case json.RawMessage:
		return decodeRawEventPayloadBytes(value)
	case []byte:
		return decodeRawEventPayloadBytes(value)
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal raw payload: %w", err)
		}
		return decodeRawEventPayloadBytes(raw)
	}
}

func decodeRawEventPayloadBytes(raw []byte) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("raw payload is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode raw payload JSON object: %w", err)
	}
	if payload == nil {
		return nil, fmt.Errorf("raw payload must be a JSON object")
	}
	return payload, nil
}

func decodeAnyPayload(data any) map[string]any {
	payload, err := decodeRawEventPayload(data)
	if err != nil {
		return nil
	}
	return payload
}

func decodeEventPayload(raw []byte) map[string]any {
	payload, err := decodeRawEventPayloadBytes(raw)
	if err != nil {
		return nil
	}
	return payload
}

// encodeEventPayload 将可变 payload 重新编码为 json.RawMessage 后再分发。
// onNotification 会在 terminal event 前注入已累积的 message 输出；编码失败时保留原始 raw，避免丢弃事件本身。
func encodeEventPayload(payload map[string]any, fallback json.RawMessage) json.RawMessage {
	if payload == nil {
		return fallback
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fallback
	}
	return json.RawMessage(buf)
}

func nestedValue(payload map[string]any, key string) map[string]any {
	value, ok := payload[key].(map[string]any)
	if !ok {
		return nil
	}
	return value
}

// stringValue 按候选 key 提取 payload 中的非空字符串值。
// json.Number 直接转字符串，兼容 Codex 不同版本对 ID/计数字段的编码差异。
func stringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := payload[key].(type) {
		case string:
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		case json.Number:
			return value.String()
		}
	}
	return ""
}

// int64Value 按候选 key 宽松读取整数字段。
// 支持 float64、int64、json.Number 和数字字符串，解析失败时返回 0。
func int64Value(payload map[string]any, keys ...string) int64 {
	for _, key := range keys {
		switch value := payload[key].(type) {
		case float64:
			return int64(value)
		case int64:
			return value
		case json.Number:
			if parsed, err := value.Int64(); err == nil {
				return parsed
			}
		case string:
			if parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
				return parsed
			}
		}
	}
	return 0
}

// boolValue 按候选 key 宽松读取布尔字段。
// 字符串只接受 strconv.ParseBool 能识别的值，其他类型不做隐式猜测。
func boolValue(payload map[string]any, keys ...string) bool {
	for _, key := range keys {
		switch value := payload[key].(type) {
		case bool:
			return value
		case string:
			if parsed, err := strconv.ParseBool(strings.TrimSpace(value)); err == nil {
				return parsed
			}
		}
	}
	return false
}

// jsonPreview 将第一个存在的 payload 字段重新编码为 JSON 预览。
// 编码失败时返回空字符串，让调用方按缺失预览处理而不是中断事件翻译。
func jsonPreview(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok && value != nil {
			if raw, err := json.Marshal(value); err == nil {
				return string(raw)
			}
		}
	}
	return ""
}

func looksLikeToolCall(payload map[string]any) bool {
	header := buildToolCallHeader(payload)
	return header.CallID != "" && header.ToolName != ""
}

func eventTime(payload map[string]any) time.Time {
	if raw := stringValue(payload, "timestamp", "ts", "createdAt", "created_at"); raw != "" {
		if parsed := shared.ParseRFC3339Loose(raw); !parsed.IsZero() {
			return parsed
		}
	}
	return time.Now()
}
