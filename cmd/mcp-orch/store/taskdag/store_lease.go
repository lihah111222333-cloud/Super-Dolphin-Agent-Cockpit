package taskdag

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

func (s *store) AcquireWorkerLease(ctx context.Context, input AcquireWorkerLeaseInput) (int64, error) {
	leaseInterval, err := intervalValue(input.LeaseInterval)
	if err != nil {
		return 0, wrapTaskDAGError(err, "acquire", "task_dag_worker_lease")
	}
	count, err := s.q.AcquireTaskDagWorkerLease(ctx, sqlc.AcquireTaskDagWorkerLeaseParams{
		TargetAgentID: input.TargetAgentID,
		OwnerID:       input.OwnerID,
		Column3:       leaseInterval,
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "acquire", "task_dag_worker_lease")
	}
	return count, nil
}

func (s *store) RenewWorkerLease(ctx context.Context, input RenewWorkerLeaseInput) (int64, error) {
	leaseInterval, err := intervalValue(input.LeaseInterval)
	if err != nil {
		return 0, wrapTaskDAGError(err, "renew", "task_dag_worker_lease")
	}
	count, err := s.q.RenewTaskDagWorkerLease(ctx, sqlc.RenewTaskDagWorkerLeaseParams{
		Column1:       leaseInterval,
		TargetAgentID: input.TargetAgentID,
		OwnerID:       input.OwnerID,
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "renew", "task_dag_worker_lease")
	}
	return count, nil
}

func (s *store) ReleaseWorkerLease(ctx context.Context, input ReleaseWorkerLeaseInput) error {
	return wrapTaskDAGError(s.q.ReleaseTaskDagWorkerLease(ctx, sqlc.ReleaseTaskDagWorkerLeaseParams{
		TargetAgentID: input.TargetAgentID,
		OwnerID:       input.OwnerID,
	}), "release", "task_dag_worker_lease")
}
