package taskdag

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

// WithTx only scopes a pool-backed SQL transaction and rebinds sqlc queries.
// Unlike V2's DAG-specific WithDAGTx helper, it does not pre-lock the DAG row
// or node rows with FOR UPDATE; callers must explicitly use the *_ForUpdate
// accessors inside the transaction when they need serialized DAG mutation.
func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
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

func (s *store) UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error) {
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.UpdateTaskDagNodeStatus(ctx, sqlc.UpdateTaskDagNodeStatusParams{
			Status:  input.Status,
			Column2: input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
		})
	}, "update_status")
}

func (s *store) ListNodes(ctx context.Context, dagKey string) ([]Node, error) {
	return queryMany(func() ([]sqlc.TaskDagNode, error) {
		return s.q.ListTaskDagNodes(ctx, dagKey)
	}, "list", "task_dag_node", fromNode)
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
	return queryOne(func() (sqlc.TaskDagNode, error) {
		return s.q.TouchRunningTaskDagNodeEvent(ctx, sqlc.TouchRunningTaskDagNodeEventParams{
			LastEventAt:  sqlc.Timestamptz{Time: input.ObservedAt, Valid: !input.ObservedAt.IsZero()},
			DagKey:       input.DagKey,
			NodeKey:      input.NodeKey,
			ActiveTurnID: stringPtr(input.TurnID),
		})
	}, "touch_running_event", "task_dag_node", fromNode)
}

func (s *store) UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error) {
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.UpdateRunningTaskDagNodeStatus(ctx, sqlc.UpdateRunningTaskDagNodeStatusParams{
			Status:         input.Status,
			Column2:        input.Result,
			ActiveWakeupID: int64Ptr(input.WakeupID),
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
		})
	}, "update_running_status")
}

func (s *store) UpdateAwaitingVerifyNodeStatus(ctx context.Context, input AwaitingVerifyNodeStatusUpdate) (*Node, error) {
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.UpdateAwaitingVerifyTaskDagNodeStatus(ctx, sqlc.UpdateAwaitingVerifyTaskDagNodeStatusParams{
			Status:  input.Status,
			Column2: input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
		})
	}, "update_awaiting_verify_status")
}

func (s *store) CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error) {
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.CompleteTaskDagNode(ctx, sqlc.CompleteTaskDagNodeParams{
			Status:  input.Status,
			Column2: input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
		})
	}, "complete")
}

func (s *store) UpdateNodeStatusFlexible(ctx context.Context, input FlexibleNodeStatusUpdate) (*Node, error) {
	return updateNodeStatus(func() (sqlc.TaskDagNode, error) {
		return s.q.UpdateTaskDagNodeStatusFlexible(ctx, sqlc.UpdateTaskDagNodeStatusFlexibleParams{
			Status:  input.Status,
			Column2: input.Result,
			DagKey:  input.DagKey,
			NodeKey: input.NodeKey,
		})
	}, "update_status_flexible")
}

func int64Ptr(value int64) sqlc.Int8 {
	return sqlc.Int8ValuePtr(&value)
}

func stringPtr(value string) sqlc.Text {
	return sqlc.TextValuePtr(&value)
}

func mapDAGs(rows []sqlc.TaskDag) []DAG {
	return mapRows(rows, fromDAG)
}

func mapNodes(rows []sqlc.TaskDagNode) []Node {
	return mapRows(rows, fromNode)
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
		StartedAt:   timestampPtr(row.StartedAt),
		FinishedAt:  timestampPtr(row.FinishedAt),
		CreatedAt:   timeValue(row.CreatedAt),
		UpdatedAt:   timeValue(row.UpdatedAt),
	}
}

func fromNode(row sqlc.TaskDagNode) Node {
	return Node{
		ID:             row.ID,
		DagKey:         row.DagKey,
		NodeKey:        row.NodeKey,
		Title:          row.Title,
		NodeType:       row.NodeType,
		AssignedTo:     row.AssignedTo,
		DependsOn:      row.DependsOn,
		Status:         row.Status,
		CommandRef:     row.CommandRef,
		Config:         row.Config,
		Result:         row.Result,
		StartedAt:      timestampPtr(row.StartedAt),
		FinishedAt:     timestampPtr(row.FinishedAt),
		CreatedAt:      timeValue(row.CreatedAt),
		UpdatedAt:      timeValue(row.UpdatedAt),
		ActiveTurnID:   sqlc.TextPtr(row.ActiveTurnID),
		ActiveWakeupID: sqlc.Int8Ptr(row.ActiveWakeupID),
		LastEventAt:    timestampPtr(row.LastEventAt),
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
