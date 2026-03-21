package eventsurface

import (
	"context"
	"log/slog"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
	"github.com/kelindar/event"
)

const (
	MethodUIStateChanged   = "ui/state/changed"
	MethodTurnStarted      = "turn/started"
	MethodTurnCompleted    = "turn/completed"
	MethodThreadStarted    = "thread/started"
	MethodThreadStopped    = "thread/stopped"
	MethodThreadMessages   = "thread/messages/page"
	MethodWorkspaceCreated = "workspace/run/created"
	MethodWorkspaceMerged  = "workspace/run/merged"
	MethodWorkspaceAborted = "workspace/run/aborted"
	MethodAgentLaunched    = "agent/launched"
	MethodAgentStopped     = "agent/stopped"
)

type PublishFunc func(method string, payload any)

func Bind(dispatcher *event.Dispatcher, logger *slog.Logger, publish PublishFunc) []context.CancelFunc {
	if dispatcher == nil || publish == nil {
		return nil
	}
	cancels := bindCore(dispatcher, logger, publish)
	cancels = append(cancels, bindThread(dispatcher, logger, publish)...)
	cancels = append(cancels, bindWorkspace(dispatcher, logger, publish)...)
	cancels = append(cancels, bindAgentLifecycle(dispatcher, logger, publish)...)
	return cancels
}

func bindCore(dispatcher *event.Dispatcher, logger *slog.Logger, publish PublishFunc) []context.CancelFunc {
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
	}
}

func bindThread(dispatcher *event.Dispatcher, logger *slog.Logger, publish PublishFunc) []context.CancelFunc {
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
	}
}

func bindWorkspace(dispatcher *event.Dispatcher, logger *slog.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev workspacedto.WorkspaceRunCreated) {
			publish(MethodWorkspaceCreated, workspaceCreatedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev workspacedto.WorkspaceRunMerged) {
			publish(MethodWorkspaceMerged, workspaceMergedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev workspacedto.WorkspaceRunAborted) {
			publish(MethodWorkspaceAborted, workspaceAbortedPayload(ev))
		}, logger),
	}
}

func bindAgentLifecycle(dispatcher *event.Dispatcher, logger *slog.Logger, publish PublishFunc) []context.CancelFunc {
	return []context.CancelFunc{
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentLaunched) {
			publish(MethodAgentLaunched, agentLaunchedPayload(ev))
		}, logger),
		bus.ResilientSubscribe(dispatcher, func(ev agentdto.AgentStopped) {
			publish(MethodAgentStopped, agentStoppedPayload(ev))
		}, logger),
	}
}

func workspaceCreatedPayload(ev workspacedto.WorkspaceRunCreated) map[string]any {
	return map[string]any{
		"runKey": ev.RunKey,
		"run": workspaceRunPayload(
			ev.RunKey, ev.DagKey, ev.SourceRoot, ev.WorkspacePath,
			firstNonEmpty(ev.Status, "active"), ev.CreatedBy, firstNonEmpty(ev.UpdatedBy, ev.CreatedBy), ev.Timestamp,
		),
	}
}

func workspaceMergedPayload(ev workspacedto.WorkspaceRunMerged) map[string]any {
	return map[string]any{
		"runKey": ev.RunKey,
		"result": map[string]any{
			"runKey":        ev.RunKey,
			"status":        firstNonEmpty(ev.Status, "merged"),
			"sourceRoot":    ev.SourceRoot,
			"workspacePath": ev.WorkspacePath,
			"dryRun":        ev.DryRun,
			"merged":        ev.MergedFileCount,
			"removed":       ev.Removed,
			"conflicts":     ev.Conflicts,
			"unchanged":     ev.Unchanged,
			"errors":        ev.Errors,
		},
	}
}

func workspaceAbortedPayload(ev workspacedto.WorkspaceRunAborted) map[string]any {
	return map[string]any{
		"runKey": ev.RunKey,
		"reason": strings.TrimSpace(ev.Reason),
		"run": workspaceRunPayload(
			ev.RunKey, ev.DagKey, ev.SourceRoot, ev.WorkspacePath,
			firstNonEmpty(ev.Status, "aborted"), "", ev.UpdatedBy, ev.Timestamp,
		),
	}
}

func workspaceRunPayload(
	runKey, dagKey, sourceRoot, workspacePath, status, createdBy, updatedBy string,
	timestamp time.Time,
) map[string]any {
	payload := map[string]any{"runKey": strings.TrimSpace(runKey)}
	setString(payload, "dagKey", dagKey)
	setString(payload, "sourceRoot", sourceRoot)
	setString(payload, "workspacePath", workspacePath)
	setString(payload, "status", status)
	setString(payload, "createdBy", createdBy)
	setString(payload, "updatedBy", updatedBy)
	if !timestamp.IsZero() {
		payload["createdAt"] = timestamp
		payload["updatedAt"] = timestamp
	}
	return payload
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

func agentLaunchedPayload(ev agentdto.AgentLaunched) map[string]any {
	payload := map[string]any{}
	setString(payload, "threadId", ev.ThreadID)
	setString(payload, "agentId", ev.AgentID)
	setString(payload, "sessionId", ev.SessionID)
	setString(payload, "cwd", ev.CWD)
	setString(payload, "model", ev.Model)
	return payload
}

func agentStoppedPayload(ev agentdto.AgentStopped) map[string]any {
	payload := map[string]any{}
	setString(payload, "threadId", ev.ThreadID)
	setString(payload, "agentId", ev.AgentID)
	setString(payload, "sessionId", ev.SessionID)
	setString(payload, "reason", ev.Reason)
	return payload
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
