package sqlc

import "context"

const (
	enqueueTaskDagWakeupSQL                   = `INSERT INTO task_dag_wakeups (dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key) VALUES ($1, $2, $3, $4, $5::jsonb, $6) ON CONFLICT (idempotency_key) DO NOTHING;`
	claimDueTaskDagWakeupsSQL                 = `UPDATE task_dag_wakeups SET status = 'dispatching', claimed_at = NOW(), claimed_by = $1, lease_expires_at = NOW() + $2::interval, attempt_count = attempt_count + 1, updated_at = NOW() WHERE id IN ( SELECT id FROM task_dag_wakeups WHERE status = 'pending' AND next_retry_at <= NOW() ORDER BY next_retry_at, id LIMIT $3 FOR UPDATE SKIP LOCKED ) RETURNING id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at;`
	markTaskDagWakeupSentSQL                  = `UPDATE task_dag_wakeups SET status = 'sent', sent_at = NOW(), updated_at = NOW() WHERE id = $1 AND status = 'dispatching' AND claimed_at = $2;`
	bindTaskDagWakeupTurnSQL                  = `UPDATE task_dag_wakeups SET bound_turn_id = $1, turn_bound_at = NOW(), updated_at = NOW() WHERE id = $2 AND status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL;`
	retryTaskDagWakeupSQL                     = `UPDATE task_dag_wakeups SET status = 'pending', next_retry_at = NOW() + $1::interval, last_error = $2, claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = NOW() WHERE id = $3 AND status = 'dispatching' AND claimed_at = $4 AND attempt_count < 8;`
	failTaskDagWakeupSQL                      = `UPDATE task_dag_wakeups SET status = 'failed', last_error = $1, claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = NOW() WHERE id = $2 AND status = 'dispatching' AND claimed_at = $3;`
	acquireTaskDagWorkerLeaseSQL              = `INSERT INTO task_dag_worker_leases (target_agent_id, owner_id, lease_expires_at, updated_at) VALUES ($1, $2, NOW() + $3::interval, NOW()) ON CONFLICT (target_agent_id) DO UPDATE SET owner_id = EXCLUDED.owner_id, lease_expires_at = EXCLUDED.lease_expires_at, updated_at = NOW() WHERE task_dag_worker_leases.lease_expires_at < NOW() OR task_dag_worker_leases.owner_id = EXCLUDED.owner_id;`
	renewTaskDagWorkerLeaseSQL                = `UPDATE task_dag_worker_leases SET lease_expires_at = NOW() + $1::interval, updated_at = NOW() WHERE target_agent_id = $2 AND owner_id = $3 AND lease_expires_at >= NOW();`
	releaseTaskDagWorkerLeaseSQL              = `DELETE FROM task_dag_worker_leases WHERE target_agent_id = $1 AND owner_id = $2;`
	reclaimStaleDispatchingTaskDagWakeupsSQL  = `UPDATE task_dag_wakeups SET status = 'pending', claimed_at = NULL, claimed_by = '', lease_expires_at = NULL, updated_at = NOW() WHERE status = 'dispatching' AND lease_expires_at < NOW();`
	listSentUnboundTaskDagWakeupsSQL          = `SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at FROM task_dag_wakeups WHERE target_agent_id = $1 AND status = 'sent' AND sent_at IS NOT NULL AND bound_turn_id IS NULL ORDER BY sent_at DESC, id DESC;`
	listPendingOrDispatchingTaskDagWakeupsSQL = `SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at FROM task_dag_wakeups WHERE status IN ('pending', 'dispatching') ORDER BY next_retry_at, id;`
	getTaskDagWakeupSQL                       = `SELECT id, dag_key, node_key, wakeup_kind, target_agent_id, prompt_payload, idempotency_key, status, attempt_count, next_retry_at, claimed_at, claimed_by, lease_expires_at, sent_at, bound_turn_id, turn_bound_at, last_error, created_at, updated_at FROM task_dag_wakeups WHERE id = $1;`
)

func scanTaskDagWakeup(row rowScanner) (TaskDagWakeup, error) {
	var item TaskDagWakeup
	err := row.Scan(&item.ID, &item.DagKey, &item.NodeKey, &item.WakeupKind, &item.TargetAgentID, &item.PromptPayload, &item.IdempotencyKey, &item.Status, &item.AttemptCount, &item.NextRetryAt, &item.ClaimedAt, &item.ClaimedBy, &item.LeaseExpiresAt, &item.SentAt, &item.BoundTurnID, &item.TurnBoundAt, &item.LastError, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func (q *Queries) EnqueueTaskDagWakeup(ctx context.Context, arg EnqueueTaskDagWakeupParams) (int64, error) {
	return q.execRows(ctx, enqueueTaskDagWakeupSQL, arg.DagKey, arg.NodeKey, arg.WakeupKind, arg.TargetAgentID, arg.PromptPayload, arg.IdempotencyKey)
}

func (q *Queries) ClaimDueTaskDagWakeups(ctx context.Context, arg ClaimDueTaskDagWakeupsParams) ([]TaskDagWakeup, error) {
	return queryMany(ctx, q, claimDueTaskDagWakeupsSQL, scanTaskDagWakeup, arg.ClaimedBy, arg.LeaseInterval, arg.Limit)
}

func (q *Queries) MarkTaskDagWakeupSent(ctx context.Context, arg MarkTaskDagWakeupSentParams) (int64, error) {
	return q.execRows(ctx, markTaskDagWakeupSentSQL, arg.ID, arg.ClaimedAt)
}

func (q *Queries) BindTaskDagWakeupTurn(ctx context.Context, arg BindTaskDagWakeupTurnParams) (int64, error) {
	return q.execRows(ctx, bindTaskDagWakeupTurnSQL, arg.TurnID, arg.ID)
}

func (q *Queries) RetryTaskDagWakeup(ctx context.Context, arg RetryTaskDagWakeupParams) (int64, error) {
	return q.execRows(ctx, retryTaskDagWakeupSQL, arg.RetryInterval, arg.LastError, arg.ID, arg.ClaimedAt)
}

func (q *Queries) FailTaskDagWakeup(ctx context.Context, arg FailTaskDagWakeupParams) (int64, error) {
	return q.execRows(ctx, failTaskDagWakeupSQL, arg.LastError, arg.ID, arg.ClaimedAt)
}

func (q *Queries) AcquireTaskDagWorkerLease(ctx context.Context, arg AcquireTaskDagWorkerLeaseParams) (int64, error) {
	return q.execRows(ctx, acquireTaskDagWorkerLeaseSQL, arg.TargetAgentID, arg.OwnerID, arg.LeaseInterval)
}

func (q *Queries) RenewTaskDagWorkerLease(ctx context.Context, arg RenewTaskDagWorkerLeaseParams) (int64, error) {
	return q.execRows(ctx, renewTaskDagWorkerLeaseSQL, arg.LeaseInterval, arg.TargetAgentID, arg.OwnerID)
}

func (q *Queries) ReleaseTaskDagWorkerLease(ctx context.Context, arg ReleaseTaskDagWorkerLeaseParams) error {
	return q.exec(ctx, releaseTaskDagWorkerLeaseSQL, arg.TargetAgentID, arg.OwnerID)
}

func (q *Queries) ReclaimStaleDispatchingTaskDagWakeups(ctx context.Context) (int64, error) {
	return q.execRows(ctx, reclaimStaleDispatchingTaskDagWakeupsSQL)
}

func (q *Queries) ListSentUnboundTaskDagWakeups(ctx context.Context, targetAgentID string) ([]TaskDagWakeup, error) {
	return queryMany(ctx, q, listSentUnboundTaskDagWakeupsSQL, scanTaskDagWakeup, targetAgentID)
}

func (q *Queries) ListPendingOrDispatchingTaskDagWakeups(ctx context.Context) ([]TaskDagWakeup, error) {
	return queryMany(ctx, q, listPendingOrDispatchingTaskDagWakeupsSQL, scanTaskDagWakeup)
}

func (q *Queries) GetTaskDagWakeup(ctx context.Context, id int64) (TaskDagWakeup, error) {
	return queryOne(ctx, q, getTaskDagWakeupSQL, scanTaskDagWakeup, id)
}
