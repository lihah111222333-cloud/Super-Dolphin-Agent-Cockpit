package codexapp

import (
	"strings"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

const (
	codexMissingTimestampCode           = "codex_missing_timestamp"
	codexInvalidTimestampCode           = "codex_invalid_timestamp"
	codexTerminalTimestampProtocolError = "terminal contract: provider timestamp invalid"
)

// codexTimestampProviderError 对会生成生命周期或终态记录的坏时间显式返回 provider error。
func codexTimestampProviderError(raw dto.RawProviderEvent) (dto.RawProviderEvent, agentdto.AgentError, bool) {
	if !codexEventRequiresTimestamp(raw.EventType) {
		return dto.RawProviderEvent{}, agentdto.AgentError{}, false
	}
	payload, err := decodeRawEventPayload(raw.Data)
	if err != nil {
		return dto.RawProviderEvent{}, agentdto.AgentError{}, false
	}
	rawTimestamp := stringValue(payload, "timestamp", "ts", "createdAt", "created_at")
	if rawTimestamp != "" && !shared.ParseRFC3339Loose(rawTimestamp).IsZero() {
		return dto.RawProviderEvent{}, agentdto.AgentError{}, false
	}
	code := codexMissingTimestampCode
	message := "codexapp: provider event missing timestamp"
	if rawTimestamp != "" {
		code = codexInvalidTimestampCode
		message = "codexapp: provider event invalid timestamp"
	}
	errorPayload := map[string]any{
		"agentId":           payloadAgentID(payload),
		"threadId":          payloadThreadID(payload),
		"sessionId":         stringValue(payload, "sessionId", "session_id"),
		"turnId":            stringValue(payload, "turnId", "turn_id"),
		"callId":            stringValue(payload, "callId", "call_id"),
		"toolName":          stringValue(payload, "toolName", "tool_name"),
		"timestamp":         time.Now().UTC().Format(time.RFC3339Nano),
		"code":              code,
		"message":           message + ": " + strings.TrimSpace(raw.EventType),
		"source_event_type": strings.TrimSpace(raw.EventType),
		"raw_timestamp":     strings.TrimSpace(rawTimestamp),
	}
	rawErr := dto.RawProviderEvent{EventType: "error", Data: errorPayload}
	return rawErr, agentdto.AgentError{
		AgentSessionHeader: buildAgentSessionHeader(errorPayload),
		RawType:            rawErr.EventType,
		Message:            rawErr.PublicMessage("Provider reported an error."),
		Code:               code,
		Payload:            rawErr.SafePayload(),
	}, true
}

// codexTerminalTimestampInvalid 仅标记会中断 canonical terminal 的坏 provider 时间。
func codexTerminalTimestampInvalid(raw dto.RawProviderEvent) bool {
	if !isTurnTerminalEvent(raw.EventType) {
		return false
	}
	_, _, invalid := codexTimestampProviderError(raw)
	return invalid
}

// publishCodexTimestampFailureTerminal 在可归因 terminal 时间无效时发布脱敏的失败终态。
// 原始时间和 provider 错误只保留在内部 provider error 通道，绝不进入 TurnCompleted。
func publishCodexTimestampFailureTerminal(hooks providershared.RuntimeHooks, raw dto.RawProviderEvent, publish func(ev any)) {
	if !isTurnTerminalEvent(raw.EventType) {
		return
	}
	payload, err := decodeRawEventPayload(raw.Data)
	if err != nil {
		return
	}
	agentID := strings.TrimSpace(payloadAgentID(payload))
	turnID := strings.TrimSpace(payloadTurnID(payload))
	if agentID == "" || strings.TrimSpace(payloadThreadID(payload)) == "" || turnID == "" {
		return
	}
	safePayload := map[string]any{
		"agentId":   agentID,
		"threadId":  agentID,
		"sessionId": stringValue(payload, "sessionId", "session_id"),
		"turnId":    turnID,
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"error":     codexTerminalTimestampProtocolError,
	}
	terminal := &dto.TerminalOutcome{Status: "failed", ContractError: "provider timestamp invalid"}
	publishCodexTranslatedEvent(raw.EventType, translateTurnTerminalEvent(hooks, safePayload, terminal), publish)
}

func codexEventRequiresTimestamp(eventType string) bool {
	if isTurnTerminalEvent(eventType) {
		return true
	}
	eventType = strings.TrimSpace(eventType)
	return isCodexAgentLaunchedEvent(eventType) ||
		isCodexAgentStoppedEvent(eventType) ||
		eventType == "thread/status/changed" ||
		eventType == "recovery.attempt" ||
		eventType == "connection.dead" ||
		eventType == "turn/started" ||
		eventType == "turn.started"
}

func isCodexAgentLaunchedEvent(eventType string) bool {
	switch eventType {
	case "thread/started", "session.configured", "agent:launched":
		return true
	default:
		return false
	}
}

func isCodexAgentStoppedEvent(eventType string) bool {
	return eventType == "shutdown.complete" || eventType == "shutdown_complete"
}
