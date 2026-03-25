package taskdag

import (
	"context"
	"errors"
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
	row, err := s.q.UpsertTaskDag(ctx, sqlc.UpsertTaskDagParams{
		DagKey:      dag.DagKey,
		Title:       dag.Title,
		Description: dag.Description,
		Status:      dag.Status,
		CreatedBy:   dag.CreatedBy,
		Column6:     dag.Metadata,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "upsert", "task_dag")
	}
	mapped := fromDAG(row)
	return &mapped, nil
}

func (s *store) ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAG, error) {
	rows, err := s.q.ListTaskDags(ctx, sqlc.ListTaskDagsParams{
		Column1: filter.Status,
		Column2: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "list", "task_dag")
	}
	return mapDAGs(rows), nil
}

func (s *store) GetDAG(ctx context.Context, dagKey string) (*DAG, error) {
	row, err := s.q.GetTaskDag(ctx, dagKey)
	if err != nil {
		return nil, wrapTaskDAGError(err, "get", "task_dag")
	}
	mapped := fromDAG(row)
	return &mapped, nil
}

func (s *store) UpsertNode(ctx context.Context, node Node) (*Node, error) {
	row, err := s.q.UpsertTaskDagNode(ctx, sqlc.UpsertTaskDagNodeParams{
		DagKey:     node.DagKey,
		NodeKey:    node.NodeKey,
		Title:      node.Title,
		NodeType:   node.NodeType,
		AssignedTo: node.AssignedTo,
		Column6:    node.DependsOn,
		CommandRef: node.CommandRef,
		Column8:    node.Config,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "upsert", "task_dag_node")
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error) {
	row, err := s.q.UpdateTaskDagNodeStatus(ctx, sqlc.UpdateTaskDagNodeStatusParams{
		Status:  input.Status,
		Column2: input.Result,
		DagKey:  input.DagKey,
		NodeKey: input.NodeKey,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "update_status", "task_dag_node")
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) ListNodes(ctx context.Context, dagKey string) ([]Node, error) {
	rows, err := s.q.ListTaskDagNodes(ctx, dagKey)
	if err != nil {
		return nil, wrapTaskDAGError(err, "list", "task_dag_node")
	}
	return mapNodes(rows), nil
}

func (s *store) ListRunningNodesByAssignee(ctx context.Context, assignee string) ([]Node, error) {
	rows, err := s.q.ListRunningTaskDagNodesByAssignee(ctx, assignee)
	if err != nil {
		return nil, wrapTaskDAGError(err, "list_running_by_assignee", "task_dag_node")
	}
	return mapNodes(rows), nil
}

func (s *store) GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error) {
	row, err := s.q.GetTaskDagForUpdate(ctx, dagKey)
	if err != nil {
		return nil, wrapTaskDAGError(err, "get_for_update", "task_dag")
	}
	mapped := fromDAG(row)
	return &mapped, nil
}

func (s *store) GetNodesForUpdate(ctx context.Context, dagKey string) ([]Node, error) {
	rows, err := s.q.GetTaskDagNodesForUpdate(ctx, dagKey)
	if err != nil {
		return nil, wrapTaskDAGError(err, "get_for_update", "task_dag_node")
	}
	return mapNodes(rows), nil
}

func (s *store) BindRunningNodeTurn(ctx context.Context, input BindRunningNodeTurnInput) (*Node, error) {
	var mapped Node
	err := sqlc.WithTxOrReuse(ctx, s.q, func(txq *sqlc.Queries) error {
		count, err := txq.BindTaskDagWakeupTurn(ctx, sqlc.BindTaskDagWakeupTurnParams{
			BoundTurnID: stringPtr(input.TurnID),
			ID:          input.WakeupID,
		})
		if err != nil {
			return wrapTaskDAGError(err, "bind_turn", "task_dag_wakeup")
		}
		if count == 0 {
			return wrapTaskDAGError(errors.New("wakeup turn binding conflict"), "bind_turn", "task_dag_wakeup")
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
	row, err := s.q.TouchRunningTaskDagNodeEvent(ctx, sqlc.TouchRunningTaskDagNodeEventParams{
		LastEventAt:  sqlc.Timestamptz{Time: input.ObservedAt, Valid: !input.ObservedAt.IsZero()},
		DagKey:       input.DagKey,
		NodeKey:      input.NodeKey,
		ActiveTurnID: stringPtr(input.TurnID),
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "touch_running_event", "task_dag_node")
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error) {
	row, err := s.q.UpdateRunningTaskDagNodeStatus(ctx, sqlc.UpdateRunningTaskDagNodeStatusParams{
		Status:         input.Status,
		Column2:        input.Result,
		ActiveWakeupID: int64Ptr(input.WakeupID),
		DagKey:         input.DagKey,
		NodeKey:        input.NodeKey,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "update_running_status", "task_dag_node")
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) UpdateAwaitingVerifyNodeStatus(ctx context.Context, input AwaitingVerifyNodeStatusUpdate) (*Node, error) {
	row, err := s.q.UpdateAwaitingVerifyTaskDagNodeStatus(ctx, sqlc.UpdateAwaitingVerifyTaskDagNodeStatusParams{
		Status:  input.Status,
		Column2: input.Result,
		DagKey:  input.DagKey,
		NodeKey: input.NodeKey,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "update_awaiting_verify_status", "task_dag_node")
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error) {
	row, err := s.q.CompleteTaskDagNode(ctx, sqlc.CompleteTaskDagNodeParams{
		Status:  input.Status,
		Column2: input.Result,
		DagKey:  input.DagKey,
		NodeKey: input.NodeKey,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "complete", "task_dag_node")
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) UpdateNodeStatusFlexible(ctx context.Context, input FlexibleNodeStatusUpdate) (*Node, error) {
	row, err := s.q.UpdateTaskDagNodeStatusFlexible(ctx, sqlc.UpdateTaskDagNodeStatusFlexibleParams{
		Status:  input.Status,
		Column2: input.Result,
		DagKey:  input.DagKey,
		NodeKey: input.NodeKey,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "update_status_flexible", "task_dag_node")
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func int64Ptr(value int64) sqlc.Int8 {
	return sqlc.Int8ValuePtr(&value)
}

func stringPtr(value string) sqlc.Text {
	return sqlc.TextValuePtr(&value)
}

func mapDAGs(rows []sqlc.TaskDag) []DAG {
	out := make([]DAG, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromDAG(row))
	}
	return out
}

func mapNodes(rows []sqlc.TaskDagNode) []Node {
	out := make([]Node, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromNode(row))
	}
	return out
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
