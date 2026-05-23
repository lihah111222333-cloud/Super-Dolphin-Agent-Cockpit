package taskdag

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func requireRuntimeRunID(op string, runID int64) error {
	if runID <= 0 {
		return fmt.Errorf("%s: run_id required", op)
	}
	return nil
}

// WithTx only scopes a pool-backed SQL transaction and rebinds sqlc queries.
// Unlike V2's DAG-specific WithDAGTx helper, it does not pre-lock the DAG row
// or node rows with FOR UPDATE; callers must explicitly use the *_ForUpdate
// accessors inside the transaction when they need serialized DAG mutation.
func (s *store) WithTx(ctx context.Context, fn func(txStore DAGMutationStore) error) error {
	return wrapTaskDAGError(sqlc.WithTx(ctx, s.q, func(txq *sqlc.Queries) error {
		return fn(&store{q: txq})
	}), "with_tx", "task_dag")
}

func (s *store) UpsertDAG(ctx context.Context, dag DAG) (*DAG, error) {
	return queryOne(func() (sqlc.TaskDag, error) {
		return s.q.UpsertTaskDag(ctx, sqlc.UpsertTaskDagParams{
			DagKey:      dag.DagKey,
			Title:       dag.Title,
			Description: dag.Description,
			Status:      dag.Status,
			CreatedBy:   dag.CreatedBy,
			Column6:     dag.Metadata,
		})
	}, "upsert", "task_dag", fromDAG)
}

func (s *store) ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAG, error) {
	return queryMany(func() ([]sqlc.TaskDag, error) {
		return s.q.ListTaskDags(ctx, sqlc.ListTaskDagsParams{
			Column1: filter.Status,
			Column2: filter.Keyword,
			Limit:   filter.Limit,
		})
	}, "list", "task_dag", fromDAG)
}

func (s *store) GetDAG(ctx context.Context, dagKey string) (*DAG, error) {
	return queryOne(func() (sqlc.TaskDag, error) {
		return s.q.GetTaskDag(ctx, dagKey)
	}, "get", "task_dag", fromDAG)
}

func (s *store) UpsertNode(ctx context.Context, node Node) (*Node, error) {
	return queryOne(func() (sqlc.TaskDagNode, error) {
		return s.q.UpsertTaskDagNode(ctx, sqlc.UpsertTaskDagNodeParams{
			DagKey:     node.DagKey,
			NodeKey:    node.NodeKey,
			Title:      node.Title,
			NodeType:   node.NodeType,
			AssignedTo: node.AssignedTo,
			Column6:    node.DependsOn,
			CommandRef: node.CommandRef,
			Column8:    node.Config,
		})
	}, "upsert", "task_dag_node", fromNode)
}

func (s *store) PatchNodeConfigIfUnchanged(ctx context.Context, input NodeConfigPatchInput) (*Node, error) {
	if err := requireRuntimeRunID("patch_config", input.RunID); err != nil {
		return nil, err
	}
	return queryOne(func() (sqlc.TaskDagNode, error) {
		return s.q.PatchTaskDagNodeConfigIfUnchanged(ctx, sqlc.PatchTaskDagNodeConfigIfUnchangedParams{
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			Config:         input.Config,
			PreviousConfig: input.PreviousConfig,
			RunID:          input.RunID,
		})
	}, "patch_config", "task_dag_node", fromNode)
}

func (s *store) DeleteNode(ctx context.Context, dagKey, nodeKey string) (int64, error) {
	rows, err := s.q.DeleteTaskDagNode(ctx, sqlc.DeleteTaskDagNodeParams{
		DagKey:  dagKey,
		NodeKey: nodeKey,
	})
	return rows, wrapTaskDAGError(err, "delete", "task_dag_node")
}

func (s *store) UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_status", input.RunID); err != nil {
		return nil, err
	}
	// R1 dead code 清理：原 2 份 SQL（UpdateTaskDagNodeStatus / UpdateTaskDagNodeStatusFlexible）
	// 逻辑上完全重复，合并为 Flexible 一份。本函数保留为发布层 sentinel（NodeStatusUpdate
	// 与 FlexibleNodeStatusUpdate 输入名字不同），但底层走同一 query。
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.UpdateTaskDagNodeStatusFlexible(ctx, sqlc.UpdateTaskDagNodeStatusFlexibleParams{
			Status:  input.Status,
			Column2: input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   input.RunID,
		})
	}, "update_status")
}

func (s *store) ListNodes(ctx context.Context, dagKey string) ([]Node, error) {
	return queryMany(func() ([]sqlc.TaskDagNode, error) {
		return s.q.ListTaskDagNodes(ctx, dagKey)
	}, "list", "task_dag_node", fromNode)
}

func (s *store) AssignNode(ctx context.Context, input AssignNodeInput) (*Node, error) {
	if err := requireRuntimeRunID("assign", input.RunID); err != nil {
		return nil, err
	}
	return queryOne(func() (sqlc.TaskDagNode, error) {
		return s.q.AssignTaskDagNode(ctx, sqlc.AssignTaskDagNodeParams{
			AssignedTo: input.AssignedTo,
			DagKey:     input.DagKey,
			NodeKey:    input.NodeKey,
			RunID:      input.RunID,
		})
	}, "assign", "task_dag_node", fromNode)
}

func (s *store) ListRunNodes(ctx context.Context, dagKey string, runID int64) ([]Node, error) {
	return queryMany(func() ([]sqlc.TaskDagNode, error) {
		return s.q.ListTaskDagRunNodes(ctx, sqlc.ListTaskDagRunNodesParams{
			DagKey: dagKey,
			RunID:  runID,
		})
	}, "list_run", "task_dag_node", fromNode)
}

// LookupNodesBySpawningThread reverses task_dag_nodes.spawning_thread_id back
// to the node rows that spawned the given child thread id. ADR-017 v1.2 §2.2.
//
// Empty result slice (not platformdb.ErrNotFound) means no node currently
// carries this thread id. N>1 results are normal on retry / recovery chains
// (migration 0083 partial index has no UNIQUE clause + F1.5 write entry-point
// is not single-writer); the caller iterates and applies idempotent
// advancement on every row.
func (s *store) LookupNodesBySpawningThread(ctx context.Context, threadID string) ([]Node, error) {
	return queryMany(func() ([]sqlc.TaskDagNode, error) {
		return s.q.LookupNodesBySpawningThread(ctx, threadID)
	}, "lookup_by_spawning_thread", "task_dag_node", fromNode)
}

func (s *store) ListRunningNodesByAssignee(ctx context.Context, assignee string) ([]Node, error) {
	return queryMany(func() ([]sqlc.TaskDagNode, error) {
		return s.q.ListRunningTaskDagNodesByAssignee(ctx, assignee)
	}, "list_running_by_assignee", "task_dag_node", fromNode)
}

func (s *store) GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error) {
	return queryOne(func() (sqlc.TaskDag, error) {
		return s.q.GetTaskDagForUpdate(ctx, dagKey)
	}, "get_for_update", "task_dag", fromDAG)
}

func (s *store) GetNodesForUpdate(ctx context.Context, dagKey string) ([]Node, error) {
	return queryMany(func() ([]sqlc.TaskDagNode, error) {
		return s.q.GetTaskDagNodesForUpdate(ctx, dagKey)
	}, "get_for_update", "task_dag_node", fromNode)
}

func (s *store) BindRunningNodeTurn(ctx context.Context, input BindRunningNodeTurnInput) (*Node, error) {
	if err := requireRuntimeRunID("bind_running_turn", input.RunID); err != nil {
		return nil, err
	}
	var mapped Node
	err := sqlc.WithTxOrReuse(ctx, s.q, func(txq *sqlc.Queries) error {
		_, err := bindWakeupTurnTx(ctx, txq, BindWakeupTurnInput{
			TurnID: input.TurnID,
			ID:     input.WakeupID,
		}, true)
		if err != nil {
			return err
		}
		row, err := txq.BindRunningTaskDagNodeTurn(ctx, sqlc.BindRunningTaskDagNodeTurnParams{
			ActiveTurnID:   stringPtr(input.TurnID),
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			ActiveWakeupID: int64Ptr(input.WakeupID),
			RunID:          input.RunID,
		})
		if err != nil {
			return wrapTaskDAGError(err, "bind_running_turn", "task_dag_node")
		}
		mapped = fromNode(row)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &mapped, nil
}

func (s *store) TouchRunningNodeEvent(ctx context.Context, input TouchRunningNodeEventInput) (*Node, error) {
	if err := requireRuntimeRunID("touch_running_event", input.RunID); err != nil {
		return nil, err
	}
	return queryOne(func() (sqlc.TaskDagNode, error) {
		return s.q.TouchRunningTaskDagNodeEvent(ctx, sqlc.TouchRunningTaskDagNodeEventParams{
			LastEventAt:  sqlc.Timestamptz{Time: input.ObservedAt, Valid: !input.ObservedAt.IsZero()},
			DagKey:       input.DagKey,
			NodeKey:      input.NodeKey,
			ActiveTurnID: stringPtr(input.TurnID),
			RunID:        input.RunID,
		})
	}, "touch_running_event", "task_dag_node", fromNode)
}

func (s *store) UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_running_status", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.UpdateRunningTaskDagNodeStatus(ctx, sqlc.UpdateRunningTaskDagNodeStatusParams{
			Status:         input.Status,
			Column2:        input.Result,
			ActiveWakeupID: int64Ptr(input.WakeupID),
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			RunID:          input.RunID,
		})
	}, "update_running_status")
}

func (s *store) UpdateAwaitingVerifyNodeStatus(ctx context.Context, input AwaitingVerifyNodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_awaiting_verify_status", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.UpdateAwaitingVerifyTaskDagNodeStatus(ctx, sqlc.UpdateAwaitingVerifyTaskDagNodeStatusParams{
			Status:  input.Status,
			Column2: input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   input.RunID,
		})
	}, "update_awaiting_verify_status")
}

func (s *store) CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error) {
	if err := requireRuntimeRunID("complete", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.CompleteTaskDagNode(ctx, sqlc.CompleteTaskDagNodeParams{
			Status:  input.Status,
			Column2: input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   input.RunID,
		})
	}, "complete")
}

func (s *store) UpdateNodeStatusFlexible(ctx context.Context, input FlexibleNodeStatusUpdate) (*Node, error) {
	if err := requireRuntimeRunID("update_status_flexible", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.UpdateTaskDagNodeStatusFlexible(ctx, sqlc.UpdateTaskDagNodeStatusFlexibleParams{
			Status:  input.Status,
			Column2: input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   input.RunID,
		})
	}, "update_status_flexible")
}

func (s *store) ClaimNodeOutputMaterialization(ctx context.Context, input OutputMaterializationClaimInput) (*Node, error) {
	if err := requireRuntimeRunID("claim_output_materialization", input.RunID); err != nil {
		return nil, err
	}
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.ClaimTaskDagNodeOutputMaterialization(ctx, sqlc.ClaimTaskDagNodeOutputMaterializationParams{
			Result:  input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
			RunID:   input.RunID,
		})
	}, "claim_output_materialization")
}

func int64Ptr(value int64) sqlc.Int8 {
	return sqlc.Int8ValuePtr(&value)
}

func stringPtr(value string) sqlc.Text {
	return sqlc.TextValuePtr(&value)
}

func fromDAG(row sqlc.TaskDag) DAG {
	return DAG{
		ID:          row.ID,
		DagKey:      row.DagKey,
		Title:       row.Title,
		Description: row.Description,
		Status:      row.Status,
		CreatedBy:   row.CreatedBy,
		Metadata:    row.Metadata,
		Trigger:     row.Trigger,
		CronExpr:    row.CronExpr,
		StartedAt:   timestampPtr(row.StartedAt),
		FinishedAt:  timestampPtr(row.FinishedAt),
		CreatedAt:   timeValue(row.CreatedAt),
		UpdatedAt:   timeValue(row.UpdatedAt),
	}
}

func fromNode(row sqlc.TaskDagNode) Node {
	return Node{
		ID:               row.ID,
		DagKey:           row.DagKey,
		NodeKey:          row.NodeKey,
		RunID:            sqlc.Int8Ptr(row.RunID),
		Title:            row.Title,
		NodeType:         row.NodeType,
		AssignedTo:       row.AssignedTo,
		DependsOn:        row.DependsOn,
		Status:           row.Status,
		CommandRef:       row.CommandRef,
		Config:           row.Config,
		Result:           row.Result,
		StartedAt:        timestampPtr(row.StartedAt),
		FinishedAt:       timestampPtr(row.FinishedAt),
		CreatedAt:        timeValue(row.CreatedAt),
		UpdatedAt:        timeValue(row.UpdatedAt),
		ActiveTurnID:     sqlc.TextPtr(row.ActiveTurnID),
		ActiveWakeupID:   sqlc.Int8Ptr(row.ActiveWakeupID),
		LastEventAt:      timestampPtr(row.LastEventAt),
		SpawningThreadID: sqlc.TextPtr(row.SpawningThreadID),
	}
}

func wrapTaskDAGError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}

// intervalValue converts textual interval input into the pgtype shape expected by sqlc.
func intervalValue(value string) (sqlc.Interval, error) {
	var interval sqlc.Interval
	if err := interval.Scan(value); err != nil {
		return sqlc.Interval{}, err
	}
	return interval, nil
}

func timeValue(value sqlc.Timestamptz) time.Time {
	return sqlc.TimeValue(value)
}

func timestampPtr(value sqlc.Timestamptz) *time.Time {
	return sqlc.TimePtr(value)
}

func timestampValue(value time.Time) sqlc.Timestamptz {
	return sqlc.Timestamptz{Time: value, Valid: !value.IsZero()}
}
