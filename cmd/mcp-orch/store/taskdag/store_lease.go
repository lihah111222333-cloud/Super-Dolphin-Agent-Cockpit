package taskdag

import (
	"context"
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// AcquireWorkerLease 抢占 target_agent_id 的 worker lease。返回 1 表示本 owner
// 获得/续占成功，返回 0 表示仍有未过期 owner 持有；调度方必须把 0 当作
// “别人正在处理”，不能继续执行同一 agent 的恢复/派发路径。
func (s *store) AcquireWorkerLease(ctx context.Context, input AcquireWorkerLeaseInput) (int64, error) {
	leaseInterval, err := parseLeaseDuration(input.LeaseInterval, "acquire", "task_dag_worker_lease")
	if err != nil {
		return 0, err
	}
	var acquired int64
	err = sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		rows, updateErr := txq.UpdateAcquirableTaskDagWorkerLease(ctx, sqlc.UpdateAcquirableTaskDagWorkerLeaseParams{
			OwnerID: input.OwnerID, LeaseMs: leaseInterval, TargetAgentID: input.TargetAgentID,
		})
		if updateErr != nil {
			return updateErr
		}
		if rows > 1 {
			return fmt.Errorf("acquire task_dag_worker_lease: updated %d rows, want at most 1", rows)
		}
		if rows == 1 {
			acquired = 1
			return nil
		}
		exists, existsErr := txq.HasTaskDagWorkerLease(ctx, sqlc.HasTaskDagWorkerLeaseParams{TargetAgentID: input.TargetAgentID})
		if existsErr != nil {
			return existsErr
		}
		if exists {
			return nil
		}
		rows, insertErr := txq.InsertTaskDagWorkerLease(ctx, sqlc.InsertTaskDagWorkerLeaseParams{
			TargetAgentID: input.TargetAgentID, OwnerID: input.OwnerID, LeaseMs: leaseInterval,
		})
		if insertErr != nil {
			return insertErr
		}
		if rows != 1 {
			return fmt.Errorf("acquire task_dag_worker_lease: inserted %d rows, want 1", rows)
		}
		acquired = 1
		return nil
	})
	return acquired, wrapTaskDAGError(err, "acquire", "task_dag_worker_lease")
}

// RenewWorkerLease 只允许当前 owner 续约。rows=0 是 fencing 信号：lease 已被
// 其它 owner 抢走或目标不存在，调用方应停止当前 worker，而不是重新创建 lease。
func (s *store) RenewWorkerLease(ctx context.Context, input RenewWorkerLeaseInput) (int64, error) {
	leaseInterval, err := parseLeaseDuration(input.LeaseInterval, "renew", "task_dag_worker_lease")
	if err != nil {
		return 0, err
	}
	return queryValueWrite(ctx, func() (int64, error) {
		return s.q.RenewTaskDagWorkerLease(ctx, sqlc.RenewTaskDagWorkerLeaseParams{
			LeaseMs:       leaseInterval,
			TargetAgentID: input.TargetAgentID,
			OwnerID:       input.OwnerID,
		})
	}, "renew", "task_dag_worker_lease")
}

// ReleaseWorkerLease 是 best-effort 清理，只删除同一 owner 持有的 lease。
// 释放失败或 rows=0 不应被上层解释成“可继续工作”；lease 的并发正确性由
// acquire/renew 的 owner fence 保证。
func (s *store) ReleaseWorkerLease(ctx context.Context, input ReleaseWorkerLeaseInput) error {
	rows, err := queryValueWrite(ctx, func() (int64, error) {
		return s.q.ReleaseTaskDagWorkerLease(ctx, sqlc.ReleaseTaskDagWorkerLeaseParams{
			TargetAgentID: input.TargetAgentID,
			OwnerID:       input.OwnerID,
		})
	}, "release", "task_dag_worker_lease")
	if err != nil {
		return err
	}
	if rows == 0 {
		return wrapTaskDAGError(errors.New("worker lease was not held by owner"), "release", "task_dag_worker_lease")
	}
	return nil
}
