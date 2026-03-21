package eventsurface

import (
	"encoding/json"
	"strings"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	workspacedto "github.com/anthropic-ai/super-agent-v3/internal/dto/workspace"
)

func workspaceCreatedPayload(ev workspacedto.WorkspaceRunCreated) map[string]any {
	return map[string]any{
		"runKey": ev.RunKey,
		"run": workspaceRunPayload(
			ev.ID,
			ev.RunKey,
			ev.DagKey,
			ev.SourceRoot,
			ev.WorkspacePath,
			firstNonEmpty(ev.Status, "active"),
			ev.CreatedBy,
			firstNonEmpty(ev.UpdatedBy, ev.CreatedBy),
			ev.Metadata,
			ev.CreatedAt,
			ev.UpdatedAt,
			ev.FinishedAt,
			ev.Timestamp,
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

func workspaceStatusChangedPayload(ev workspacedto.WorkspaceRunStatusChanged) map[string]any {
	payload := map[string]any{"runKey": strings.TrimSpace(ev.RunKey)}
	setString(payload, "oldStatus", ev.OldStatus)
	setString(payload, "newStatus", ev.NewStatus)
	setString(payload, "updatedBy", ev.UpdatedBy)
	return payload
}

func workspaceAbortedPayload(ev workspacedto.WorkspaceRunAborted) map[string]any {
	return map[string]any{
		"runKey": ev.RunKey,
		"reason": strings.TrimSpace(ev.Reason),
		"run": workspaceRunPayload(
			0,
			ev.RunKey,
			ev.DagKey,
			ev.SourceRoot,
			ev.WorkspacePath,
			firstNonEmpty(ev.Status, "aborted"),
			"",
			ev.UpdatedBy,
			nil,
			time.Time{},
			time.Time{},
			nil,
			ev.Timestamp,
		),
	}
}

func workspaceMergeErrorPayload(ev workspacedto.WorkspaceRunMergeError) map[string]any {
	payload := map[string]any{"runKey": strings.TrimSpace(ev.RunKey)}
	setString(payload, "sourceRoot", ev.SourceRoot)
	setString(payload, "workspacePath", ev.WorkspacePath)
	setString(payload, "message", ev.Message)
	setString(payload, "updatedBy", ev.UpdatedBy)
	payload["conflicts"] = ev.Conflicts
	payload["errors"] = ev.Errors
	return payload
}

func agentLaunchedPayload(ev agentdto.AgentLaunched) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "cwd", ev.CWD)
	setString(payload, "model", ev.Model)
	return payload
}

func agentStoppedPayload(ev agentdto.AgentStopped) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "reason", ev.Reason)
	return payload
}

func agentRecoveringPayload(ev agentdto.AgentRecovering) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "reason", ev.Reason)
	if ev.Attempt > 0 {
		payload["attempt"] = ev.Attempt
	}
	return payload
}

func agentFailedPayload(ev agentdto.AgentFailed) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "error", ev.Error)
	if ev.Recoverable {
		payload["recoverable"] = true
	}
	return payload
}

func agentRuntimeReportedPayload(ev agentdto.AgentRuntimeReported) map[string]any {
	payload := agentSessionPayload(ev.AgentSessionHeader)
	setString(payload, "provider", ev.Provider)
	if ev.Port > 0 {
		payload["port"] = ev.Port
	}
	return payload
}

func workspaceRunPayload(
	id int64,
	runKey, dagKey, sourceRoot, workspacePath, status, createdBy, updatedBy string,
	metadata json.RawMessage,
	createdAt, updatedAt time.Time,
	finishedAt *time.Time,
	timestamp time.Time,
) map[string]any {
	payload := map[string]any{"runKey": strings.TrimSpace(runKey)}
	if id > 0 {
		payload["id"] = id
	}
	setString(payload, "dagKey", dagKey)
	setString(payload, "sourceRoot", sourceRoot)
	setString(payload, "workspacePath", workspacePath)
	setString(payload, "status", status)
	setString(payload, "createdBy", createdBy)
	setString(payload, "updatedBy", updatedBy)
	if len(metadata) != 0 {
		payload["metadata"] = append(json.RawMessage(nil), metadata...)
	}
	if createdAt.IsZero() {
		createdAt = timestamp
	}
	if updatedAt.IsZero() {
		updatedAt = timestamp
	}
	if !createdAt.IsZero() {
		payload["createdAt"] = createdAt
	}
	if !updatedAt.IsZero() {
		payload["updatedAt"] = updatedAt
	}
	if finishedAt != nil {
		payload["finishedAt"] = *finishedAt
	}
	return payload
}
