package taskdag

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

func (s *store) EnqueueWakeup(ctx context.Context, input EnqueueWakeupInput) (int64, error) {
	if input.RunID <= 0 {
		return 0, fmt.Errorf("enqueue task dag wakeup: run_id required")
	}
	return queryValue(func() (int64, error) {
		return s.q.EnqueueTaskDagWakeup(ctx, sqlc.EnqueueTaskDagWakeupParams{
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			Column3:        input.RunID,
			WakeupKind:     input.WakeupKind,
			TargetAgentID:  input.TargetAgentID,
			Column6:        input.PromptPayload,
			IdempotencyKey: input.IdempotencyKey,
		})
	}, "enqueue", "task_dag_wakeup")
}

func (s *store) ClaimDueWakeups(ctx context.Context, input ClaimDueWakeupsInput) ([]Wakeup, error) {
	leaseInterval, err := parseLeaseDuration(input.LeaseInterval, "claim_due", "task_dag_wakeup")
	if err != nil {
		return nil, err
	}
	return queryMany(func() ([]sqlc.TaskDagWakeup, error) {
		return s.q.ClaimDueTaskDagWakeups(ctx, sqlc.ClaimDueTaskDagWakeupsParams{
			ClaimedBy: input.ClaimedBy,
			Column2:   leaseInterval,
			Limit:     input.Limit,
		})
	}, "claim_due", "task_dag_wakeup", fromWakeup)
}

func (s *store) MarkWakeupSent(ctx context.Context, input MarkWakeupSentInput) (int64, error) {
	fence := wakeupFenceFromMark(input)
	return fencedWakeupMutation("mark_sent", fence, func(fence wakeupFence) (int64, error) {
		return s.q.MarkTaskDagWakeupSent(ctx, sqlc.MarkTaskDagWakeupSentParams{
			ID:             fence.ID,
			ClaimedAt:      timestampValue(fence.ClaimedAt),
			ClaimedBy:      fence.ClaimedBy,
			LeaseExpiresAt: timestampValue(fence.LeaseExpiresAt),
		})
	})
}

func (s *store) BindWakeupTurn(ctx context.Context, input BindWakeupTurnInput) (int64, error) {
	return bindWakeupTurnTx(ctx, s.q, input, false)
}

func (s *store) RetryWakeup(ctx context.Context, input RetryWakeupInput) (int64, error) {
	retryInterval, err := parseLeaseDuration(input.RetryInterval, "retry", "task_dag_wakeup")
	if err != nil {
		return 0, err
	}
	fence := wakeupFenceFromRetry(input)
	return fencedWakeupMutation("retry", fence, func(fence wakeupFence) (int64, error) {
		return s.q.RetryTaskDagWakeup(ctx, sqlc.RetryTaskDagWakeupParams{
			Column1:        retryInterval,
			LastError:      input.LastError,
			ID:             fence.ID,
			ClaimedAt:      timestampValue(fence.ClaimedAt),
			ClaimedBy:      fence.ClaimedBy,
			LeaseExpiresAt: timestampValue(fence.LeaseExpiresAt),
		})
	})
}

func (s *store) RetryWakeupWithNodeConfigPatch(ctx context.Context, input RetryWakeupWithNodeConfigPatchInput) (int64, error) {
	retryInterval, err := parseLeaseDuration(input.RetryWakeup.RetryInterval, "retry_with_config_patch", "task_dag_wakeup")
	if err != nil {
		return 0, err
	}
	fence := wakeupFenceFromRetry(input.RetryWakeup)
	var rows int64
	err = sqlctx.WithTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		retryRows, retryErr := txq.RetryTaskDagWakeup(ctx, sqlc.RetryTaskDagWakeupParams{
			Column1:        retryInterval,
			LastError:      input.RetryWakeup.LastError,
			ID:             fence.ID,
			ClaimedAt:      timestampValue(fence.ClaimedAt),
			ClaimedBy:      fence.ClaimedBy,
			LeaseExpiresAt: timestampValue(fence.LeaseExpiresAt),
		})
		if retryErr != nil {
			return retryErr
		}
		rows = retryRows
		if retryRows == 0 {
			return nil
		}
		_, patchErr := txq.PatchTaskDagNodeConfigIfUnchanged(ctx, sqlc.PatchTaskDagNodeConfigIfUnchangedParams{
			DagKey:         input.NodeConfig.DagKey,
			NodeKey:        input.NodeConfig.NodeKey,
			Config:         input.NodeConfig.Config,
			PreviousConfig: input.NodeConfig.PreviousConfig,
			RunID:          int64Ptr(input.NodeConfig.RunID),
		})
		if patchErr != nil {
			return wrapTaskDAGError(patchErr, "patch_config", "task_dag_node")
		}
		return nil
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "retry_with_config_patch", "task_dag_wakeup")
	}
	return rows, nil
}

func (s *store) FailWakeup(ctx context.Context, input FailWakeupInput) (int64, error) {
	fence := wakeupFenceFromFail(input)
	return fencedWakeupMutation("fail", fence, func(fence wakeupFence) (int64, error) {
		return s.q.FailTaskDagWakeup(ctx, sqlc.FailTaskDagWakeupParams{
			LastError:      input.LastError,
			ID:             fence.ID,
			ClaimedAt:      timestampValue(fence.ClaimedAt),
			ClaimedBy:      fence.ClaimedBy,
			LeaseExpiresAt: timestampValue(fence.LeaseExpiresAt),
		})
	})
}

func (s *store) ReclaimStaleDispatchingWakeups(ctx context.Context) (int64, error) {
	return queryValue(func() (int64, error) {
		return s.q.ReclaimStaleDispatchingTaskDagWakeups(ctx)
	}, "reclaim_stale_dispatching", "task_dag_wakeup")
}

func (s *store) ListSentUnboundWakeups(ctx context.Context, targetAgentID string) ([]Wakeup, error) {
	return queryMany(func() ([]sqlc.TaskDagWakeup, error) {
		return s.q.ListSentUnboundTaskDagWakeups(ctx, targetAgentID)
	}, "list_sent_unbound", "task_dag_wakeup", fromWakeup)
}

func (s *store) ListPendingOrDispatchingWakeups(ctx context.Context) ([]Wakeup, error) {
	return queryMany(func() ([]sqlc.TaskDagWakeup, error) {
		return s.q.ListPendingOrDispatchingTaskDagWakeups(ctx)
	}, "list_pending_or_dispatching", "task_dag_wakeup", fromWakeup)
}

func (s *store) GetWakeup(ctx context.Context, id int64) (*Wakeup, error) {
	return queryOne(func() (sqlc.TaskDagWakeup, error) {
		return s.q.GetTaskDagWakeup(ctx, id)
	}, "get", "task_dag_wakeup", fromWakeup)
}

func fromWakeup(row sqlc.TaskDagWakeup) Wakeup {
	return Wakeup{
		ID:             row.ID,
		DagKey:         row.DagKey,
		NodeKey:        row.NodeKey,
		RunID:          sqlc.Int8Ptr(row.RunID),
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
