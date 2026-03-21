package codexapp

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func RegisterTranslators(dispatcher *unified.EventDispatcher) {
	if dispatcher != nil {
		dispatcher.Register(translateCodexEvent)
	}
}

func translateCodexEvent(raw dto.RawProviderEvent, publish func(ev any)) {
	eventType := strings.TrimSpace(raw.Type)
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
	if ev, ok := translateToolEvent(eventType, payload); ok {
		publish(ev)
	}
}

func translateAgentEvent(eventType string, payload map[string]any) (any, bool) {
	switch eventType {
	case "thread/started", "session.configured":
		return agentdto.AgentLaunched{
			AgentSessionHeader: agentSessionHeader(payload),
			Model:              stringValue(payload, "model"),
			CWD:                stringValue(payload, "cwd"),
		}, true
	case "thread/status/changed":
		return agentdto.StateChanged{
			AgentSessionHeader: agentSessionHeader(payload),
			OldState:           stringValue(payload, "oldState", "old_state"),
			NewState:           stringValue(payload, "newState", "new_state", "status"),
			Trigger:            stringValue(payload, "trigger"),
		}, true
	case "shutdown.complete", "shutdown_complete":
		return agentdto.AgentStopped{
			AgentSessionHeader: agentSessionHeader(payload),
			Reason:             stringValue(payload, "reason", "message"),
		}, true
	case "recovery.attempt":
		return agentdto.AgentRecovering{
			AgentSessionHeader: agentSessionHeader(payload),
			Reason:             firstNonEmpty(stringValue(payload, "reason", "message"), "reconnecting"),
			Attempt:            int(int64Value(payload, "attempt")),
		}, true
	case "connection.dead":
		return agentdto.AgentFailed{
			AgentSessionHeader: agentSessionHeader(payload),
			Error:              firstNonEmpty(stringValue(payload, "error", "message"), "connection lost"),
			Recoverable:        boolValue(payload, "recoverable", "willRetry", "will_retry"),
		}, true
	default:
		return nil, false
	}
}

func translateTurnEvent(eventType string, payload map[string]any) (any, bool) {
	switch eventType {
	case "turn/started", "turn.started":
		return turndto.TurnStarted{TurnHeader: turnHeader(payload)}, true
	case "turn/completed", "turn.completed", "turn/aborted", "turn.aborted":
		return turndto.TurnCompleted{
			TurnHeader: turnHeader(payload),
			Success:    completionSuccess(eventType, payload),
			Error:      stringValue(payload, "error", "message", "reason"),
			Status:     stringValue(payload, "status"),
			Reason:     stringValue(payload, "reason"),
		}, true
	case "turn/interrupted", "turn.interrupted":
		return turndto.TurnInterrupted{
			TurnHeader: turnHeader(payload),
			Reason:     stringValue(payload, "reason", "message"),
		}, true
	case "item/agentMessage/delta", "message.delta", "agent_message_delta":
		return turndto.TurnOutputDelta{
			TurnHeader: turnHeader(payload),
			Stream:     "message",
			Delta:      stringValue(payload, "delta", "content"),
		}, true
	case "item/reasoning/summaryTextDelta", "item/reasoning/textDelta", "reasoning.delta":
		return turndto.TurnOutputDelta{
			TurnHeader: turnHeader(payload),
			Stream:     "reasoning",
			Delta:      stringValue(payload, "delta", "content"),
		}, true
	case "item/commandExecution/outputDelta", "exec_output_delta":
		return turndto.TurnOutputDelta{
			TurnHeader: turnHeader(payload),
			Stream:     "stdout",
			Delta:      stringValue(payload, "delta", "content"),
		}, true
	default:
		return nil, false
	}
}

func translateToolEvent(eventType string, payload map[string]any) (any, bool) {
	switch eventType {
	case "item/tool/call", "dynamic_tool_call", "tool.call.begin":
		return tooldto.ToolCallBegin{
			ToolCallHeader:   toolCallHeader(payload),
			RequestID:        int64Value(payload, "requestId"),
			ArgumentsPreview: jsonPreview(payload, "arguments", "args"),
		}, true
	case "item/completed", "tool.call.end":
		if !looksLikeToolCall(payload) {
			return nil, false
		}
		return tooldto.ToolCallEnd{
			ToolCallHeader: toolCallHeader(payload),
			Success:        completionSuccess(eventType, payload),
			Error:          stringValue(payload, "error", "message", "reason"),
			ElapsedMS:      int64Value(payload, "elapsedMs", "elapsed_ms"),
		}, true
	case rpc.DefaultApprovalCallbackMethod,
		"tool/approval/request",
		"item/commandExecution/requestApproval",
		"item/fileChange/requestApproval",
		"skill/requestApproval",
		"tool.approval.requested":
		return tooldto.ToolApprovalRequested{
			ToolApprovalHeader: toolApprovalHeader(payload),
			RequestID:          int64Value(payload, "requestId"),
			Reason:             stringValue(payload, "reason", "message"),
		}, true
	case "approval/resolved", "tool.approval.resolved":
		return tooldto.ToolApprovalResolved{
			ToolApprovalHeader: toolApprovalHeader(payload),
			Approved:           boolValue(payload, "approved"),
			Decision:           stringValue(payload, "decision"),
			ReviewedBy:         stringValue(payload, "reviewedBy", "reviewed_by"),
		}, true
	default:
		return nil, false
	}
}

func agentSessionHeader(payload map[string]any) shared.AgentSessionHeader {
	threadID := firstNonEmpty(stringValue(payload, "threadId", "thread_id"), stringValue(nestedValue(payload, "thread"), "id"))
	return shared.AgentSessionHeader{
		AgentHeader: shared.AgentHeader{
			ThreadHeader: shared.ThreadHeader{
				EventHeader: shared.EventHeader{Timestamp: eventTime(payload)},
				ThreadID:    threadID,
			},
			AgentID: stringValue(payload, "agentId", "agent_id"),
		},
		SessionID: firstNonEmpty(stringValue(payload, "sessionId", "session_id"), threadID),
	}
}

func turnHeader(payload map[string]any) shared.TurnHeader {
	return shared.TurnHeader{
		AgentHeader: agentSessionHeader(payload).AgentHeader,
		TurnIDHeader: shared.TurnIDHeader{
			TurnID: firstNonEmpty(
				stringValue(payload, "turnId", "turn_id"),
				stringValue(nestedValue(payload, "turn"), "id"),
			),
		},
	}
}

func toolCallHeader(payload map[string]any) shared.ToolCallHeader {
	return shared.ToolCallHeader{
		TurnHeader: turnHeader(payload),
		CallID:     firstNonEmpty(stringValue(payload, "callId", "call_id"), stringValue(nestedValue(payload, "item"), "callId")),
		ToolName:   firstNonEmpty(stringValue(payload, "toolName", "tool_name", "tool"), stringValue(nestedValue(payload, "item"), "toolName", "tool")),
	}
}

func toolApprovalHeader(payload map[string]any) shared.ToolApprovalHeader {
	return shared.ToolApprovalHeader{
		ToolCallHeader: toolCallHeader(payload),
		ApprovalID:     stringValue(payload, "approvalId", "approval_id"),
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

func decodeEventPayload(raw []byte) map[string]any {
	var payload map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	return payload
}

func nestedValue(payload map[string]any, key string) map[string]any {
	value, _ := payload[key].(map[string]any)
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

func completionSuccess(eventType string, payload map[string]any) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(eventType)), "aborted") {
		return false
	}
	if value, ok := payload["success"].(bool); ok {
		return value
	}
	status := strings.ToLower(stringValue(payload, "status"))
	return status == "" || (status != "failed" && status != "error" && status != "aborted")
}

func looksLikeToolCall(payload map[string]any) bool {
	header := toolCallHeader(payload)
	return header.CallID != "" && header.ToolName != ""
}

func eventTime(payload map[string]any) time.Time {
	if raw := stringValue(payload, "timestamp", "ts", "createdAt", "created_at"); raw != "" {
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, raw); err == nil {
				return parsed
			}
		}
	}
	return time.Now()
}
