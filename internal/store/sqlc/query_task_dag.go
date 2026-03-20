package sqlc

import "context"

const (
	upsertTaskDagSQL                         = `INSERT INTO task_dags (dag_key, title, description, status, created_by, metadata) VALUES ($1, $2, $3, $4, $5, $6::jsonb) ON CONFLICT (dag_key) DO UPDATE SET title = EXCLUDED.title, description = EXCLUDED.description, status = EXCLUDED.status, created_by = EXCLUDED.created_by, metadata = EXCLUDED.metadata, updated_at = NOW() RETURNING id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at;`
	listTaskDagsSQL                          = `SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at FROM task_dags WHERE ($1::text = '' OR status = $1) AND ($2::text = '' OR dag_key ILIKE '%' || $2 || '%' OR title ILIKE '%' || $2 || '%' OR description ILIKE '%' || $2 || '%') ORDER BY updated_at DESC, id DESC LIMIT $3;`
	getTaskDagSQL                            = `SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at FROM task_dags WHERE dag_key = $1;`
	upsertTaskDagNodeSQL                     = `INSERT INTO task_dag_nodes (dag_key, node_key, title, node_type, assigned_to, depends_on, command_ref, config) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7, $8::jsonb) ON CONFLICT (dag_key, node_key) DO UPDATE SET title = EXCLUDED.title, node_type = EXCLUDED.node_type, assigned_to = EXCLUDED.assigned_to, depends_on = EXCLUDED.depends_on, command_ref = EXCLUDED.command_ref, config = EXCLUDED.config, updated_at = NOW() RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;`
	updateTaskDagNodeStatusSQL               = `UPDATE task_dag_nodes SET status = $1, result = $2::jsonb, updated_at = NOW() WHERE dag_key = $3 AND node_key = $4 RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;`
	listTaskDagNodesSQL                      = `SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at FROM task_dag_nodes WHERE dag_key = $1 ORDER BY created_at;`
	listRunningTaskDagNodesByAssigneeSQL     = `SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at FROM task_dag_nodes WHERE assigned_to = $1 AND status = 'running' ORDER BY created_at;`
	getTaskDagForUpdateSQL                   = `SELECT id, dag_key, title, description, status, created_by, metadata, started_at, finished_at, created_at, updated_at FROM task_dags WHERE dag_key = $1 FOR UPDATE;`
	getTaskDagNodesForUpdateSQL              = `SELECT id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at FROM task_dag_nodes WHERE dag_key = $1 ORDER BY created_at, id FOR UPDATE;`
	bindRunningTaskDagNodeTurnSQL            = `UPDATE task_dag_nodes SET active_turn_id = $1, last_event_at = NOW(), updated_at = NOW() WHERE dag_key = $2 AND node_key = $3 AND status = 'running' AND active_turn_id IS NULL AND active_wakeup_id = $4 RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;`
	touchRunningTaskDagNodeEventSQL          = `UPDATE task_dag_nodes SET last_event_at = $1, updated_at = NOW() WHERE dag_key = $2 AND node_key = $3 AND status = 'running' AND active_turn_id = $4 AND (last_event_at IS NULL OR last_event_at < $1) RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;`
	updateRunningTaskDagNodeStatusSQL        = `UPDATE task_dag_nodes SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = $3, last_event_at = NULL, started_at = COALESCE(started_at, NOW()), updated_at = NOW() WHERE dag_key = $4 AND node_key = $5 AND status IN ('pending') RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;`
	updateAwaitingVerifyTaskDagNodeStatusSQL = `UPDATE task_dag_nodes SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = NULL, updated_at = NOW() WHERE dag_key = $3 AND node_key = $4 AND status IN ('running') RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;`
	completeTaskDagNodeSQL                   = `UPDATE task_dag_nodes SET status = $1, result = $2::jsonb, active_turn_id = NULL, active_wakeup_id = NULL, finished_at = COALESCE(finished_at, NOW()), updated_at = NOW() WHERE dag_key = $3 AND node_key = $4 AND status IN ('running', 'awaiting_verify') RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;`
	updateTaskDagNodeStatusFlexibleSQL       = `UPDATE task_dag_nodes SET status = $1, result = $2::jsonb, updated_at = NOW() WHERE dag_key = $3 AND node_key = $4 RETURNING id, dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at;`
)

func scanTaskDag(row rowScanner) (TaskDag, error) {
	var item TaskDag
	err := row.Scan(&item.ID, &item.DagKey, &item.Title, &item.Description, &item.Status, &item.CreatedBy, &item.Metadata, &item.StartedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func scanTaskDagNode(row rowScanner) (TaskDagNode, error) {
	var item TaskDagNode
	err := row.Scan(&item.ID, &item.DagKey, &item.NodeKey, &item.Title, &item.NodeType, &item.AssignedTo, &item.DependsOn, &item.Status, &item.CommandRef, &item.Config, &item.Result, &item.StartedAt, &item.FinishedAt, &item.CreatedAt, &item.UpdatedAt, &item.ActiveTurnID, &item.ActiveWakeupID, &item.LastEventAt)
	return item, err
}

func (q *Queries) UpsertTaskDag(ctx context.Context, arg UpsertTaskDagParams) (TaskDag, error) {
	return queryOne(ctx, q, upsertTaskDagSQL, scanTaskDag, arg.DagKey, arg.Title, arg.Description, arg.Status, arg.CreatedBy, arg.Metadata)
}

func (q *Queries) ListTaskDags(ctx context.Context, arg ListTaskDagsParams) ([]TaskDag, error) {
	return queryMany(ctx, q, listTaskDagsSQL, scanTaskDag, arg.Status, arg.Keyword, arg.Limit)
}

func (q *Queries) GetTaskDag(ctx context.Context, dagKey string) (TaskDag, error) {
	return queryOne(ctx, q, getTaskDagSQL, scanTaskDag, dagKey)
}

func (q *Queries) UpsertTaskDagNode(ctx context.Context, arg UpsertTaskDagNodeParams) (TaskDagNode, error) {
	return queryOne(ctx, q, upsertTaskDagNodeSQL, scanTaskDagNode, arg.DagKey, arg.NodeKey, arg.Title, arg.NodeType, arg.AssignedTo, arg.DependsOn, arg.CommandRef, arg.Config)
}

func (q *Queries) UpdateTaskDagNodeStatus(ctx context.Context, arg UpdateTaskDagNodeStatusParams) (TaskDagNode, error) {
	return queryOne(ctx, q, updateTaskDagNodeStatusSQL, scanTaskDagNode, arg.Status, arg.Result, arg.DagKey, arg.NodeKey)
}

func (q *Queries) ListTaskDagNodes(ctx context.Context, dagKey string) ([]TaskDagNode, error) {
	return queryMany(ctx, q, listTaskDagNodesSQL, scanTaskDagNode, dagKey)
}

func (q *Queries) ListRunningTaskDagNodesByAssignee(ctx context.Context, assignee string) ([]TaskDagNode, error) {
	return queryMany(ctx, q, listRunningTaskDagNodesByAssigneeSQL, scanTaskDagNode, assignee)
}

func (q *Queries) GetTaskDagForUpdate(ctx context.Context, dagKey string) (TaskDag, error) {
	return queryOne(ctx, q, getTaskDagForUpdateSQL, scanTaskDag, dagKey)
}

func (q *Queries) GetTaskDagNodesForUpdate(ctx context.Context, dagKey string) ([]TaskDagNode, error) {
	return queryMany(ctx, q, getTaskDagNodesForUpdateSQL, scanTaskDagNode, dagKey)
}

func (q *Queries) BindRunningTaskDagNodeTurn(ctx context.Context, arg BindRunningTaskDagNodeTurnParams) (TaskDagNode, error) {
	return queryOne(ctx, q, bindRunningTaskDagNodeTurnSQL, scanTaskDagNode, arg.TurnID, arg.DagKey, arg.NodeKey, arg.WakeupID)
}

func (q *Queries) TouchRunningTaskDagNodeEvent(ctx context.Context, arg TouchRunningTaskDagNodeEventParams) (TaskDagNode, error) {
	return queryOne(ctx, q, touchRunningTaskDagNodeEventSQL, scanTaskDagNode, arg.ObservedAt, arg.DagKey, arg.NodeKey, arg.TurnID)
}

func (q *Queries) UpdateRunningTaskDagNodeStatus(ctx context.Context, arg UpdateRunningTaskDagNodeStatusParams) (TaskDagNode, error) {
	return queryOne(ctx, q, updateRunningTaskDagNodeStatusSQL, scanTaskDagNode, arg.Status, arg.Result, arg.WakeupID, arg.DagKey, arg.NodeKey)
}

func (q *Queries) UpdateAwaitingVerifyTaskDagNodeStatus(ctx context.Context, arg UpdateAwaitingVerifyTaskDagNodeStatusParams) (TaskDagNode, error) {
	return queryOne(ctx, q, updateAwaitingVerifyTaskDagNodeStatusSQL, scanTaskDagNode, arg.Status, arg.Result, arg.DagKey, arg.NodeKey)
}

func (q *Queries) CompleteTaskDagNode(ctx context.Context, arg CompleteTaskDagNodeParams) (TaskDagNode, error) {
	return queryOne(ctx, q, completeTaskDagNodeSQL, scanTaskDagNode, arg.Status, arg.Result, arg.DagKey, arg.NodeKey)
}

func (q *Queries) UpdateTaskDagNodeStatusFlexible(ctx context.Context, arg UpdateTaskDagNodeStatusFlexibleParams) (TaskDagNode, error) {
	return queryOne(ctx, q, updateTaskDagNodeStatusFlexibleSQL, scanTaskDagNode, arg.Status, arg.Result, arg.DagKey, arg.NodeKey)
}
