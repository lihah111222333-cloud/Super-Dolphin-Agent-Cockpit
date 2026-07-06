package taskdag

import (
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// fromDAGUpsertRow 把 UpsertTaskDagRow 投影成 contract DAG。
func fromDAGUpsertRow(row sqlc.UpsertTaskDagRow) DAG {
	return fromDAGRaw(row.ID, row.DagKey, row.Version, row.Title, row.Description, row.Status, row.CreatedBy, row.Metadata, row.Trigger, row.CronExpr, row.NextRunAt, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt)
}

// fromDAGGetRow 把 GetTaskDagRow 投影成 contract DAG。
func fromDAGGetRow(row sqlc.GetTaskDagRow) DAG {
	return fromDAGRaw(row.ID, row.DagKey, row.Version, row.Title, row.Description, row.Status, row.CreatedBy, row.Metadata, row.Trigger, row.CronExpr, row.NextRunAt, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt)
}

// fromDAGGetForUpdateRow 把 GetTaskDagForUpdateRow 投影成 contract DAG。
func fromDAGGetForUpdateRow(row sqlc.GetTaskDagForUpdateRow) DAG {
	return fromDAGRaw(row.ID, row.DagKey, row.Version, row.Title, row.Description, row.Status, row.CreatedBy, row.Metadata, row.Trigger, row.CronExpr, row.NextRunAt, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt)
}

// fromDAGRaw 是所有 DAG 行映射路径的公共构造函数。
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

// fromNodeUpsertRow 把 UpsertTaskDagNodeRow 投影成 contract Node。
func fromNodeUpsertRow(row sqlc.UpsertTaskDagNodeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodePatchConfigRow 把 PatchTaskDagNodeConfigIfUnchangedRow 投影成 contract Node。
func fromNodePatchConfigRow(row sqlc.PatchTaskDagNodeConfigIfUnchangedRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeStatusIfCurrentRow 把 UpdateTaskDagNodeStatusIfCurrentRow 投影成 contract Node。
func fromNodeStatusIfCurrentRow(row sqlc.UpdateTaskDagNodeStatusIfCurrentRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeListRow 把 ListTaskDagNodesRow 投影成 contract Node。
func fromNodeListRow(row sqlc.ListTaskDagNodesRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeAssignRow 把 AssignTaskDagNodeRow 投影成 contract Node。
func fromNodeAssignRow(row sqlc.AssignTaskDagNodeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeRunListRow 把 ListTaskDagRunNodesRow 投影成 contract Node。
func fromNodeRunListRow(row sqlc.ListTaskDagRunNodesRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeRunForUpdateRow 把 GetTaskDagRunNodeForUpdateRow 投影成 contract Node。
func fromNodeRunForUpdateRow(row sqlc.GetTaskDagRunNodeForUpdateRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeLookupBySpawningThreadRow 把 LookupNodesBySpawningThreadRow 投影成 contract Node。
func fromNodeLookupBySpawningThreadRow(row sqlc.LookupNodesBySpawningThreadRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeRunningByAssigneeRow 把 ListRunningTaskDagNodesByAssigneeRow 投影成 contract Node。
func fromNodeRunningByAssigneeRow(row sqlc.ListRunningTaskDagNodesByAssigneeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeForUpdateRow 把 GetTaskDagNodesForUpdateRow 投影成 contract Node。
func fromNodeForUpdateRow(row sqlc.GetTaskDagNodesForUpdateRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeBindTurnRow 把 BindRunningTaskDagNodeTurnRow 投影成 contract Node。
func fromNodeBindTurnRow(row sqlc.BindRunningTaskDagNodeTurnRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeTouchEventRow 把 TouchRunningTaskDagNodeEventRow 投影成 contract Node。
func fromNodeTouchEventRow(row sqlc.TouchRunningTaskDagNodeEventRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeUpdateRunningRow 把 UpdateRunningTaskDagNodeStatusRow 投影成 contract Node。
func fromNodeUpdateRunningRow(row sqlc.UpdateRunningTaskDagNodeStatusRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeCompleteRow 把 CompleteTaskDagNodeRow 投影成 contract Node。
func fromNodeCompleteRow(row sqlc.CompleteTaskDagNodeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeClaimOutputRow 把 ClaimTaskDagNodeOutputMaterializationRow 投影成 contract Node。
func fromNodeClaimOutputRow(row sqlc.ClaimTaskDagNodeOutputMaterializationRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeFailNonTerminalRow 把 FailTaskDagNodeIfNonTerminalRow 投影成 contract Node。
func fromNodeFailNonTerminalRow(row sqlc.FailTaskDagNodeIfNonTerminalRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeRaw 是所有节点行映射路径的公共构造函数，把原始列值转为 contract Node。
func fromNodeRaw(id int64, dagKey, nodeKey string, runID *int64, title, nodeType, assignedTo string, dependsOn []byte, status, commandRef string, config, result []byte, startedAt, finishedAt *int64, createdAt, updatedAt int64, activeTurnID *string, activeWakeupID *int64, lastEventAt *int64, reads, writes []byte, spawningThreadID *string) Node {
	return Node{
		ID:               id,
		DagKey:           dagKey,
		NodeKey:          nodeKey,
		RunID:            sqlc.Int8Ptr(runID),
		Title:            title,
		NodeType:         nodeType,
		AssignedTo:       assignedTo,
		DependsOn:        dependsOn,
		Reads:            nodeStringSlice(reads),
		Writes:           nodeStringSlice(writes),
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

func nodeStringSlice(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil
	}
	return append([]string(nil), values...)
}
