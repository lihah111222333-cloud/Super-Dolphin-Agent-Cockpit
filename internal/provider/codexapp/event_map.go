package codexapp

import (
	"encoding/json"
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

func RegisterTranslators(dispatcher *unified.EventDispatcher) {
	if dispatcher != nil {
		dispatcher.Register(translateCodexEvent)
	}
}

var outputDeltaTranslateLogSampler = pkglogger.NewEverySampler(1000)

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
		pkglogger.Get().Warn("codexapp: unknown raw event", "raw_type", eventType, "payload", payload)
	}
}

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
		statusKind := strings.ToLower(strings.TrimSpace(status))
		if statusKind == "error" || statusKind == "failed" || statusKind == "failure" || strings.TrimSpace(errMsg) != "" {
			pkglogger.Get().Warn("codexapp: mcp server startup status", attrs...)
		} else {
			pkglogger.Get().Debug("codexapp: mcp server startup status", attrs...)
		}
		return true
	default:
		return false
	}
}

func shouldWarnUnknownRawEvent(eventType string, payload map[string]any) bool {
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
			// ADR-015 v4.1 §2.1: payload["result"] is populated by
			// session.sniffTurnOutput from the per-turn TurnOutputDelta
			// accumulator before onNotification dispatches the event.
			// The other three fields are read defensively in case future
			// codex builds attach them directly to TurnCompleted payloads.
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

// encodeEventPayload re-serializes a payload map back into json.RawMessage so
// onNotification can mutate the payload (e.g. inject merged TurnOutputDelta
// content for TurnCompleted, ADR-015 v4.1 §2.1) before forwarding. On
// marshal failure the original raw bytes are returned to preserve the
// dispatch path; loss only manifests as missing accumulated content rather
// than a dropped event.
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
