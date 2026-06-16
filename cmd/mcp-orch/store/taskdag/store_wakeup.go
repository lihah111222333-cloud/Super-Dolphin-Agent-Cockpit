package taskdag

import (
	"context"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

// EnqueueWakeup 只接受 run-scoped wakeup。run_id 是 dispatcher 之后定位
// runtime node 的硬要求，不能为了兼容模板节点而放空；幂等由调用方传入的
// idempotency_key 和 SQL ON CONFLICT 共同保证。
func (s *store) EnqueueWakeup(ctx context.Context, input EnqueueWakeupInput) (int64, error) {
	if input.RunID <= 0 {
		return 0, fmt.Errorf("enqueue task dag wakeup: run_id required")
	}
	return queryValueWrite(ctx, func() (int64, error) {
		return s.q.EnqueueTaskDagWakeup(ctx, sqlc.EnqueueTaskDagWakeupParams{
			DagKey:         input.DagKey,
			NodeKey:        input.NodeKey,
			RunID:          int64Ptr(input.RunID),
			WakeupKind:     input.WakeupKind,
			TargetAgentID:  input.TargetAgentID,
			PromptPayload:  input.PromptPayload,
			IdempotencyKey: input.IdempotencyKey,
		})
	}, "enqueue", "task_dag_wakeup")
}

// ClaimDueWakeups 把 due pending wakeup 抢成 dispatching，并写入 claimed_by /
// claimed_at / lease_expires_at。返回行里的这些 fence 字段必须原样带到
// MarkWakeupSent/RetryWakeup/FailWakeup，过期被 reclaim 后旧副本会 rows=0。
func (s *store) ClaimDueWakeups(ctx context.Context, input ClaimDueWakeupsInput) ([]Wakeup, error) {
	leaseInterval, err := parseLeaseDuration(input.LeaseInterval, "claim_due", "task_dag_wakeup")
	if err != nil {
		return nil, err
	}
	return queryManyWrite(ctx, func() ([]sqlc.ClaimDueTaskDagWakeupsRow, error) {
		return s.q.ClaimDueTaskDagWakeups(ctx, sqlc.ClaimDueTaskDagWakeupsParams{
			WorkerID:   input.ClaimedBy,
			LeaseMs:    leaseInterval,
			LimitCount: int64(input.Limit),
		})
	}, "claim_due", "task_dag_wakeup", fromClaimedWakeup)
}

// MarkWakeupSent 标记 wakeup 已发送给目标线程。
func (s *store) MarkWakeupSent(ctx context.Context, input MarkWakeupSentInput) (int64, error) {
	fence := wakeupFenceFromMark(input)
	return fencedWakeupMutationWrite(ctx, "mark_sent", fence, func(fence wakeupFence) (int64, error) {
		return s.q.MarkTaskDagWakeupSent(ctx, sqlc.MarkTaskDagWakeupSentParams{
			ID:             fence.ID,
			ClaimedAt:      timestampValue(fence.ClaimedAt),
			ClaimedBy:      fence.ClaimedBy,
			LeaseExpiresAt: timestampValue(fence.LeaseExpiresAt),
		})
	})
}

// BindWakeupTurn 把 wakeup 和实际 turn 绑定起来。
func (s *store) BindWakeupTurn(ctx context.Context, input BindWakeupTurnInput) (int64, error) {
	return bindWakeupTurnTx(ctx, s.q, input, false)
}

// RetryWakeup 重新排队失败或超时的 wakeup。
func (s *store) RetryWakeup(ctx context.Context, input RetryWakeupInput) (int64, error) {
	retryInterval, err := parseLeaseDuration(input.RetryInterval, "retry", "task_dag_wakeup")
	if err != nil {
		return 0, err
	}
	fence := wakeupFenceFromRetry(input)
	return fencedWakeupMutationWrite(ctx, "retry", fence, func(fence wakeupFence) (int64, error) {
		return s.q.RetryTaskDagWakeup(ctx, sqlc.RetryTaskDagWakeupParams{
			DelayMs:        retryInterval,
			LastError:      input.LastError,
			ID:             fence.ID,
			ClaimedAt:      timestampValue(fence.ClaimedAt),
			ClaimedBy:      fence.ClaimedBy,
			LeaseExpiresAt: timestampValue(fence.LeaseExpiresAt),
		})
	})
}

// RetryWakeupWithNodeConfigPatch 是智能重试的原子准备步骤：同一事务内先把
// 当前 claimed wakeup 退回 pending/next_retry_at，再用 previous_config fence
// patch runtime node config。patch miss 必须回滚 retry，避免 wakeup 已重试但
// 节点仍是旧配置的半提交状态。
func (s *store) RetryWakeupWithNodeConfigPatch(ctx context.Context, input RetryWakeupWithNodeConfigPatchInput) (int64, error) {
	retryInterval, err := parseLeaseDuration(input.RetryWakeup.RetryInterval, "retry_with_config_patch", "task_dag_wakeup")
	if err != nil {
		return 0, err
	}
	fence := wakeupFenceFromRetry(input.RetryWakeup)
	var rows int64
	err = sqlctx.WithTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		retryRows, retryErr := txq.RetryTaskDagWakeup(ctx, sqlc.RetryTaskDagWakeupParams{
			DelayMs:        retryInterval,
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

// FailWakeup 把 wakeup 标记为失败并记录原因。
func (s *store) FailWakeup(ctx context.Context, input FailWakeupInput) (int64, error) {
	fence := wakeupFenceFromFail(input)
	return fencedWakeupMutationWrite(ctx, "fail", fence, func(fence wakeupFence) (int64, error) {
		return s.q.FailTaskDagWakeup(ctx, sqlc.FailTaskDagWakeupParams{
			LastError:      input.LastError,
			ID:             fence.ID,
			ClaimedAt:      timestampValue(fence.ClaimedAt),
			ClaimedBy:      fence.ClaimedBy,
			LeaseExpiresAt: timestampValue(fence.LeaseExpiresAt),
		})
	})
}

// FailWakeupAndFailNodeAndCancelDownstream 把“wakeup 永久失败”和“节点失败 /
// 失败级联”绑定成一个事务。若 wakeup fence miss（rows=0），说明这条
// claimed 副本已经被 sent/retry/reclaim，不再触碰节点，防止迟到 dispatcher
// 改写新一轮调度的状态。
func (s *store) FailWakeupAndFailNodeAndCancelDownstream(ctx context.Context, wakeup FailWakeupInput, node FailNodeInput) (int64, *FailNodeResult, error) {
	if err := requireRuntimeRunID("fail_wakeup_and_fail_node", node.RunID); err != nil {
		return 0, nil, err
	}
	fence := wakeupFenceFromFail(wakeup)
	var failRows int64
	var result *FailNodeResult
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		txStore := &store{db: txdb, q: txq}
		rows, failWakeupErr := txq.FailTaskDagWakeup(ctx, sqlc.FailTaskDagWakeupParams{
			LastError:      wakeup.LastError,
			ID:             fence.ID,
			ClaimedAt:      timestampValue(fence.ClaimedAt),
			ClaimedBy:      fence.ClaimedBy,
			LeaseExpiresAt: timestampValue(fence.LeaseExpiresAt),
		})
		if failWakeupErr != nil {
			return failWakeupErr
		}
		failRows = rows
		if rows == 0 {
			return nil
		}
		nodeResult, failNodeErr := failNodeAndCancelDownstreamTx(ctx, txStore, node)
		if failNodeErr != nil {
			return failNodeErr
		}
		result = nodeResult
		return nil
	})
	if err != nil {
		return 0, nil, wrapTaskDAGError(err, "fail_wakeup_and_fail_node", "task_dag_wakeup")
	}
	return failRows, result, nil
}

// ReclaimStaleDispatchingWakeups 回收 lease 过期且仍处 dispatching 的 wakeup。
// 它不判断 dispatcher 是否存活，只依赖 lease_expires_at；被回收后旧 claim 的
// fence 全部失效，下一轮 dispatcher 会重新 claim。
func (s *store) ReclaimStaleDispatchingWakeups(ctx context.Context) (int64, error) {
	return queryValueWrite(ctx, func() (int64, error) {
		return s.q.ReclaimStaleDispatchingTaskDagWakeups(ctx)
	}, "reclaim_stale_dispatching", "task_dag_wakeup")
}

// ListSentUnboundWakeups 列出已发送但还没绑定 turn 的 wakeup。
func (s *store) ListSentUnboundWakeups(ctx context.Context, targetAgentID string) ([]Wakeup, error) {
	return queryMany(func() ([]sqlc.ListSentUnboundTaskDagWakeupsRow, error) {
		return s.q.ListSentUnboundTaskDagWakeups(ctx, sqlc.ListSentUnboundTaskDagWakeupsParams{TargetAgentID: targetAgentID})
	}, "list_sent_unbound", "task_dag_wakeup", fromSentUnboundWakeup)
}

// ListPendingOrDispatchingWakeups 列出等待或正在派发的 wakeup。
func (s *store) ListPendingOrDispatchingWakeups(ctx context.Context) ([]Wakeup, error) {
	return queryMany(func() ([]sqlc.ListPendingOrDispatchingTaskDagWakeupsRow, error) {
		return s.q.ListPendingOrDispatchingTaskDagWakeups(ctx)
	}, "list_pending_or_dispatching", "task_dag_wakeup", fromPendingOrDispatchingWakeup)
}

// GetWakeup 读取单个 wakeup 的当前状态。
func (s *store) GetWakeup(ctx context.Context, id int64) (*Wakeup, error) {
	return queryOne(func() (sqlc.GetTaskDagWakeupRow, error) {
		return s.q.GetTaskDagWakeup(ctx, sqlc.GetTaskDagWakeupParams{ID: id})
	}, "get", "task_dag_wakeup", fromGetWakeup)
}

func fromClaimedWakeup(row sqlc.ClaimDueTaskDagWakeupsRow) Wakeup {
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
		AttemptCount:   int32(row.AttemptCount),
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

func fromGetWakeup(row sqlc.GetTaskDagWakeupRow) Wakeup {
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
		AttemptCount:   int32(row.AttemptCount),
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

func fromSentUnboundWakeup(row sqlc.ListSentUnboundTaskDagWakeupsRow) Wakeup {
	return Wakeup{
		ID:             row.ID,
		DagKey:         row.DagKey,
		NodeKey:        row.NodeKey,
		RunID:          sqlc.Int8Ptr(row.RunID),
		WakeupKind:     row.WakeupKind,
		TargetAgentID:  row.TargetAgentID,
		IdempotencyKey: row.IdempotencyKey,
		Status:         row.Status,
		AttemptCount:   int32(row.AttemptCount),
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

func fromPendingOrDispatchingWakeup(row sqlc.ListPendingOrDispatchingTaskDagWakeupsRow) Wakeup {
	return Wakeup{
		ID:             row.ID,
		DagKey:         row.DagKey,
		NodeKey:        row.NodeKey,
		RunID:          sqlc.Int8Ptr(row.RunID),
		WakeupKind:     row.WakeupKind,
		TargetAgentID:  row.TargetAgentID,
		IdempotencyKey: row.IdempotencyKey,
		Status:         row.Status,
		AttemptCount:   int32(row.AttemptCount),
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
