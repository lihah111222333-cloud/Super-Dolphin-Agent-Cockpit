package taskdag

import "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"

func fromDAGUpsertRow(row sqlc.UpsertTaskDagRow) DAG {
	return fromDAGRaw(row.ID, row.DagKey, row.Version, row.Title, row.Description, row.Status, row.CreatedBy, row.Metadata, row.Trigger, row.CronExpr, row.NextRunAt, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt)
}

func fromDAGGetRow(row sqlc.GetTaskDagRow) DAG {
	return fromDAGRaw(row.ID, row.DagKey, row.Version, row.Title, row.Description, row.Status, row.CreatedBy, row.Metadata, row.Trigger, row.CronExpr, row.NextRunAt, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt)
}

func fromDAGGetForUpdateRow(row sqlc.GetTaskDagForUpdateRow) DAG {
	return fromDAGRaw(row.ID, row.DagKey, row.Version, row.Title, row.Description, row.Status, row.CreatedBy, row.Metadata, row.Trigger, row.CronExpr, row.NextRunAt, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt)
}

func fromDAGRaw(id int64, dagKey string, version int64, title, description, status, createdBy string, metadata []byte, trigger, cronExpr string, nextRunAt, startedAt, finishedAt *int64, createdAt, updatedAt int64) DAG {
	return DAG{
		ID:          id,
		DagKey:      dagKey,
		Version:     version,
		Title:       title,
		Description: description,
		Status:      status,
		CreatedBy:   createdBy,
		Metadata:    metadata,
		Trigger:     trigger,
		CronExpr:    cronExpr,
		NextRunAt:   timestampPtr(nextRunAt),
		StartedAt:   timestampPtr(startedAt),
		FinishedAt:  timestampPtr(finishedAt),
		CreatedAt:   timeValue(createdAt),
		UpdatedAt:   timeValue(updatedAt),
	}
}

func fromNodeUpsertRow(row sqlc.UpsertTaskDagNodeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodePatchConfigRow(row sqlc.PatchTaskDagNodeConfigIfUnchangedRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeStatusFlexibleRow(row sqlc.UpdateTaskDagNodeStatusFlexibleRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeListRow(row sqlc.ListTaskDagNodesRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeAssignRow(row sqlc.AssignTaskDagNodeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeRunListRow(row sqlc.ListTaskDagRunNodesRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeLookupBySpawningThreadRow(row sqlc.LookupNodesBySpawningThreadRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeRunningByAssigneeRow(row sqlc.ListRunningTaskDagNodesByAssigneeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeForUpdateRow(row sqlc.GetTaskDagNodesForUpdateRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeBindTurnRow(row sqlc.BindRunningTaskDagNodeTurnRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeTouchEventRow(row sqlc.TouchRunningTaskDagNodeEventRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeUpdateRunningRow(row sqlc.UpdateRunningTaskDagNodeStatusRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeUpdateAwaitingVerifyRow(row sqlc.UpdateAwaitingVerifyTaskDagNodeStatusRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeCompleteRow(row sqlc.CompleteTaskDagNodeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeClaimOutputRow(row sqlc.ClaimTaskDagNodeOutputMaterializationRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeFailNonTerminalRow(row sqlc.FailTaskDagNodeIfNonTerminalRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.SpawningThreadID)
}

func fromNodeRaw(id int64, dagKey, nodeKey string, runID *int64, title, nodeType, assignedTo string, dependsOn []byte, status, commandRef string, config, result []byte, startedAt, finishedAt *int64, createdAt, updatedAt int64, activeTurnID *string, activeWakeupID *int64, lastEventAt *int64, spawningThreadID *string) Node {
	return Node{
		ID:               id,
		DagKey:           dagKey,
		NodeKey:          nodeKey,
		RunID:            sqlc.Int8Ptr(runID),
		Title:            title,
		NodeType:         nodeType,
		AssignedTo:       assignedTo,
		DependsOn:        dependsOn,
		Status:           status,
		CommandRef:       commandRef,
		Config:           config,
		Result:           result,
		StartedAt:        timestampPtr(startedAt),
		FinishedAt:       timestampPtr(finishedAt),
		CreatedAt:        timeValue(createdAt),
		UpdatedAt:        timeValue(updatedAt),
		ActiveTurnID:     sqlc.TextPtr(activeTurnID),
		ActiveWakeupID:   sqlc.Int8Ptr(activeWakeupID),
		LastEventAt:      timestampPtr(lastEventAt),
		SpawningThreadID: sqlc.TextPtr(spawningThreadID),
	}
}
