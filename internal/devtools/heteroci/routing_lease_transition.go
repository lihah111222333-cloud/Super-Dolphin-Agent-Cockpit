package heteroci

import (
	"fmt"
	"maps"
	"time"
)

// Start 依据簿内决策和当前快照将 planned 租约原子推进为 started。
func (book *RoutingLeaseBook) Start(runID, unitID string, currentSnapshot SnapshotSet, at time.Time) (RoutingLease, error) {
	if book == nil {
		return RoutingLease{}, fmt.Errorf("routing lease book must not be nil")
	}
	if !validRoutingRunID(runID) {
		return RoutingLease{}, fmt.Errorf("routing lease run id %q is invalid", runID)
	}
	if !validExecutionUnitID(unitID) {
		return RoutingLease{}, fmt.Errorf("routing lease unit id %q is invalid", unitID)
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	run, lease, atUnixMilli, err := book.loadTransitionLocked(runID, unitID, at)
	if err != nil {
		return RoutingLease{}, err
	}
	if err := validateStartBinding(run, lease, unitID, currentSnapshot, at); err != nil {
		return RoutingLease{}, err
	}
	next, err := startedRoutingLease(lease, unitID, atUnixMilli)
	if err != nil {
		return RoutingLease{}, err
	}
	if err := next.ValidateAgainst(run.decision, at); err != nil {
		return RoutingLease{}, fmt.Errorf("validate started routing lease %q: %w", unitID, err)
	}
	run.leases[unitID] = next
	return next, nil
}

// Finish 将 started 租约以 succeeded 或 failed 终态原子提交，并在运行完成时释放预留。
func (book *RoutingLeaseBook) Finish(runID, unitID string, terminal RoutingLeaseState, at time.Time) (RoutingLease, error) {
	if book == nil {
		return RoutingLease{}, fmt.Errorf("routing lease book must not be nil")
	}
	if !validRoutingRunID(runID) {
		return RoutingLease{}, fmt.Errorf("routing lease run id %q is invalid", runID)
	}
	if !validExecutionUnitID(unitID) {
		return RoutingLease{}, fmt.Errorf("routing lease unit id %q is invalid", unitID)
	}
	if !isRoutingLeaseTerminal(terminal) {
		return RoutingLease{}, fmt.Errorf("routing lease terminal state %q is not succeeded or failed", terminal)
	}

	book.mu.Lock()
	defer book.mu.Unlock()
	run, lease, atUnixMilli, err := book.loadTransitionLocked(runID, unitID, at)
	if err != nil {
		return RoutingLease{}, err
	}
	next, err := finishedRoutingLease(lease, unitID, terminal, atUnixMilli)
	if err != nil {
		return RoutingLease{}, err
	}
	if err := next.ValidateAgainst(run.decision, at); err != nil {
		return RoutingLease{}, fmt.Errorf("validate terminal routing lease %q: %w", unitID, err)
	}
	if err := book.commitLeaseTransitionLocked(run, unitID, next); err != nil {
		return RoutingLease{}, err
	}
	return next, nil
}

// CancelPlanned 将尚未启动的 planned 租约原子取消，并在运行完成时释放预留。
func (book *RoutingLeaseBook) CancelPlanned(runID, unitID string, at time.Time) (RoutingLease, error) {
	if book == nil {
		return RoutingLease{}, fmt.Errorf("routing lease book must not be nil")
	}
	if !validRoutingRunID(runID) {
		return RoutingLease{}, fmt.Errorf("routing lease run id %q is invalid", runID)
	}
	if !validExecutionUnitID(unitID) {
		return RoutingLease{}, fmt.Errorf("routing lease unit id %q is invalid", unitID)
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	run, lease, atUnixMilli, err := book.loadTransitionLocked(runID, unitID, at)
	if err != nil {
		return RoutingLease{}, err
	}
	next, err := cancelledRoutingLease(lease, unitID, atUnixMilli)
	if err != nil {
		return RoutingLease{}, err
	}
	if err := next.ValidateAgainst(run.decision, at); err != nil {
		return RoutingLease{}, fmt.Errorf("validate cancelled routing lease %q: %w", unitID, err)
	}
	if err := book.commitLeaseTransitionLocked(run, unitID, next); err != nil {
		return RoutingLease{}, err
	}
	return next, nil
}

// loadTransitionLocked 在锁内读取运行、租约和当前操作时间戳。
func (book *RoutingLeaseBook) loadTransitionLocked(runID, unitID string, at time.Time) (*routingLeaseRun, RoutingLease, int64, error) {
	if err := book.validateTransitionMapsLocked(); err != nil {
		return nil, RoutingLease{}, 0, err
	}
	run, err := book.routingLeaseRunLocked(runID)
	if err != nil {
		return nil, RoutingLease{}, 0, err
	}
	lease, err := run.leaseLocked(unitID)
	if err != nil {
		return nil, RoutingLease{}, 0, err
	}
	atUnixMilli, err := checkedUnixMilli(at)
	if err != nil {
		return nil, RoutingLease{}, 0, fmt.Errorf("convert routing lease transition time: %w", err)
	}
	return run, lease, atUnixMilli, nil
}

// validateStartBinding 校验决策、租约、快照和工作单元的完整启动边界。
func validateStartBinding(run *routingLeaseRun, lease RoutingLease, unitID string, currentSnapshot SnapshotSet, at time.Time) error {
	expected, err := routingLeaseAssignment(run.decision.Assignments, unitID)
	if err != nil {
		return err
	}
	if !routingLeaseMatchesAssignment(lease, expected) {
		return fmt.Errorf("routing lease unit %q identity does not match decision assignment", unitID)
	}
	if err := currentSnapshot.ValidateAgainst(run.manifest, at, run.heartbeatTTL); err != nil {
		return fmt.Errorf("validate current routing snapshot: %w", err)
	}
	if currentSnapshot.CapturedAtUnixMilli < run.decision.SnapshotCapturedAtUnixMilli {
		return fmt.Errorf("current routing snapshot capture precedes decision snapshot")
	}
	unit, err := routingLeaseExecutionUnit(run.units, unitID)
	if err != nil {
		return err
	}
	switch expected.Executor {
	case ExecutorKindLocalNode:
		return validateLocalStartBinding(expected, unit, currentSnapshot)
	case ExecutorKindAliyunECI:
		return validateECIStartBinding(expected, unit)
	default:
		return fmt.Errorf("routing lease unit %q executor %q is unknown", unitID, expected.Executor)
	}
}

// routingLeaseExecutionUnit 从簿内深拷贝工作单元中取出唯一目标。
func routingLeaseExecutionUnit(units []ExecutionUnit, unitID string) (ExecutionUnit, error) {
	if units == nil {
		return ExecutionUnit{}, fmt.Errorf("routing lease run execution units must not be nil")
	}
	var match ExecutionUnit
	found := false
	for _, unit := range units {
		if unit.UnitID != unitID {
			continue
		}
		if found {
			return ExecutionUnit{}, fmt.Errorf("routing lease execution unit %q is duplicated", unitID)
		}
		match = unit
		found = true
	}
	if !found {
		return ExecutionUnit{}, fmt.Errorf("routing lease execution unit %q is unknown", unitID)
	}
	return match, nil
}

// validateLocalStartBinding 校验本地节点四身份和当前运行时能力。
func validateLocalStartBinding(assignment RouteAssignment, unit ExecutionUnit, snapshot SnapshotSet) error {
	if assignment.ECITier != "" {
		return fmt.Errorf("routing lease local start binding must not carry an ECI tier")
	}
	node, err := routingLeaseSnapshotNode(snapshot, assignment.NodeID)
	if err != nil {
		return err
	}
	if node.Endpoint != assignment.Endpoint {
		return routingLeaseStartFieldMismatch("endpoint")
	}
	if node.ProbeIdentity != assignment.ProbeIdentity {
		return routingLeaseStartFieldMismatch("probe_identity")
	}
	if node.ProbeBootID != assignment.ProbeBootID {
		return routingLeaseStartFieldMismatch("probe_boot_id")
	}
	if !executionUnitCanRunOnNode(unit, node) {
		return fmt.Errorf("routing lease local node cannot run execution unit")
	}
	return nil
}

// routingLeaseSnapshotNode 在当前快照按 NodeID 取出本地节点。
func routingLeaseSnapshotNode(snapshot SnapshotSet, nodeID string) (NodeSnapshot, error) {
	for _, node := range snapshot.Nodes {
		if node.NodeID == nodeID {
			return node, nil
		}
	}
	return NodeSnapshot{}, fmt.Errorf("routing lease local node is unknown")
}

// validateECIStartBinding 校验 ECI 无本地身份且使用规范资源档位。
func validateECIStartBinding(assignment RouteAssignment, unit ExecutionUnit) error {
	if assignment.NodeID != "" || assignment.Endpoint != "" || assignment.ProbeIdentity != "" || assignment.ProbeBootID != "" {
		return fmt.Errorf("routing lease ECI start binding must have empty local identity")
	}
	if _, ok := assignment.ECITier.Capacity(); !ok {
		return fmt.Errorf("routing lease ECI start tier is not canonical")
	}
	expectedTier, fits := smallestFittingECITier(unit)
	if !fits || assignment.ECITier != expectedTier {
		return fmt.Errorf("routing lease ECI start tier does not match execution unit")
	}
	return nil
}

// routingLeaseStartFieldMismatch 返回不回显节点端点或探针值的字段错误。
func routingLeaseStartFieldMismatch(field string) error {
	return fmt.Errorf("routing lease current snapshot %s mismatch", field)
}

// startedRoutingLease 构造并检查 planned 到 started 的时间约束。
func startedRoutingLease(lease RoutingLease, unitID string, atUnixMilli int64) (RoutingLease, error) {
	if lease.State != LeaseStatePlanned {
		return RoutingLease{}, fmt.Errorf("routing lease unit %q is not planned", unitID)
	}
	if atUnixMilli < lease.ReservedAtUnixMilli {
		return RoutingLease{}, fmt.Errorf("routing lease unit %q start time %d precedes reservation %d", unitID, atUnixMilli, lease.ReservedAtUnixMilli)
	}
	next := lease
	next.State = LeaseStateStarted
	next.StartedAtUnixMilli = atUnixMilli
	next.FinishedAtUnixMilli = 0
	return next, nil
}

// finishedRoutingLease 构造并检查 started 到 terminal 的时间约束。
func finishedRoutingLease(lease RoutingLease, unitID string, terminal RoutingLeaseState, atUnixMilli int64) (RoutingLease, error) {
	if lease.State != LeaseStateStarted {
		return RoutingLease{}, fmt.Errorf("routing lease unit %q is not started", unitID)
	}
	if atUnixMilli < lease.StartedAtUnixMilli {
		return RoutingLease{}, fmt.Errorf("routing lease unit %q finish time %d precedes start %d", unitID, atUnixMilli, lease.StartedAtUnixMilli)
	}
	next := lease
	next.State = terminal
	next.FinishedAtUnixMilli = atUnixMilli
	return next, nil
}

// cancelledRoutingLease 构造并检查 planned 到 cancelled 的时间约束。
func cancelledRoutingLease(lease RoutingLease, unitID string, atUnixMilli int64) (RoutingLease, error) {
	if lease.State != LeaseStatePlanned {
		return RoutingLease{}, fmt.Errorf("routing lease unit %q is not planned", unitID)
	}
	if atUnixMilli < lease.ReservedAtUnixMilli {
		return RoutingLease{}, fmt.Errorf("routing lease unit %q cancellation time %d precedes reservation %d", unitID, atUnixMilli, lease.ReservedAtUnixMilli)
	}
	next := lease
	next.State = LeaseStateCancelled
	next.StartedAtUnixMilli = 0
	next.FinishedAtUnixMilli = atUnixMilli
	return next, nil
}

// isRoutingLeaseTerminal 判断 Finish 允许提交的两种终态。
func isRoutingLeaseTerminal(state RoutingLeaseState) bool {
	return state == LeaseStateSucceeded || state == LeaseStateFailed
}

// Snapshot 返回指定运行按 UnitID 升序排列且与内部状态隔离的租约副本。
func (book *RoutingLeaseBook) Snapshot(runID string) ([]RoutingLease, error) {
	if book == nil {
		return nil, fmt.Errorf("routing lease book must not be nil")
	}
	if !validRoutingRunID(runID) {
		return nil, fmt.Errorf("routing lease run id %q is invalid", runID)
	}

	book.mu.Lock()
	defer book.mu.Unlock()
	if err := book.validateTransitionMapsLocked(); err != nil {
		return nil, err
	}
	if _, err := book.routingLeaseRunLocked(runID); err != nil {
		return nil, err
	}
	return snapshotRoutingLeaseRunLocked(book, runID)
}

// validateTransitionMapsLocked 检查租约簿的并发状态容器已正确初始化。
func (book *RoutingLeaseBook) validateTransitionMapsLocked() error {
	if book.runs == nil {
		return fmt.Errorf("routing lease book runs map must not be nil")
	}
	if book.reservations == nil {
		return fmt.Errorf("routing lease book reservations map must not be nil")
	}
	return nil
}

// routingLeaseRunLocked 取出已存在的运行；调用方必须持有 book.mu。
func (book *RoutingLeaseBook) routingLeaseRunLocked(runID string) (*routingLeaseRun, error) {
	run, exists := book.runs[runID]
	if !exists || run == nil {
		return nil, fmt.Errorf("routing lease run %q is unknown", runID)
	}
	if run.leases == nil {
		return nil, fmt.Errorf("routing lease run leases map must not be nil")
	}
	return run, nil
}

// leaseLocked 取出运行内已存在的单元租约；调用方必须持有 book.mu。
func (run *routingLeaseRun) leaseLocked(unitID string) (RoutingLease, error) {
	if run == nil {
		return RoutingLease{}, fmt.Errorf("routing lease run must not be nil")
	}
	if run.leases == nil {
		return RoutingLease{}, fmt.Errorf("routing lease run leases map must not be nil")
	}
	lease, exists := run.leases[unitID]
	if !exists {
		return RoutingLease{}, fmt.Errorf("routing lease unit %q is unknown", unitID)
	}
	return lease, nil
}

// routingLeaseMatchesAssignment 检查租约完整身份与决策绑定完全一致。
func routingLeaseMatchesAssignment(lease RoutingLease, assignment RouteAssignment) bool {
	return lease.UnitID == assignment.UnitID &&
		lease.Executor == assignment.Executor &&
		lease.NodeID == assignment.NodeID &&
		lease.Endpoint == assignment.Endpoint &&
		lease.ProbeIdentity == assignment.ProbeIdentity &&
		lease.ProbeBootID == assignment.ProbeBootID &&
		lease.ECITier == assignment.ECITier
}

// commitLeaseTransitionLocked 先预演终态释放，再一次性提交租约和容量变化。
func (book *RoutingLeaseBook) commitLeaseTransitionLocked(run *routingLeaseRun, unitID string, next RoutingLease) error {
	if book == nil {
		return fmt.Errorf("routing lease book must not be nil")
	}
	if run == nil || run.leases == nil {
		return fmt.Errorf("routing lease run or leases map is nil")
	}
	nextLeases := nextRoutingLeaseMap(run.leases, unitID, next)
	complete, err := routingLeaseRunComplete(nextLeases)
	if err != nil {
		return err
	}
	if run.released && !complete {
		return fmt.Errorf("routing lease run is marked released before terminal completion")
	}
	if !complete || run.released {
		run.leases[unitID] = next
		return nil
	}
	releasePlan, err := book.planReservationReleaseLocked(run)
	if err != nil {
		return err
	}
	run.leases[unitID] = next
	applyReservationReleaseLocked(book, releasePlan)
	run.released = true
	return nil
}

// nextRoutingLeaseMap 构造终态预演副本，避免验证失败污染运行历史。
func nextRoutingLeaseMap(source map[string]RoutingLease, unitID string, next RoutingLease) map[string]RoutingLease {
	clone := maps.Clone(source)
	clone[unitID] = next
	return clone
}

// applyReservationReleaseLocked 提交已通过预演的节点容量释放。
func applyReservationReleaseLocked(book *RoutingLeaseBook, releasePlan map[nodeReservationKey]ResourceCapacity) {
	for key, capacity := range releasePlan {
		if capacity == (ResourceCapacity{}) {
			delete(book.reservations, key)
			continue
		}
		book.reservations[key] = capacity
	}
}

// routingLeaseRunComplete 判断运行内每个租约是否已进入终态。
func routingLeaseRunComplete(leases map[string]RoutingLease) (bool, error) {
	if leases == nil {
		return false, fmt.Errorf("routing lease leases map must not be nil")
	}
	if len(leases) == 0 {
		return false, fmt.Errorf("routing lease run must contain leases")
	}
	for unitID, lease := range leases {
		if unitID == "" || lease.UnitID != unitID {
			return false, fmt.Errorf("routing lease map key %q does not match lease unit %q", unitID, lease.UnitID)
		}
		switch lease.State {
		case LeaseStateSucceeded, LeaseStateFailed, LeaseStateCancelled:
		case LeaseStatePlanned, LeaseStateStarted:
			return false, nil
		default:
			return false, fmt.Errorf("routing lease unit %q has unknown state %q", unitID, lease.State)
		}
	}
	return true, nil
}

// planReservationReleaseLocked 预演整次运行的容量释放，任何不足均不修改状态。
func (book *RoutingLeaseBook) planReservationReleaseLocked(run *routingLeaseRun) (map[nodeReservationKey]ResourceCapacity, error) {
	if book.reservations == nil {
		return nil, fmt.Errorf("routing lease book reservations map must not be nil")
	}
	if run == nil {
		return nil, fmt.Errorf("routing lease run must not be nil")
	}
	released := make(map[nodeReservationKey]ResourceCapacity, len(run.reservations))
	seen := make(map[nodeReservationKey]struct{}, len(run.reservations))
	for index, reservation := range run.reservations {
		if _, duplicate := seen[reservation.key]; duplicate {
			return nil, fmt.Errorf("routing lease run reservation at index %d is duplicated", index)
		}
		seen[reservation.key] = struct{}{}
		current, exists := book.reservations[reservation.key]
		if !exists {
			return nil, fmt.Errorf("routing lease reservation for node %q is missing", reservation.key.nodeID)
		}
		next, err := checkedSubtractReservation(current, reservation.capacity)
		if err != nil {
			return nil, fmt.Errorf("release routing lease reservation for node %q: %w", reservation.key.nodeID, err)
		}
		released[reservation.key] = next
	}
	return released, nil
}

// checkedSubtractReservation 校验容量非负且执行不可下溢的逐项减法。
func checkedSubtractReservation(current, requested ResourceCapacity) (ResourceCapacity, error) {
	if capacityHasNegative(current) || capacityHasNegative(requested) {
		return ResourceCapacity{}, fmt.Errorf("reservation capacities must be non-negative")
	}
	if current.CPUMilli < requested.CPUMilli || current.MemoryBytes < requested.MemoryBytes || current.MaxConcurrency < requested.MaxConcurrency {
		return ResourceCapacity{}, fmt.Errorf("reservation capacity release would underflow")
	}
	return ResourceCapacity{
		CPUMilli:       current.CPUMilli - requested.CPUMilli,
		MemoryBytes:    current.MemoryBytes - requested.MemoryBytes,
		MaxConcurrency: current.MaxConcurrency - requested.MaxConcurrency,
	}, nil
}
