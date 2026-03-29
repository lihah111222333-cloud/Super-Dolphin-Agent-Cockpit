package eventsurface

import (
	"context"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

const (
	MethodUIStateChanged           = "ui/state/changed"
	MethodTurnStarted              = "turn/started"
	MethodTurnCompleted            = "turn/completed"
	MethodTurnInterrupted          = "turn/interrupted"
	MethodTurnStalled              = "turn/stalled"
	MethodTurnResumed              = "turn/resumed"
	MethodTurnOutputDelta          = "turn/output/delta"
	MethodAgentMessageDelta        = "item/agentMessage/delta"
	MethodReasoningTextDelta       = "item/reasoning/textDelta"
	MethodCommandOutputDelta       = "item/commandExecution/outputDelta"
	MethodToolCall                 = "item/tool/call"
	MethodItemCompleted            = "item/completed"
	MethodCommandApprovalRequested = "item/commandExecution/requestApproval"
	MethodFileApprovalRequested    = "item/fileChange/requestApproval"
	MethodSkillApprovalRequested   = "skill/requestApproval"
	MethodApprovalResolved         = "approval/resolved"
	MethodThreadStarted            = "thread/started"
	MethodThreadStopped            = "thread/stopped"
	MethodThreadMessages           = "thread/messages/page"
	MethodThreadCompacted          = "thread/compacted"
	// Deprecated: token usage now rides on ui/thread/patch; kept for reference only
	MethodThreadTokenUsage       = "thread/tokenusage/updated"
	MethodSkillsChanged          = "skills/changed"
	MethodUIPreferencesChanged   = "ui/preferences/changed"
	MethodUIThreadPatch          = "ui/thread/patch"
	MethodAgentLaunched          = "agent/launched"
	MethodAgentStopped           = "agent/stopped"
	MethodAgentRecovering        = "agent/recovering"
	MethodAgentFailed            = "agent/failed"
	MethodAgentRuntimeReported   = "agent/runtime/reported"
)

type PublishFunc func(method string, payload any)

func Bind(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	if dispatcher == nil || publish == nil {
		return nil
	}
	cancels := bindCore(dispatcher, logger, publish)
	cancels = append(cancels, bindThread(dispatcher, logger, publish)...)
	cancels = append(cancels, bindTool(dispatcher, logger, publish)...)
	cancels = append(cancels, bindUI(dispatcher, logger, publish)...)
	cancels = append(cancels, bindAgentLifecycle(dispatcher, logger, publish)...)
	return cancels
}

func bindCore(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
			publish(MethodUIStateChanged, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStarted) {
			publish(MethodTurnStarted, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
			publish(MethodTurnCompleted, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnInterrupted) {
			publish(MethodTurnInterrupted, turnInterruptedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStalled) {
			publish(MethodTurnStalled, turnStalledPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnResumed) {
			publish(MethodTurnResumed, turnResumedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnOutputDelta) {
			publish(turnOutputMethod(ev), turnOutputDeltaPayload(ev))
		}, logger),
	}
}

func bindThread(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev threaddto.Started) {
			publish(MethodThreadStarted, threadStartedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev threaddto.Stopped) {
			publish(MethodThreadStopped, threadStoppedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev threaddto.MessagesPage) {
			publish(MethodThreadMessages, threadMessagesPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev threaddto.Compacted) {
			publish(MethodThreadCompacted, threadCompactedPayload(ev))
		}, logger),
	}
}

func bindTool(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolCallBegin) {
			publish(MethodToolCall, toolCallBeginPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolCallEnd) {
			publish(MethodItemCompleted, toolCallEndPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalRequested) {
			publish(toolApprovalRequestedMethod(ev), toolApprovalRequestedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
			publish(MethodApprovalResolved, toolApprovalResolvedPayload(ev))
		}, logger),
	}
}

func bindUI(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev uidto.UITokensUpdated) {
			publish(MethodThreadTokenUsage, threadTokenUsagePayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev uidto.SkillsChanged) {
			publish(MethodSkillsChanged, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev uidto.UIPreferencesChanged) {
			publish(MethodUIPreferencesChanged, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev uidto.UIThreadPatch) {
			publish(MethodUIThreadPatch, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev uidto.UIProjectionUpdated) {
			publish(projectionUpdatedMethod(ev), projectionUpdatedPayload(ev))
		}, logger),
	}
}

func bindAgentLifecycle(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentLaunched) {
			publish(MethodAgentLaunched, agentLaunchedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentStopped) {
			publish(MethodAgentStopped, agentStoppedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentRecovering) {
			publish(MethodAgentRecovering, agentRecoveringPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentFailed) {
			publish(MethodAgentFailed, agentFailedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentRuntimeReported) {
			publish(MethodAgentRuntimeReported, agentRuntimeReportedPayload(ev))
		}, logger),
	}
}

func threadStartedPayload(ev threaddto.Started) map[string]any {
	payload := map[string]any{"threadId": strings.TrimSpace(ev.ThreadID)}
	setString(payload, "agentId", ev.AgentID)
	setString(payload, "provider", ev.Provider)
	setString(payload, "providerThreadId", firstNonEmpty(ev.ProviderThreadID, ev.ThreadID))
	setString(payload, "cwd", ev.CWD)
	setString(payload, "model", ev.Model)
	return payload
}

func threadStoppedPayload(ev threaddto.Stopped) map[string]any {
	payload := map[string]any{"threadId": strings.TrimSpace(ev.ThreadID)}
	setString(payload, "agentId", ev.AgentID)
	setString(payload, "status", ev.Status)
	setString(payload, "reason", ev.Reason)
	return payload
}

func threadMessagesPayload(ev threaddto.MessagesPage) map[string]any {
	return map[string]any{
		"threadId":   strings.TrimSpace(ev.ThreadID),
		"totalCount": ev.TotalCount,
		"pages":      ev.Pages,
	}
}

func threadCompactedPayload(ev threaddto.Compacted) map[string]any {
	return map[string]any{
		"threadId":     strings.TrimSpace(ev.ThreadID),
		"command":      strings.TrimSpace(ev.Command),
		"beforeTokens": ev.BeforeTokens,
		"afterTokens":  ev.AfterTokens,
		"compacted":    ev.Compacted,
		"estimated":    ev.Estimated,
	}
}

func threadTokenUsagePayload(ev uidto.UITokensUpdated) map[string]any {
	payload := agentSessionPayload(shareddto.AgentSessionHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: ev.ThreadHeader,
		},
	})
	setString(payload, "turnId", ev.TurnID)
	payload["input_tokens"] = ev.InputTokens
	payload["output_tokens"] = ev.OutputTokens
	payload["total_tokens"] = ev.TotalTokens
	payload["contextWindowTokens"] = ev.ContextWindowTokens
	return payload
}

func turnInterruptedPayload(ev turndto.TurnInterrupted) map[string]any {
	payload := turnHeaderPayload(ev.TurnHeader)
	setString(payload, "reason", ev.Reason)
	return payload
}

func turnStalledPayload(ev turndto.TurnStalled) map[string]any {
	payload := turnHeaderPayload(ev.TurnHeader)
	setString(payload, "reason", ev.Reason)
	if ev.StalledMS > 0 {
		payload["stalledMs"] = ev.StalledMS
	}
	return payload
}

func turnResumedPayload(ev turndto.TurnResumed) map[string]any {
	payload := turnHeaderPayload(ev.TurnHeader)
	setString(payload, "reason", ev.Reason)
	return payload
}

func turnOutputMethod(ev turndto.TurnOutputDelta) string {
	switch strings.ToLower(strings.TrimSpace(ev.Stream)) {
	case "message":
		return MethodAgentMessageDelta
	case "reasoning":
		return MethodReasoningTextDelta
	case "stdout":
		return MethodCommandOutputDelta
	default:
		return MethodTurnOutputDelta
	}
}

func turnOutputDeltaPayload(ev turndto.TurnOutputDelta) map[string]any {
	payload := turnHeaderPayload(ev.TurnHeader)
	setString(payload, "stream", ev.Stream)
	setString(payload, "delta", ev.Delta)
	return payload
}

func toolCallBeginPayload(ev tooldto.ToolCallBegin) map[string]any {
	payload := toolCallHeaderPayload(ev.ToolCallHeader)
	if ev.RequestID > 0 {
		payload["requestId"] = ev.RequestID
	}
	setString(payload, "argumentsPreview", ev.ArgumentsPreview)
	return payload
}

func toolCallEndPayload(ev tooldto.ToolCallEnd) map[string]any {
	payload := toolCallHeaderPayload(ev.ToolCallHeader)
	payload["success"] = ev.Success
	setString(payload, "error", ev.Error)
	if ev.ElapsedMS > 0 {
		payload["elapsedMs"] = ev.ElapsedMS
	}
	return payload
}

func toolApprovalRequestedMethod(ev tooldto.ToolApprovalRequested) string {
	switch normalizedEventKind(ev.Kind) {
	case "filechange", "file":
		return MethodFileApprovalRequested
	case "skill":
		return MethodSkillApprovalRequested
	default:
		return MethodCommandApprovalRequested
	}
}

func toolApprovalRequestedPayload(ev tooldto.ToolApprovalRequested) map[string]any {
	payload := toolApprovalHeaderPayload(ev.ToolApprovalHeader)
	if ev.RequestID > 0 {
		payload["requestId"] = ev.RequestID
	}
	setString(payload, "reason", ev.Reason)
	setString(payload, "kind", ev.Kind)
	return payload
}

func toolApprovalResolvedPayload(ev tooldto.ToolApprovalResolved) map[string]any {
	payload := toolApprovalHeaderPayload(ev.ToolApprovalHeader)
	payload["approved"] = ev.Approved
	setString(payload, "decision", ev.Decision)
	setString(payload, "reviewedBy", ev.ReviewedBy)
	setString(payload, "kind", ev.Kind)
	return payload
}

func agentSessionPayload(header shareddto.AgentSessionHeader) map[string]any {
	payload := map[string]any{}
	setString(payload, "threadId", header.ThreadID)
	setString(payload, "agentId", header.AgentID)
	setString(payload, "sessionId", header.SessionID)
	return payload
}

func turnHeaderPayload(header shareddto.TurnHeader) map[string]any {
	payload := agentSessionPayload(shareddto.AgentSessionHeader{
		AgentHeader: header.AgentHeader,
	})
	setString(payload, "turnId", header.TurnID)
	return payload
}

func toolCallHeaderPayload(header shareddto.ToolCallHeader) map[string]any {
	payload := turnHeaderPayload(header.TurnHeader)
	setString(payload, "callId", header.CallID)
	setString(payload, "toolName", header.ToolName)
	payload["item"] = map[string]any{
		"kind":     "tool",
		"type":     "tool",
		"toolName": strings.TrimSpace(header.ToolName),
	}
	return payload
}

func toolApprovalHeaderPayload(header shareddto.ToolApprovalHeader) map[string]any {
	payload := toolCallHeaderPayload(header.ToolCallHeader)
	setString(payload, "approvalId", header.ApprovalID)
	payload["item"] = map[string]any{
		"kind":     "approval",
		"type":     "approval",
		"toolName": strings.TrimSpace(header.ToolName),
	}
	return payload
}

func normalizedEventKind(kind string) string {
	value := strings.ToLower(strings.TrimSpace(kind))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(value)
}

func setString(payload map[string]any, key, value string) {
	if text := strings.TrimSpace(value); text != "" {
		payload[key] = text
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}

func projectionUpdatedMethod(ev uidto.UIProjectionUpdated) string {
	switch strings.TrimSpace(ev.Projection) {
	case "sidebar":
		return MethodUISidebarChanged
	default:
		return MethodUIThreadChanged
	}
}

func projectionUpdatedPayload(ev uidto.UIProjectionUpdated) map[string]any {
	payload := map[string]any{
		"projection": strings.TrimSpace(ev.Projection),
		"revision":   ev.Revision,
	}
	if tid := strings.TrimSpace(ev.ThreadID); tid != "" {
		payload["threadId"] = tid
	}
	return payload
}
