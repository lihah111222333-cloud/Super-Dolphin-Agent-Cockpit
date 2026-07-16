package claudecli

import (
	"strings"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// RegisterTranslators 将 Claude provider 原始事件翻译器注册到统一 dispatcher。
func RegisterTranslators(dispatcher *unified.EventDispatcher) {
	if dispatcher == nil {
		return
	}
	dispatcher.Register(translateClaudeEvent)
}

// translateClaudeEvent 按 UI token、状态、agent、turn、tool 的顺序翻译并发布事件。
func translateClaudeEvent(raw dto.RawProviderEvent, publish func(ev any)) {
	if rawErr, agentErr, ok := claudeTimestampProviderError(raw); ok {
		publish(dto.BusRawProviderEvent{Event: rawErr})
		publish(agentErr)
		return
	}
	unified.PublishUITokensUpdated(raw.Data, publish)
	if ev, ok := translateStatusPatchEvent(raw); ok {
		publish(ev)
		return
	}
	if ev, ok := translateAgentEvent(raw); ok {
		publish(ev)
		return
	}
	if ev, ok := translateTurnEvent(raw); ok {
		publish(ev)
		return
	}
	if ev, ok := translateToolEvent(raw); ok {
		publish(ev)
	}
}

const (
	claudeMissingTimestampCode = "claude_missing_timestamp"
	claudeInvalidTimestampCode = "claude_invalid_timestamp"
)

// claudeTimestampProviderError 把缺失或坏格式 timestamp 转成显式 provider error。
// 调用方会在正常 typed 翻译前短路，避免坏事件继续生成零时间生命周期或工具事件。
func claudeTimestampProviderError(raw dto.RawProviderEvent) (dto.RawProviderEvent, agentdto.AgentError, bool) {
	if !claudeEventRequiresTimestamp(raw.EventType) {
		return dto.RawProviderEvent{}, agentdto.AgentError{}, false
	}
	rawTimestamp := dataString(raw.Data, "timestamp", "ts")
	if rawTimestamp != "" && !platformshared.ParseRFC3339Loose(rawTimestamp).IsZero() {
		return dto.RawProviderEvent{}, agentdto.AgentError{}, false
	}
	code := claudeMissingTimestampCode
	message := "claudecli: provider event missing timestamp"
	if rawTimestamp != "" {
		code = claudeInvalidTimestampCode
		message = "claudecli: provider event invalid timestamp"
	}
	rawErr := claudeProviderErrorRaw(raw.Data, code, message, raw.EventType, rawTimestamp)
	return rawErr, claudeAgentErrorFromRaw(rawErr), true
}

func claudeEventRequiresTimestamp(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "agent:status_patch",
		"system:init",
		"agent:state_changed",
		"agent:stopped",
		"agent:failed",
		"turn:started",
		"turn:input_received",
		"assistant:message_delta",
		"turn:interrupted",
		"turn:complete",
		"tool:use_begin",
		"tool:use_end",
		"tokens:log_watcher":
		return true
	default:
		return false
	}
}

// claudeProviderErrorRaw 构造 unified common translator 已支持的 raw error 形状。
// error 事件自身使用当前时间；source_event_type/raw_timestamp 保留原坏事件的来源。
func claudeProviderErrorRaw(source any, code, message, sourceEventType, rawTimestamp string) dto.RawProviderEvent {
	data := map[string]any{
		"agent_id":          dataString(source, "agent_id"),
		"thread_id":         dataString(source, "thread_id"),
		"session_id":        dataString(source, "session_id"),
		"turn_id":           dataString(source, "turn_id"),
		"call_id":           dataString(source, "call_id"),
		"tool_name":         dataString(source, "tool_name"),
		"timestamp":         time.Now().UTC().Format(time.RFC3339Nano),
		"code":              strings.TrimSpace(code),
		"message":           strings.TrimSpace(message) + ": " + strings.TrimSpace(sourceEventType),
		"source_event_type": strings.TrimSpace(sourceEventType),
		"raw_timestamp":     strings.TrimSpace(rawTimestamp),
	}
	return dto.RawProviderEvent{EventType: "error", Data: data}
}

func claudeAgentErrorFromRaw(raw dto.RawProviderEvent) agentdto.AgentError {
	return agentdto.AgentError{
		AgentSessionHeader: agentSessionHeader(raw.Data),
		RawType:            raw.EventType,
		Message:            dataString(raw.Data, "message"),
		Code:               dataString(raw.Data, "code"),
		Payload:            raw.SafePayload(),
	}
}

// translateStatusPatchEvent 翻译重启等 provider 状态补丁事件。
func translateStatusPatchEvent(raw dto.RawProviderEvent) (any, bool) {
	switch raw.EventType {
	case "agent:status_patch":
		return uidto.UIThreadPatch{
			ThreadID:      dataString(raw.Data, "thread_id"),
			Source:        dataString(raw.Data, "source"),
			Status:        dataString(raw.Data, "status"),
			StatusHeader:  dataString(raw.Data, "status_header"),
			StatusDetails: dataString(raw.Data, "status_details"),
			Partial:       dataBool(raw.Data, "partial"),
		}, true
	default:
		return nil, false
	}
}

// translateAgentEvent 翻译 agent 生命周期事件。
func translateAgentEvent(raw dto.RawProviderEvent) (any, bool) {
	switch raw.EventType {
	case "agent:launched":
		return agentdto.AgentLaunched{
			AgentSessionHeader: agentSessionHeader(raw.Data),
			Model:              dataString(raw.Data, "model"),
			CWD:                dataString(raw.Data, "cwd"),
		}, true
	case "system:init":
		// system:init carries the real session UUID; update session but do not
		// re-publish AgentLaunched (agent:launched already did that).
		return agentdto.AgentRuntimeReported{
			AgentSessionHeader: agentSessionHeader(raw.Data),
		}, true
	case "agent:state_changed":
		return agentdto.StateChanged{
			AgentSessionHeader: agentSessionHeader(raw.Data),
			OldState:           dataString(raw.Data, "old_state"),
			NewState:           dataString(raw.Data, "new_state"),
		}, true
	case "agent:stopped":
		return agentdto.AgentStopped{AgentSessionHeader: agentSessionHeader(raw.Data)}, true
	case "agent:failed":
		return agentdto.AgentFailed{
			AgentSessionHeader: agentSessionHeader(raw.Data),
			Error:              dataString(raw.Data, "error"),
		}, true
	default:
		return nil, false
	}
}

// translateTurnEvent 翻译 turn 生命周期和输出增量事件。
func translateTurnEvent(raw dto.RawProviderEvent) (any, bool) {
	switch raw.EventType {
	case "turn:started":
		return turndto.TurnStarted{TurnHeader: turnHeader(raw.Data)}, true
	case "turn:input_received":
		return turndto.TurnInputReceived{
			TurnHeader: turnHeader(raw.Data),
			InputType:  dataString(raw.Data, "input_type"),
			Source:     dataString(raw.Data, "source"),
			Text:       dataString(raw.Data, "text"),
		}, true
	case "assistant:message_delta":
		stream := dataString(raw.Data, "stream")
		delta := dataString(raw.Data, "delta")
		pkglogger.Get().Warn("claudecli: translateTurnEvent: message_delta",
			"stream", stream,
			"thread_id", dataString(raw.Data, "thread_id"),
			"delta_len", len(delta),
		)
		return turndto.TurnOutputDelta{
			TurnHeader: turnHeader(raw.Data),
			Stream:     stream,
			Delta:      delta,
		}, true
	case "turn:interrupted":
		return turndto.TurnCompleted{
			TurnHeader: turnHeader(raw.Data),
			Success:    false,
			Status:     "interrupted",
			Reason:     "provider",
			Error:      dataString(raw.Data, "reason"),
		}, true
	case "turn:complete":
		header := turnHeader(raw.Data)
		outcome := providershared.ResolveRawTerminalOutcome(raw.Data)
		errorText := dataString(raw.Data, "error")
		if outcome.ContractError != "" {
			if errorText != "" {
				errorText += "; "
			}
			errorText += "terminal contract: " + outcome.ContractError
		}
		if err := providershared.ResetToolResultScope(header.ThreadID, header.TurnID); err != nil {
			outcome.Success = false
			outcome.Status = "failed"
			outcome.Cause = ""
			errorText = appendProviderRuntimeError(errorText, err)
		}
		return turndto.TurnCompleted{
			TurnHeader: header,
			Success:    outcome.Success,
			Error:      errorText,
			Status:     outcome.Status,
			Reason:     terminalReason(outcome.Cause, dataString(raw.Data, "reason")),
			Result:     dataString(raw.Data, "result"),
			Summary:    dataString(raw.Data, "summary"),
			Message:    dataString(raw.Data, "message"),
			StopReason: dataString(raw.Data, "stop_reason"),
		}, true
	default:
		return nil, false
	}
}

func terminalReason(cause, rawReason string) string {
	if strings.TrimSpace(cause) != "" {
		return cause
	}
	return strings.TrimSpace(rawReason)
}

// translateToolEvent 翻译工具开始/结束事件，并把大结果交给共享结果捕获器。
func translateToolEvent(raw dto.RawProviderEvent) (any, bool) {
	switch raw.EventType {
	case "tool:use_begin":
		return tooldto.ToolCallBegin{
			ToolCallHeader:   toolHeader(raw.Data),
			ArgumentsPreview: providershared.SafeToolArgumentsPreviewString(dataString(raw.Data, "arguments_preview")),
		}, true
	case "tool:use_end":
		header := toolHeader(raw.Data)
		result, captureErr := providershared.CaptureToolResult(providershared.ToolResultMeta{
			ThreadID:  header.ThreadID,
			TurnID:    header.TurnID,
			CallID:    header.CallID,
			ToolName:  header.ToolName,
			Timestamp: eventTime(raw.Data),
		}, dataString(raw.Data, "result"))
		success := dataBool(raw.Data, "success")
		errorText := dataString(raw.Data, "error")
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

// agentSessionHeader 从事件 data 中抽取 agent/session 公共头。
func agentSessionHeader(data any) shared.AgentSessionHeader {
	return shared.AgentSessionHeader{
		AgentHeader: shared.AgentHeader{
			ThreadHeader: shared.ThreadHeader{
				EventHeader: shared.EventHeader{Timestamp: eventTime(data)},
				ThreadID:    dataString(data, "thread_id"),
			},
			AgentID: dataString(data, "agent_id"),
		},
		SessionID: dataString(data, "session_id"),
	}
}

// turnHeader 从事件 data 中抽取 turn 公共头。
func turnHeader(data any) shared.TurnHeader {
	header := agentSessionHeader(data).AgentHeader
	return shared.TurnHeader{
		AgentHeader:  header,
		TurnIDHeader: shared.TurnIDHeader{TurnID: dataString(data, "turn_id")},
	}
}

// toolHeader 从事件 data 中抽取 tool call 公共头。
func toolHeader(data any) shared.ToolCallHeader {
	return shared.ToolCallHeader{
		TurnHeader: turnHeader(data),
		CallID:     dataString(data, "call_id"),
		ToolName:   dataString(data, "tool_name"),
	}
}

// eventTime 解析 provider timestamp，缺失或格式不兼容时返回零值时间。
// 零值让上游坏事件保持可见，不能用当前时间伪造成正常 provider 时间。
func eventTime(data any) time.Time {
	raw := dataString(data, "timestamp", "ts")
	if parsed := platformshared.ParseRFC3339Loose(raw); !parsed.IsZero() {
		return parsed
	}
	return time.Time{}
}
