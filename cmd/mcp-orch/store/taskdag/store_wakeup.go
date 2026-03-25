package taskdag

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

func (s *store) EnqueueWakeup(ctx context.Context, input EnqueueWakeupInput) (int64, error) {
	id, err := s.q.EnqueueTaskDagWakeup(ctx, sqlc.EnqueueTaskDagWakeupParams{
		DagKey:         input.DagKey,
		NodeKey:        input.NodeKey,
		WakeupKind:     input.WakeupKind,
		TargetAgentID:  input.TargetAgentID,
		Column5:        input.PromptPayload,
		IdempotencyKey: input.IdempotencyKey,
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "enqueue", "task_dag_wakeup")
	}
	return id, nil
}

func (s *store) ClaimDueWakeups(ctx context.Context, input ClaimDueWakeupsInput) ([]Wakeup, error) {
	leaseInterval, err := intervalValue(input.LeaseInterval)
	if err != nil {
		return nil, wrapTaskDAGError(err, "claim_due", "task_dag_wakeup")
	}
	rows, err := s.q.ClaimDueTaskDagWakeups(ctx, sqlc.ClaimDueTaskDagWakeupsParams{
		ClaimedBy: input.ClaimedBy,
		Column2:   leaseInterval,
		Limit:     input.Limit,
	})
	if err != nil {
		return nil, wrapTaskDAGError(err, "claim_due", "task_dag_wakeup")
	}
	return mapWakeups(rows), nil
}

func (s *store) MarkWakeupSent(ctx context.Context, input MarkWakeupSentInput) (int64, error) {
	count, err := s.q.MarkTaskDagWakeupSent(ctx, sqlc.MarkTaskDagWakeupSentParams{
		ID:             input.ID,
		ClaimedAt:      timestampValue(input.ClaimedAt),
		ClaimedBy:      input.ClaimedBy,
		LeaseExpiresAt: timestampValue(input.LeaseExpiresAt),
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "mark_sent", "task_dag_wakeup")
	}
	return count, nil
}

func (s *store) BindWakeupTurn(ctx context.Context, input BindWakeupTurnInput) (int64, error) {
	count, err := s.q.BindTaskDagWakeupTurn(ctx, sqlc.BindTaskDagWakeupTurnParams{
		BoundTurnID: stringPtr(input.TurnID),
		ID:          input.ID,
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "bind_turn", "task_dag_wakeup")
	}
	return count, nil
}

func (s *store) RetryWakeup(ctx context.Context, input RetryWakeupInput) (int64, error) {
	retryInterval, err := intervalValue(input.RetryInterval)
	if err != nil {
		return 0, wrapTaskDAGError(err, "retry", "task_dag_wakeup")
	}
	count, err := s.q.RetryTaskDagWakeup(ctx, sqlc.RetryTaskDagWakeupParams{
		Column1:        retryInterval,
		LastError:      input.LastError,
		ID:             input.ID,
		ClaimedAt:      timestampValue(input.ClaimedAt),
		ClaimedBy:      input.ClaimedBy,
		LeaseExpiresAt: timestampValue(input.LeaseExpiresAt),
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "retry", "task_dag_wakeup")
	}
	return count, nil
}

func (s *store) FailWakeup(ctx context.Context, input FailWakeupInput) (int64, error) {
	count, err := s.q.FailTaskDagWakeup(ctx, sqlc.FailTaskDagWakeupParams{
		LastError:      input.LastError,
		ID:             input.ID,
		ClaimedAt:      timestampValue(input.ClaimedAt),
		ClaimedBy:      input.ClaimedBy,
		LeaseExpiresAt: timestampValue(input.LeaseExpiresAt),
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "fail", "task_dag_wakeup")
	}
	return count, nil
}

func (s *store) ReclaimStaleDispatchingWakeups(ctx context.Context) (int64, error) {
	count, err := s.q.ReclaimStaleDispatchingTaskDagWakeups(ctx)
	if err != nil {
		return 0, wrapTaskDAGError(err, "reclaim_stale_dispatching", "task_dag_wakeup")
	}
	return count, nil
}

func (s *store) ListSentUnboundWakeups(ctx context.Context, targetAgentID string) ([]Wakeup, error) {
	rows, err := s.q.ListSentUnboundTaskDagWakeups(ctx, targetAgentID)
	if err != nil {
		return nil, wrapTaskDAGError(err, "list_sent_unbound", "task_dag_wakeup")
	}
	return mapWakeups(rows), nil
}

func (s *store) ListPendingOrDispatchingWakeups(ctx context.Context) ([]Wakeup, error) {
	rows, err := s.q.ListPendingOrDispatchingTaskDagWakeups(ctx)
	if err != nil {
		return nil, wrapTaskDAGError(err, "list_pending_or_dispatching", "task_dag_wakeup")
	}
	return mapWakeups(rows), nil
}

func (s *store) GetWakeup(ctx context.Context, id int64) (*Wakeup, error) {
	row, err := s.q.GetTaskDagWakeup(ctx, id)
	if err != nil {
		return nil, wrapTaskDAGError(err, "get", "task_dag_wakeup")
	}
	mapped := fromWakeup(row)
	return &mapped, nil
}

func mapWakeups(rows []sqlc.TaskDagWakeup) []Wakeup {
	out := make([]Wakeup, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromWakeup(row))
	}
	return out
}

func fromWakeup(row sqlc.TaskDagWakeup) Wakeup {
	return Wakeup{
		ID:             row.ID,
		DagKey:         row.DagKey,
		NodeKey:        row.NodeKey,
		WakeupKind:     row.WakeupKind,
		TargetAgentID:  row.TargetAgentID,
		PromptPayload:  row.PromptPayload,
		IdempotencyKey: row.IdempotencyKey,
		Status:         row.Status,
		AttemptCount:   row.AttemptCount,
		NextRetryAt:    timeValue(row.NextRetryAt),
		ClaimedAt:      timestampPtr(row.ClaimedAt),
		ClaimedBy:      row.ClaimedBy,
		LeaseExpiresAt: timestampPtr(row.LeaseExpiresAt),
		SentAt:         timestampPtr(row.SentAt),
		BoundTurnID:    sqlc.TextPtr(row.BoundTurnID),
		TurnBoundAt:    timestampPtr(row.TurnBoundAt),
		LastError:      row.LastError,
		CreatedAt:      timeValue(row.CreatedAt),
		UpdatedAt:      timeValue(row.UpdatedAt),
	}
}
