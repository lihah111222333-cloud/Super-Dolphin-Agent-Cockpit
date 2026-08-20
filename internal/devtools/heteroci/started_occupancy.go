package heteroci

import "time"

// StartedRoutingOccupancy 是 daemon 识别已启动旧 epoch 运行占用的严格标记。
type StartedRoutingOccupancy struct {
	OwnerEpoch     RoutingOwnerEpoch `json:"owner_epoch"`
	RunID          string            `json:"run_id"`
	UnitID         string            `json:"unit_id"`
	DecisionDigest string            `json:"decision_digest"`
}

// StartedRoutingLease 是 durable Start 返回给 launcher 的完整值对象。
type StartedRoutingLease struct {
	Fence     OwnerFence              `json:"fence"`
	Lease     RoutingLease            `json:"lease"`
	Occupancy StartedRoutingOccupancy `json:"occupancy"`
}

// NewStartedRoutingOccupancy 从已验证的 lease 身份字段创建运行占用标记。
func NewStartedRoutingOccupancy(ownerEpoch RoutingOwnerEpoch, runID, unitID, decisionDigest string) (StartedRoutingOccupancy, error) {
	occupancy := StartedRoutingOccupancy{
		OwnerEpoch:     ownerEpoch,
		RunID:          runID,
		UnitID:         unitID,
		DecisionDigest: decisionDigest,
	}
	if err := occupancy.Validate(); err != nil {
		return StartedRoutingOccupancy{}, err
	}
	return occupancy, nil
}

// Validate 检查运行占用标记的 epoch、运行、工作单元和决策摘要。
func (occupancy StartedRoutingOccupancy) Validate() error {
	if err := occupancy.OwnerEpoch.Validate(); err != nil {
		return err
	}
	if !validRoutingRunID(occupancy.RunID) {
		return safeStartedOccupancyError("run id")
	}
	if !validExecutionUnitID(occupancy.UnitID) {
		return safeStartedOccupancyError("unit id")
	}
	if !isSHA256Digest(occupancy.DecisionDigest) {
		return safeStartedOccupancyError("decision digest")
	}
	return nil
}

// NewStartedRoutingLease 从 fence 和 started lease 内部派生 occupancy。
func NewStartedRoutingLease(fence OwnerFence, lease RoutingLease, decision RoutingDecision, now time.Time) (StartedRoutingLease, error) {
	if err := fence.Validate(); err != nil {
		return StartedRoutingLease{}, err
	}
	if err := validateStartedRoutingLeaseBinding(lease, decision, now); err != nil {
		return StartedRoutingLease{}, err
	}
	occupancy, err := NewStartedRoutingOccupancy(fence.OwnerEpoch, lease.RunID, lease.UnitID, lease.DecisionDigest)
	if err != nil {
		return StartedRoutingLease{}, err
	}
	return StartedRoutingLease{Fence: fence, Lease: lease, Occupancy: occupancy}, nil
}

// ValidateAgainst 检查 durable Start 结果与 fence、decision 和 lease 的绑定。
func (started StartedRoutingLease) ValidateAgainst(decision RoutingDecision, now time.Time) error {
	if err := started.Fence.Validate(); err != nil {
		return err
	}
	if err := validateStartedRoutingLeaseBinding(started.Lease, decision, now); err != nil {
		return err
	}
	if err := started.Occupancy.Validate(); err != nil {
		return err
	}
	if started.Occupancy.OwnerEpoch != started.Fence.OwnerEpoch {
		return safeStartedOccupancyError("owner epoch")
	}
	if started.Occupancy.RunID != started.Lease.RunID || started.Occupancy.UnitID != started.Lease.UnitID || started.Occupancy.DecisionDigest != started.Lease.DecisionDigest {
		return safeStartedOccupancyError("lease identity")
	}
	return nil
}

func validateStartedRoutingLeaseBinding(lease RoutingLease, decision RoutingDecision, now time.Time) error {
	if lease.State != LeaseStateStarted {
		return safeStartedOccupancyError("lease state")
	}
	return lease.ValidateAgainst(decision, now)
}

func safeStartedOccupancyError(field string) error {
	return &startedOccupancyError{field: field}
}

type startedOccupancyError struct{ field string }

// Error 返回不包含身份值的稳定 occupancy 错误文本。
func (err *startedOccupancyError) Error() string {
	return "started routing occupancy is invalid: " + err.field
}
