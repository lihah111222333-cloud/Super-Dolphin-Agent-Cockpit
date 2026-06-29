package codexapp

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
	payload := decodeAnyPayload(raw.Data)
	unified.PublishUITokensUpdated(payload, publish)
	if ev, ok := translateAgentEvent(eventType, payload); ok {
		publish(ev)
		return
	}
	if ev, ok := translateTurnEvent(eventType, payload); ok {
		publish(ev)
		return
	}
	if ev, ok := translateCodexRolloutToolEvent(eventType, payload); ok {
		publish(ev)
		return
	}
	if ev, ok := translateToolEvent(eventType, payload); ok {
		publish(ev)
		return
	}
	if logCodexMCPStartupStatus(eventType, payload) {
		return
	}
	if shouldWarnUnknownRawEvent(eventType, payload) {
		pkglogger.Get().Warn("codexapp: unknown raw event", "raw_type", eventType, "payload_metadata", raw.SanitizedCopy().Data)
	}
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
		return agentdto.AgentFailed{
			AgentSessionHeader: buildAgentSessionHeader(payload),
			Error:              shared.FirstNonEmpty(stringValue(payload, "error", "message"), "connection lost"),
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
		header := buildTurnHeader(payload)
		providershared.ResetToolResultScope(header.ThreadID, header.TurnID)
		return turndto.TurnCompleted{
			TurnHeader: header,
			Success:    turnTerminalSuccess(eventType, payload),
			Error:      stringValue(payload, "error", "message", "reason"),
			Status:     stringValue(payload, "status"),
			Reason:     stringValue(payload, "reason"),
			// result 由 session 的 per-turn 输出累积器在分发前合并进 payload。
			// 其他字段保留兼容读取，覆盖未来 Codex 直接携带 terminal 文本的 wire 形态。
			Result:     stringValue(payload, "result"),
			Summary:    stringValue(payload, "summary"),
			Message:    stringValue(payload, "message"),
			StopReason: stringValue(payload, "stop_reason"),
		}, true
	}
	switch eventType {
	case "turn/started", "turn.started":
		return turndto.TurnStarted{TurnHeader: buildTurnHeader(payload)}, true
	case "turn/interrupted", "turn.interrupted":
		return turndto.TurnInterrupted{
			TurnHeader: buildTurnHeader(payload),
			Reason:     stringValue(payload, "reason", "message"),
		}, true
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
			ArgumentsPreview: jsonPreview(payload, "arguments", "args"),
		}, true
	case "item/completed", "tool.call.end":
		if !looksLikeToolCall(payload) {
			return nil, false
		}
		header := buildToolCallHeader(payload)
		success, errorText := toolEventEndOutcome(eventType, payload)
		result := providershared.CaptureToolResult(providershared.ToolResultMeta{
			ThreadID:  header.ThreadID,
			TurnID:    header.TurnID,
			CallID:    header.CallID,
			ToolName:  header.ToolName,
			Timestamp: eventTime(payload),
		}, jsonPreview(payload, "result", "content"))
		return tooldto.ToolCallEnd{
			ToolCallHeader: header,
			Success:        success,
			Error:          errorText,
			Result:         result.Preview,
			PersistedPath:  result.PersistedPath,
			Truncated:      result.Truncated,
			OriginalSize:   result.OriginalSize,
			ElapsedMS:      int64Value(payload, "elapsedMs", "elapsed_ms"),
		}, true
	case "approval/resolved", "tool.approval.resolved":
		return tooldto.ToolApprovalResolved{
			ToolApprovalHeader: buildToolApprovalHeader(payload),
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

func decodeAnyPayload(data any) map[string]any {
	switch value := data.(type) {
	case map[string]any:
		return value
	case json.RawMessage:
		return decodeEventPayload(value)
	case []byte:
		return decodeEventPayload(value)
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil
		}
		return decodeEventPayload(raw)
	}
}

func decodeEventPayload(raw []byte) map[string]any { return decodeJSONMap(raw) }

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
