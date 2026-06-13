package taskdag

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

func (s *store) AcquireWorkerLease(ctx context.Context, input AcquireWorkerLeaseInput) (int64, error) {
	leaseInterval, err := parseLeaseDuration(input.LeaseInterval, "acquire", "task_dag_worker_lease")
	if err != nil {
		return 0, err
	}
	return queryValue(func() (int64, error) {
		return s.q.AcquireTaskDagWorkerLease(ctx, sqlc.AcquireTaskDagWorkerLeaseParams{
			TargetAgentID: input.TargetAgentID,
			OwnerID:       input.OwnerID,
			LeaseMs:       leaseInterval,
		})
	}, "acquire", "task_dag_worker_lease")
}

func (s *store) RenewWorkerLease(ctx context.Context, input RenewWorkerLeaseInput) (int64, error) {
	leaseInterval, err := parseLeaseDuration(input.LeaseInterval, "renew", "task_dag_worker_lease")
	if err != nil {
		return 0, err
	}
	return queryValue(func() (int64, error) {
		return s.q.RenewTaskDagWorkerLease(ctx, sqlc.RenewTaskDagWorkerLeaseParams{
			LeaseMs:       leaseInterval,
			TargetAgentID: input.TargetAgentID,
			OwnerID:       input.OwnerID,
		})
	}, "renew", "task_dag_worker_lease")
}

func (s *store) ReleaseWorkerLease(ctx context.Context, input ReleaseWorkerLeaseInput) error {
	_, err := queryValue(func() (struct{}, error) {
		return struct{}{}, s.q.ReleaseTaskDagWorkerLease(ctx, sqlc.ReleaseTaskDagWorkerLeaseParams{
			TargetAgentID: input.TargetAgentID,
			OwnerID:       input.OwnerID,
		})
	}, "release", "task_dag_worker_lease")
	return err
}
