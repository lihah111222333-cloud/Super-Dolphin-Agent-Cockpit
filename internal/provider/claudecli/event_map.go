package claudecli

import (
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// RegisterTranslators 注册translators。
func RegisterTranslators(dispatcher *unified.EventDispatcher) {
	if dispatcher == nil {
		return
	}
	dispatcher.Register(translateClaudeEvent)
}

func translateClaudeEvent(raw dto.RawProviderEvent, publish func(ev any)) {
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

// translateAgentEvent 处理translate代理事件。
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

// translateTurnEvent 处理translateturn事件。
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
		return turndto.TurnInterrupted{
			TurnHeader: turnHeader(raw.Data),
			Reason:     dataString(raw.Data, "reason"),
		}, true
	case "turn:complete":
		header := turnHeader(raw.Data)
		providershared.ResetToolResultScope(header.ThreadID, header.TurnID)
		return turndto.TurnCompleted{
			TurnHeader: header,
			Success:    dataBool(raw.Data, "success"),
			Error:      dataString(raw.Data, "error"),
			Status:     dataString(raw.Data, "status"),
			Reason:     dataString(raw.Data, "reason"),
			Result:     dataString(raw.Data, "result"),
			Summary:    dataString(raw.Data, "summary"),
			Message:    dataString(raw.Data, "message"),
			StopReason: dataString(raw.Data, "stop_reason"),
		}, true
	default:
		return nil, false
	}
}

func translateToolEvent(raw dto.RawProviderEvent) (any, bool) {
	switch raw.EventType {
	case "tool:use_begin":
		return tooldto.ToolCallBegin{
			ToolCallHeader:   toolHeader(raw.Data),
			ArgumentsPreview: dataString(raw.Data, "arguments_preview"),
		}, true
	case "tool:use_end":
		header := toolHeader(raw.Data)
		result := providershared.CaptureToolResult(providershared.ToolResultMeta{
			ThreadID:  header.ThreadID,
			TurnID:    header.TurnID,
			CallID:    header.CallID,
			ToolName:  header.ToolName,
			Timestamp: eventTime(raw.Data),
		}, dataString(raw.Data, "result"))
		return tooldto.ToolCallEnd{
			ToolCallHeader: header,
			Success:        dataBool(raw.Data, "success"),
			Error:          dataString(raw.Data, "error"),
			Result:         result.Preview,
			PersistedPath:  result.PersistedPath,
			Truncated:      result.Truncated,
			OriginalSize:   result.OriginalSize,
		}, true
	default:
		return nil, false
	}
}

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

func turnHeader(data any) shared.TurnHeader {
	header := agentSessionHeader(data).AgentHeader
	return shared.TurnHeader{
		AgentHeader:  header,
		TurnIDHeader: shared.TurnIDHeader{TurnID: dataString(data, "turn_id")},
	}
}

func toolHeader(data any) shared.ToolCallHeader {
	return shared.ToolCallHeader{
		TurnHeader: turnHeader(data),
		CallID:     dataString(data, "call_id"),
		ToolName:   dataString(data, "tool_name"),
	}
}

func eventTime(data any) time.Time {
	raw := dataString(data, "timestamp", "ts")
	if parsed := platformshared.ParseRFC3339Loose(raw); !parsed.IsZero() {
		return parsed
	}
	return time.Now()
}
