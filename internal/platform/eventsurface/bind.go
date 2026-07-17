package eventsurface

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	crondto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/cron"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	taskdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/task"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

var outputDeltaPublishLogSampler = pkglogger.NewEverySampler(1000)

func captureBindStack() string {
	pcs := make([]uintptr, 8)
	n := runtime.Callers(3, pcs)
	frames := runtime.CallersFrames(pcs[:n])
	var b strings.Builder
	for {
		f, more := frames.Next()
		fmt.Fprintf(&b, "%s:%d ", f.Function, f.Line)
		if !more || b.Len() > 400 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}

const (
	MethodUIStateChanged = "ui/state/changed"
	MethodTurnStarted    = "turn/started"
	MethodTurnTerminal   = "turn/terminal"
	// Deprecated: 远端 orchestration 仍引用旧符号名，wire 值已统一为 turn/terminal。
	MethodTurnCompleted            = MethodTurnTerminal
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
	// Deprecated: token usage 已迁移到 ui/thread/patch；常量仅保留 wire 兼容。
	MethodThreadTokenUsage       = "thread/tokenusage/updated"
	MethodSkillsChanged          = "skills/changed"
	MethodUIPreferencesChanged   = "ui/preferences/changed"
	MethodUIThreadPatch          = "ui/thread/patch"
	MethodUISharedFilesChanged   = "ui/shared-files/changed"
	MethodUIMemoryChanged        = "ui/memory/changed"
	MethodUIPromptsChanged       = "ui/prompts/changed"
	MethodAgentLaunched          = "agent/launched"
	MethodAgentStopped           = "agent/stopped"
	MethodAgentRecovering        = "agent/recovering"
	MethodAgentFailed            = "agent/failed"
	MethodAgentRuntimeReported   = "agent/runtime/reported"
	MethodTaskNodeStatusChanged  = "task/node/statusChanged"
	MethodCronJobRunStateChanged = "cron/job/runStateChanged"
)

// PublishFunc 是事件面向 UI/远端发布 JSON-RPC 通知的边界函数。
type PublishFunc func(method string, payload any)

// Bind 将核心 event bus 事件绑定到前端事件面发布函数。
// dispatcher 或 publish 为空时直接返回空取消函数列表，并记录错误，避免半绑定事件管线继续运行。
func Bind(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	if dispatcher == nil || publish == nil {
		if logger != nil {
			logger.Error("eventsurface: Bind called with nil dispatcher or publish, event pipeline disabled")
		}
		return nil
	}
	if logger != nil {
		logger.Warn("eventsurface: Bind() called",
			"dispatcher", fmt.Sprintf("%p", dispatcher),
			"stack", captureBindStack(),
		)
	}
	cancels := bindCore(dispatcher, logger, publish)
	cancels = append(cancels, bindThread(dispatcher, logger, publish)...)
	cancels = append(cancels, bindTool(dispatcher, logger, publish)...)
	cancels = append(cancels, bindUI(dispatcher, logger, publish)...)
	cancels = append(cancels, bindAgentLifecycle(dispatcher, logger, publish)...)
	cancels = append(cancels, bindTask(dispatcher, logger, publish)...)
	cancels = append(cancels, bindCron(dispatcher, logger, publish)...)
	return cancels
}

func bindTask(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev taskdto.TaskNodeStatusChanged) {
			publish(MethodTaskNodeStatusChanged, taskNodeStatusChangedPayload(ev))
		}, logger),
	}
}

func taskNodeStatusChangedPayload(ev taskdto.TaskNodeStatusChanged) map[string]any {
	payload := map[string]any{
		"dag_key":    strings.TrimSpace(ev.DagKey),
		"node_key":   strings.TrimSpace(ev.NodeKey),
		"new_status": strings.TrimSpace(ev.NewStatus),
	}
	if ev.RunID != 0 {
		payload["run_id"] = ev.RunID
	}
	setString(payload, "run_key", ev.RunKey)
	setString(payload, "old_status", ev.OldStatus)
	setString(payload, "assigned_to", ev.AssignedTo)
	setString(payload, "active_turn_id", ev.ActiveTurnID)
	if ev.ActiveWakeupID != 0 {
		payload["active_wakeup_id"] = ev.ActiveWakeupID
	}
	return payload
}

// bindCore 绑定 turn 和全局状态事件。
// 输出增量事件带采样 debug 日志，避免高频 token/command 输出刷爆日志。
func bindCore(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.StateChanged) {
			publish(MethodUIStateChanged, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStarted) {
			publish(MethodTurnStarted, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
			publishTurnTerminal(logger, publish, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStalled) {
			publish(MethodTurnStalled, turnStalledPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnResumed) {
			publish(MethodTurnResumed, turnResumedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev turndto.TurnOutputDelta) {
			method := turnOutputMethod(ev)
			if logger != nil && outputDeltaPublishLogSampler.ShouldLog(ev.Stream) {
				logger.Debug("eventsurface: TurnOutputDelta publish",
					"sample_rate", "0.1%",
					"method", method,
					"thread_id", ev.ThreadID,
					"stream", ev.Stream,
					"delta_len", len(ev.Delta),
				)
			}
			publish(method, turnOutputDeltaPayload(ev))
		}, logger),
	}
}

func publishTurnTerminal(logger *pkglogger.Logger, publish PublishFunc, ev turndto.TurnCompleted) {
	terminal, canonical, err := turndto.CanonicalTurnTerminal(ev)
	if err == nil && !canonical {
		terminal, err = turndto.NewTurnTerminalV2(ev, uuid.NewString())
	}
	if err != nil {
		if logger != nil {
			logger.Error("eventsurface: canonical turn terminal rejected", "error", err, "thread_id", ev.ThreadID, "turn_id", ev.TurnID)
			return
		}
		pkglogger.Error("eventsurface: canonical turn terminal rejected", "error", err, "thread_id", ev.ThreadID, "turn_id", ev.TurnID)
		return
	}
	publish(MethodTurnTerminal, terminal)
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
		bus.ResilientSubscribe(dispatcher, func(ev uidto.UISharedFilesChanged) {
			publish(MethodUISharedFilesChanged, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev uidto.UIMemoryChanged) {
			publish(MethodUIMemoryChanged, ev)
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev uidto.UIPromptsChanged) {
			publish(MethodUIPromptsChanged, ev)
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
	setString(payload, "providerThreadId", shared.FirstNonEmpty(ev.ProviderThreadID, ev.ThreadID))
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
	setString(payload, "result", ev.Result)
	setString(payload, "persistedPath", ev.PersistedPath)
	if ev.PersistFailed {
		payload["persistFailed"] = true
	}
	setString(payload, "persistError", ev.PersistError)
	if ev.Truncated {
		payload["truncated"] = true
	}
	if ev.OriginalSize > 0 {
		payload["originalSize"] = ev.OriginalSize
	}
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
	if ev.RequestID > 0 {
		payload["requestId"] = ev.RequestID
	}
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
	setString(payload, "sessionScope", header.SessionScope)
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

func bindCron(dispatcher *event.Dispatcher, logger *pkglogger.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev crondto.JobRunStateChanged) {
			publish(MethodCronJobRunStateChanged, cronJobRunStateChangedPayload(ev))
		}, logger),
	}
}

func cronJobRunStateChangedPayload(ev crondto.JobRunStateChanged) map[string]any {
	payload := map[string]any{
		"job_id": strings.TrimSpace(ev.JobID),
		"run_id": strings.TrimSpace(ev.RunID),
		"status": strings.TrimSpace(ev.Status),
	}
	setString(payload, "turn_id", ev.TurnID)
	setString(payload, "error", ev.Error)
	if !ev.ScheduledAt.IsZero() {
		payload["scheduled_at"] = ev.ScheduledAt.UTC().Format(time.RFC3339)
	}
	return payload
}
