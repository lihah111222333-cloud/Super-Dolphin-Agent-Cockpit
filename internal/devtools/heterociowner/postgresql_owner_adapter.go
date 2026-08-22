package heterociowner

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// PostgreSQLDurableOwner 将 remote PG owner 适配为 workflow 的 fence 生命周期端口。
type PostgreSQLDurableOwner struct {
	owner *PostgreSQLOwner
}

// NewPostgreSQLDurableOwner 创建不含本地副本或 fallback 的 workflow owner 适配器。
func NewPostgreSQLDurableOwner(owner *PostgreSQLOwner) (*PostgreSQLDurableOwner, error) {
	if owner == nil || owner.db == nil {
		return nil, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	return &PostgreSQLDurableOwner{owner: owner}, nil
}

// Acquire 创建新的 remote PostgreSQL owner fence。
func (owner *PostgreSQLDurableOwner) Acquire(ctx context.Context) (heteroci.OwnerFence, error) {
	if owner == nil || owner.owner == nil {
		return heteroci.OwnerFence{}, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	return owner.owner.Acquire(ctx)
}

// LocateRun 读取 PostgreSQL owner 中同一运行标识的 durable 投影。
func (owner *PostgreSQLDurableOwner) LocateRun(ctx context.Context, runID heteroci.TopologyRunID) ([]DurableRunState, error) {
	if owner == nil || owner.owner == nil {
		return nil, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	return owner.owner.LocateRun(ctx, runID)
}

// PlanAndReserve 通过 workflow fence 端口提交一次 PostgreSQL durable reservation。
func (owner *PostgreSQLDurableOwner) PlanAndReserve(ctx context.Context, fence heteroci.OwnerFence, request heteroci.TopologyPlanRequest) (DurableReservedRoutingPlan, error) {
	if owner == nil || owner.owner == nil {
		return DurableReservedRoutingPlan{}, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	session, err := heteroci.NewTopologySession(fence)
	if err != nil {
		return DurableReservedRoutingPlan{}, err
	}
	planned, err := owner.owner.PlanAndReserve(ctx, session, request)
	if err != nil {
		return DurableReservedRoutingPlan{}, err
	}
	return DurableReservedRoutingPlan{Fence: planned.Fence, OperationID: planned.OperationID, SourceSnapshot: planned.SourceSnapshot, EffectiveSnapshot: planned.EffectiveSnapshot, Plan: planned.Plan, Decision: planned.Decision, Leases: planned.Leases}, nil
}

// BindExecutors 将 request-level binding 作为独立 PostgreSQL owner mutation 提交。
func (owner *PostgreSQLDurableOwner) BindExecutors(ctx context.Context, mutation heteroci.ExecutorBindingMutation) (heteroci.ExecutorBindingMutationResult, error) {
	if owner == nil || owner.owner == nil {
		return heteroci.ExecutorBindingMutationResult{}, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	return owner.owner.BindExecutors(ctx, mutation)
}

// Start 将 planned lease 绑定为 started lease。
func (owner *PostgreSQLDurableOwner) Start(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, snapshot heteroci.SnapshotSet) (heteroci.StartedRoutingLease, error) {
	if owner == nil || owner.owner == nil {
		return heteroci.StartedRoutingLease{}, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	return owner.owner.Start(ctx, fence, operationID, runID, unitID, snapshot)
}

// Finish 将 started lease 收敛为 succeeded 或 failed。
func (owner *PostgreSQLDurableOwner) Finish(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string, terminal heteroci.RoutingLeaseState) (DurableLeaseTransition, error) {
	if owner == nil || owner.owner == nil {
		return DurableLeaseTransition{}, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	return owner.owner.Finish(ctx, fence, operationID, runID, unitID, terminal)
}

// CancelPlanned 将未启动 lease 收敛为 cancelled。
func (owner *PostgreSQLDurableOwner) CancelPlanned(ctx context.Context, fence heteroci.OwnerFence, operationID heteroci.OperationID, runID heteroci.TopologyRunID, unitID string) (DurableLeaseTransition, error) {
	if owner == nil || owner.owner == nil {
		return DurableLeaseTransition{}, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	return owner.owner.CancelPlanned(ctx, fence, operationID, runID, unitID)
}

// ReadRun 读取当前 owner epoch 的 PostgreSQL durable run。
func (owner *PostgreSQLDurableOwner) ReadRun(ctx context.Context, runID heteroci.TopologyRunID) (DurableRunState, error) {
	if owner == nil || owner.owner == nil {
		return DurableRunState{}, fmt.Errorf("heteroci PostgreSQL durable owner is nil")
	}
	return owner.owner.ReadRun(ctx, runID)
}
