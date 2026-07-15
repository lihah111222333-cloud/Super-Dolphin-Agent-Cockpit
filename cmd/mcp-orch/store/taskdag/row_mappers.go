package taskdag

import (
	"encoding/json"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
)

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

// fromNodeGetRow 把 GetTaskDagNodeRow 投影成 contract Node。
func fromNodeGetRow(row sqlc.GetTaskDagNodeRow) Node {
	return fromNodeRaw(row.ID, row.DagKey, row.NodeKey, row.RunID, row.Title, row.NodeType, row.AssignedTo, row.DependsOn, row.Status, row.CommandRef, row.Config, row.Result, row.StartedAt, row.FinishedAt, row.CreatedAt, row.UpdatedAt, row.ActiveTurnID, row.ActiveWakeupID, row.LastEventAt, row.Reads, row.Writes, row.SpawningThreadID)
}

// fromNodeListRow 把 ListTaskDagNodesRow 投影成 contract Node。
func fromNodeListRow(row sqlc.ListTaskDagNodesRow) Node {
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
