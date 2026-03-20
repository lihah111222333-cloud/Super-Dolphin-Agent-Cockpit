package taskdag

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	return s.q.WithTx(ctx, func(txq *sqlc.Queries) error {
		return fn(&store{q: txq})
	})
}

func (s *store) UpsertDAG(ctx context.Context, dag DAG) (*DAG, error) {
	row, err := s.q.UpsertTaskDag(ctx, sqlc.UpsertTaskDagParams{DagKey: dag.DagKey, Title: dag.Title, Description: dag.Description, Status: dag.Status, CreatedBy: dag.CreatedBy, Metadata: dag.Metadata})
	if err != nil {
		return nil, err
	}
	mapped := fromDAG(row)
	return &mapped, nil
}

func (s *store) ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAG, error) {
	rows, err := s.q.ListTaskDags(ctx, sqlc.ListTaskDagsParams{Status: filter.Status, Keyword: filter.Keyword, Limit: filter.Limit})
	if err != nil {
		return nil, err
	}
	return mapDAGs(rows), nil
}

func (s *store) GetDAG(ctx context.Context, dagKey string) (*DAG, error) {
	row, err := s.q.GetTaskDag(ctx, dagKey)
	if err != nil {
		return nil, err
	}
	mapped := fromDAG(row)
	return &mapped, nil
}

func (s *store) UpsertNode(ctx context.Context, node Node) (*Node, error) {
	row, err := s.q.UpsertTaskDagNode(ctx, sqlc.UpsertTaskDagNodeParams{DagKey: node.DagKey, NodeKey: node.NodeKey, Title: node.Title, NodeType: node.NodeType, AssignedTo: node.AssignedTo, DependsOn: node.DependsOn, CommandRef: node.CommandRef, Config: node.Config})
	if err != nil {
		return nil, err
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) UpdateNodeStatus(ctx context.Context, input NodeStatusUpdate) (*Node, error) {
	row, err := s.q.UpdateTaskDagNodeStatus(ctx, sqlc.UpdateTaskDagNodeStatusParams{Status: input.Status, Result: input.Result, DagKey: input.DagKey, NodeKey: input.NodeKey})
	if err != nil {
		return nil, err
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) ListNodes(ctx context.Context, dagKey string) ([]Node, error) {
	rows, err := s.q.ListTaskDagNodes(ctx, dagKey)
	if err != nil {
		return nil, err
	}
	return mapNodes(rows), nil
}

func (s *store) ListRunningNodesByAssignee(ctx context.Context, assignee string) ([]Node, error) {
	rows, err := s.q.ListRunningTaskDagNodesByAssignee(ctx, assignee)
	if err != nil {
		return nil, err
	}
	return mapNodes(rows), nil
}

func (s *store) GetDAGForUpdate(ctx context.Context, dagKey string) (*DAG, error) {
	row, err := s.q.GetTaskDagForUpdate(ctx, dagKey)
	if err != nil {
		return nil, err
	}
	mapped := fromDAG(row)
	return &mapped, nil
}

func (s *store) GetNodesForUpdate(ctx context.Context, dagKey string) ([]Node, error) {
	rows, err := s.q.GetTaskDagNodesForUpdate(ctx, dagKey)
	if err != nil {
		return nil, err
	}
	return mapNodes(rows), nil
}

func (s *store) BindRunningNodeTurn(ctx context.Context, input BindRunningNodeTurnInput) (*Node, error) {
	row, err := s.q.BindRunningTaskDagNodeTurn(ctx, sqlc.BindRunningTaskDagNodeTurnParams{TurnID: input.TurnID, DagKey: input.DagKey, NodeKey: input.NodeKey, WakeupID: input.WakeupID})
	if err != nil {
		return nil, err
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) TouchRunningNodeEvent(ctx context.Context, input TouchRunningNodeEventInput) (*Node, error) {
	row, err := s.q.TouchRunningTaskDagNodeEvent(ctx, sqlc.TouchRunningTaskDagNodeEventParams{ObservedAt: input.ObservedAt, DagKey: input.DagKey, NodeKey: input.NodeKey, TurnID: input.TurnID})
	if err != nil {
		return nil, err
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error) {
	row, err := s.q.UpdateRunningTaskDagNodeStatus(ctx, sqlc.UpdateRunningTaskDagNodeStatusParams{Status: input.Status, Result: input.Result, WakeupID: input.WakeupID, DagKey: input.DagKey, NodeKey: input.NodeKey})
	if err != nil {
		return nil, err
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) UpdateAwaitingVerifyNodeStatus(ctx context.Context, input AwaitingVerifyNodeStatusUpdate) (*Node, error) {
	row, err := s.q.UpdateAwaitingVerifyTaskDagNodeStatus(ctx, sqlc.UpdateAwaitingVerifyTaskDagNodeStatusParams{Status: input.Status, Result: input.Result, DagKey: input.DagKey, NodeKey: input.NodeKey})
	if err != nil {
		return nil, err
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error) {
	row, err := s.q.CompleteTaskDagNode(ctx, sqlc.CompleteTaskDagNodeParams{Status: input.Status, Result: input.Result, DagKey: input.DagKey, NodeKey: input.NodeKey})
	if err != nil {
		return nil, err
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) UpdateNodeStatusFlexible(ctx context.Context, input FlexibleNodeStatusUpdate) (*Node, error) {
	row, err := s.q.UpdateTaskDagNodeStatusFlexible(ctx, sqlc.UpdateTaskDagNodeStatusFlexibleParams{Status: input.Status, Result: input.Result, DagKey: input.DagKey, NodeKey: input.NodeKey})
	if err != nil {
		return nil, err
	}
	mapped := fromNode(row)
	return &mapped, nil
}

func (s *store) EnqueueWakeup(ctx context.Context, input EnqueueWakeupInput) (int64, error) {
	return s.q.EnqueueTaskDagWakeup(ctx, sqlc.EnqueueTaskDagWakeupParams{DagKey: input.DagKey, NodeKey: input.NodeKey, WakeupKind: input.WakeupKind, TargetAgentID: input.TargetAgentID, PromptPayload: input.PromptPayload, IdempotencyKey: input.IdempotencyKey})
}

func (s *store) ClaimDueWakeups(ctx context.Context, input ClaimDueWakeupsInput) ([]Wakeup, error) {
	rows, err := s.q.ClaimDueTaskDagWakeups(ctx, sqlc.ClaimDueTaskDagWakeupsParams{ClaimedBy: input.ClaimedBy, LeaseInterval: input.LeaseInterval, Limit: input.Limit})
	if err != nil {
		return nil, err
	}
	return mapWakeups(rows), nil
}

func (s *store) MarkWakeupSent(ctx context.Context, input MarkWakeupSentInput) (int64, error) {
	return s.q.MarkTaskDagWakeupSent(ctx, sqlc.MarkTaskDagWakeupSentParams{ID: input.ID, ClaimedAt: input.ClaimedAt})
}

func (s *store) BindWakeupTurn(ctx context.Context, input BindWakeupTurnInput) (int64, error) {
	return s.q.BindTaskDagWakeupTurn(ctx, sqlc.BindTaskDagWakeupTurnParams{TurnID: input.TurnID, ID: input.ID})
}

func (s *store) RetryWakeup(ctx context.Context, input RetryWakeupInput) (int64, error) {
	return s.q.RetryTaskDagWakeup(ctx, sqlc.RetryTaskDagWakeupParams{RetryInterval: input.RetryInterval, LastError: input.LastError, ID: input.ID, ClaimedAt: input.ClaimedAt})
}

func (s *store) FailWakeup(ctx context.Context, input FailWakeupInput) (int64, error) {
	return s.q.FailTaskDagWakeup(ctx, sqlc.FailTaskDagWakeupParams{LastError: input.LastError, ID: input.ID, ClaimedAt: input.ClaimedAt})
}

func (s *store) AcquireWorkerLease(ctx context.Context, input AcquireWorkerLeaseInput) (int64, error) {
	return s.q.AcquireTaskDagWorkerLease(ctx, sqlc.AcquireTaskDagWorkerLeaseParams{TargetAgentID: input.TargetAgentID, OwnerID: input.OwnerID, LeaseInterval: input.LeaseInterval})
}

func (s *store) RenewWorkerLease(ctx context.Context, input RenewWorkerLeaseInput) (int64, error) {
	return s.q.RenewTaskDagWorkerLease(ctx, sqlc.RenewTaskDagWorkerLeaseParams{LeaseInterval: input.LeaseInterval, TargetAgentID: input.TargetAgentID, OwnerID: input.OwnerID})
}

func (s *store) ReleaseWorkerLease(ctx context.Context, input ReleaseWorkerLeaseInput) error {
	return s.q.ReleaseTaskDagWorkerLease(ctx, sqlc.ReleaseTaskDagWorkerLeaseParams{TargetAgentID: input.TargetAgentID, OwnerID: input.OwnerID})
}

func (s *store) ReclaimStaleDispatchingWakeups(ctx context.Context) (int64, error) {
	return s.q.ReclaimStaleDispatchingTaskDagWakeups(ctx)
}

func (s *store) ListSentUnboundWakeups(ctx context.Context, targetAgentID string) ([]Wakeup, error) {
	rows, err := s.q.ListSentUnboundTaskDagWakeups(ctx, targetAgentID)
	if err != nil {
		return nil, err
	}
	return mapWakeups(rows), nil
}

func (s *store) ListPendingOrDispatchingWakeups(ctx context.Context) ([]Wakeup, error) {
	rows, err := s.q.ListPendingOrDispatchingTaskDagWakeups(ctx)
	if err != nil {
		return nil, err
	}
	return mapWakeups(rows), nil
}

func (s *store) GetWakeup(ctx context.Context, id int64) (*Wakeup, error) {
	row, err := s.q.GetTaskDagWakeup(ctx, id)
	if err != nil {
		return nil, err
	}
	mapped := fromWakeup(row)
	return &mapped, nil
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

func mapWakeups(rows []sqlc.TaskDagWakeup) []Wakeup {
	out := make([]Wakeup, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromWakeup(row))
	}
	return out
}

func fromDAG(row sqlc.TaskDag) DAG {
	return DAG{ID: row.ID, DagKey: row.DagKey, Title: row.Title, Description: row.Description, Status: row.Status, CreatedBy: row.CreatedBy, Metadata: row.Metadata, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func fromNode(row sqlc.TaskDagNode) Node {
	return Node{ID: row.ID, DagKey: row.DagKey, NodeKey: row.NodeKey, Title: row.Title, NodeType: row.NodeType, AssignedTo: row.AssignedTo, DependsOn: row.DependsOn, Status: row.Status, CommandRef: row.CommandRef, Config: row.Config, Result: row.Result, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt, ActiveTurnID: row.ActiveTurnID, ActiveWakeupID: row.ActiveWakeupID, LastEventAt: row.LastEventAt}
}

func fromWakeup(row sqlc.TaskDagWakeup) Wakeup {
	return Wakeup{ID: row.ID, DagKey: row.DagKey, NodeKey: row.NodeKey, WakeupKind: row.WakeupKind, TargetAgentID: row.TargetAgentID, PromptPayload: row.PromptPayload, IdempotencyKey: row.IdempotencyKey, Status: row.Status, AttemptCount: row.AttemptCount, NextRetryAt: row.NextRetryAt, ClaimedAt: row.ClaimedAt, ClaimedBy: row.ClaimedBy, LeaseExpiresAt: row.LeaseExpiresAt, SentAt: row.SentAt, BoundTurnID: row.BoundTurnID, TurnBoundAt: row.TurnBoundAt, LastError: row.LastError, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}
