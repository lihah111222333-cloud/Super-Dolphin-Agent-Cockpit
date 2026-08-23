package heteroci

import (
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"
)

// RoutingLeaseBook 保存本地异构路由运行和节点容量预留。
type RoutingLeaseBook struct {
	mu           sync.Mutex
	runs         map[string]*routingLeaseRun
	reservations map[nodeReservationKey]ResourceCapacity
}

// ReservedRoutingPlan 保存一次已原子预留的非 wire 规划结果及其有效容量快照。
type ReservedRoutingPlan struct {
	SourceSnapshot    SnapshotSet
	EffectiveSnapshot SnapshotSet
	Plan              PackingPlan
	Decision          RoutingDecision
	Leases            []RoutingLease
}

// routingLeaseRun 保存单次路由决策的租约和容量预留。
type routingLeaseRun struct {
	decision     RoutingDecision
	leases       map[string]RoutingLease
	reservations []nodeCapacityReservation
	manifest     Manifest
	units        []ExecutionUnit
	heartbeatTTL time.Duration
	released     bool
}

// NewRoutingLeaseBook 创建一个空的路由租约簿。
func NewRoutingLeaseBook() *RoutingLeaseBook {
	return &RoutingLeaseBook{
		runs:         make(map[string]*routingLeaseRun),
		reservations: make(map[nodeReservationKey]ResourceCapacity),
	}
}

// Reserve 校验并原子预留一次路由决策的本地节点容量。
func (book *RoutingLeaseBook) Reserve(
	runID string,
	decision RoutingDecision,
	manifest Manifest,
	snapshot SnapshotSet,
	units []ExecutionUnit,
	plan PackingPlan,
	now time.Time,
	heartbeatTTL time.Duration,
) ([]RoutingLease, error) {
	if err := decision.ValidateAgainst(manifest, snapshot, units, plan, now, heartbeatTTL); err != nil {
		return nil, fmt.Errorf("validate routing decision for lease reservation: %w", err)
	}
	if !validRoutingRunID(runID) {
		return nil, fmt.Errorf("routing lease run id %q is invalid", runID)
	}
	nowUnixMilli, err := checkedUnixMilli(now)
	if err != nil {
		return nil, fmt.Errorf("convert routing lease reservation time: %w", err)
	}
	reservations, err := routingNodeCapacityReservations(plan, snapshot)
	if err != nil {
		return nil, fmt.Errorf("build routing node capacity reservations: %w", err)
	}
	leases, err := buildPlannedRoutingLeases(runID, decision, nowUnixMilli)
	if err != nil {
		return nil, err
	}
	return book.commitRoutingLeaseReservation(runID, decision, leases, reservations, snapshot, manifest, units, heartbeatTTL)
}

// PlanAndReserve 真实规划路由并原子保存对应租约。
func (book *RoutingLeaseBook) PlanAndReserve(
	runID string,
	manifest Manifest,
	sourceSnapshot SnapshotSet,
	units []ExecutionUnit,
	now time.Time,
	heartbeatTTL time.Duration,
) (ReservedRoutingPlan, error) {
	nowUnixMilli, err := validatePlanAndReserveInput(book, runID, now)
	if err != nil {
		return ReservedRoutingPlan{}, err
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	return book.planAndReserveLocked(runID, manifest, sourceSnapshot, units, now, heartbeatTTL, nowUnixMilli)
}

// validatePlanAndReserveInput 在取得容量锁前校验不依赖簿状态的输入。
func validatePlanAndReserveInput(book *RoutingLeaseBook, runID string, now time.Time) (int64, error) {
	if book == nil {
		return 0, fmt.Errorf("routing lease book is nil")
	}
	if !validRoutingRunID(runID) {
		return 0, fmt.Errorf("routing lease run id %q is invalid", runID)
	}
	nowUnixMilli, err := checkedUnixMilli(now)
	if err != nil {
		return 0, fmt.Errorf("convert routing lease reservation time: %w", err)
	}
	return nowUnixMilli, nil
}

// planAndReserveLocked 基于同一容量状态规划、预演并一次提交；调用方必须持有 book.mu。
func (book *RoutingLeaseBook) planAndReserveLocked(
	runID string,
	manifest Manifest,
	sourceSnapshot SnapshotSet,
	units []ExecutionUnit,
	now time.Time,
	heartbeatTTL time.Duration,
	nowUnixMilli int64,
) (ReservedRoutingPlan, error) {
	if book.runs == nil || book.reservations == nil {
		return ReservedRoutingPlan{}, fmt.Errorf("routing lease book maps must be non-nil")
	}
	if _, exists := book.runs[runID]; exists {
		return ReservedRoutingPlan{}, fmt.Errorf("routing lease run id %q already exists", runID)
	}
	if err := sourceSnapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return ReservedRoutingPlan{}, fmt.Errorf("validate routing source snapshot: %w", err)
	}
	effectiveSnapshot, err := effectiveRoutingSnapshot(sourceSnapshot, book.reservations)
	if err != nil {
		return ReservedRoutingPlan{}, err
	}
	plan, decision, err := PlanRoutingDecision(manifest, effectiveSnapshot, units, now, heartbeatTTL)
	if err != nil {
		return ReservedRoutingPlan{}, err
	}
	requested, err := routingNodeCapacityReservations(plan, effectiveSnapshot)
	if err != nil {
		return ReservedRoutingPlan{}, fmt.Errorf("build routing node capacity reservations: %w", err)
	}
	leases, err := buildPlannedRoutingLeases(runID, decision, nowUnixMilli)
	if err != nil {
		return ReservedRoutingPlan{}, err
	}
	committed, err := book.commitRoutingLeaseReservationLocked(runID, decision, leases, requested, sourceSnapshot, manifest, units, heartbeatTTL)
	if err != nil {
		return ReservedRoutingPlan{}, err
	}
	return ReservedRoutingPlan{
		SourceSnapshot:    cloneRoutingSnapshotSet(sourceSnapshot),
		EffectiveSnapshot: cloneRoutingSnapshotSet(effectiveSnapshot),
		Plan:              cloneRoutingPackingPlan(plan),
		Decision:          cloneRoutingDecision(decision),
		Leases:            cloneRoutingSlice(committed),
	}, nil
}

// buildPlannedRoutingLeases 将决策中的每个绑定转换为 planned 租约。
func buildPlannedRoutingLeases(runID string, decision RoutingDecision, reservedAtUnixMilli int64) (map[string]RoutingLease, error) {
	leases := make(map[string]RoutingLease, len(decision.Assignments))
	for _, assignment := range decision.Assignments {
		if _, exists := leases[assignment.UnitID]; exists {
			return nil, fmt.Errorf("routing decision assigns execution unit %q more than once", assignment.UnitID)
		}
		leases[assignment.UnitID] = RoutingLease{
			SchemaVersion:       RoutingLeaseSchemaVersion,
			Authority:           AuthorityLocalNonAuthoritative,
			RunID:               runID,
			DecisionDigest:      decision.DecisionDigest,
			UnitID:              assignment.UnitID,
			Executor:            assignment.Executor,
			NodeID:              assignment.NodeID,
			Endpoint:            assignment.Endpoint,
			ProbeIdentity:       assignment.ProbeIdentity,
			ProbeBootID:         assignment.ProbeBootID,
			State:               LeaseStatePlanned,
			ReservedAtUnixMilli: reservedAtUnixMilli,
		}
	}
	return leases, nil
}

// commitRoutingLeaseReservation 在锁内预演容量并一次提交运行状态。
func (book *RoutingLeaseBook) commitRoutingLeaseReservation(
	runID string,
	decision RoutingDecision,
	leases map[string]RoutingLease,
	requested []nodeCapacityReservation,
	snapshot SnapshotSet,
	manifest Manifest,
	units []ExecutionUnit,
	heartbeatTTL time.Duration,
) ([]RoutingLease, error) {
	if book == nil {
		return nil, fmt.Errorf("routing lease book is nil")
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	return book.commitRoutingLeaseReservationLocked(runID, decision, leases, requested, snapshot, manifest, units, heartbeatTTL)
}

// commitRoutingLeaseReservationLocked 预演全部容量后一次提交；调用方必须持有 book.mu。
func (book *RoutingLeaseBook) commitRoutingLeaseReservationLocked(
	runID string,
	decision RoutingDecision,
	leases map[string]RoutingLease,
	requested []nodeCapacityReservation,
	snapshot SnapshotSet,
	manifest Manifest,
	units []ExecutionUnit,
	heartbeatTTL time.Duration,
) ([]RoutingLease, error) {
	if book.runs == nil || book.reservations == nil {
		return nil, fmt.Errorf("routing lease book maps must be non-nil")
	}
	if _, exists := book.runs[runID]; exists {
		return nil, fmt.Errorf("routing lease run id %q already exists", runID)
	}
	nextReservations := cloneReservationMap(book.reservations, len(requested))
	for _, reservation := range requested {
		limit, err := reservationAvailableCapacity(snapshot, reservation.key)
		if err != nil {
			return nil, err
		}
		updated, err := checkedAddReservation(nextReservations[reservation.key], reservation.capacity, limit)
		if err != nil {
			return nil, fmt.Errorf("reserve node %q capacity: %w", reservation.key.nodeID, err)
		}
		nextReservations[reservation.key] = updated
	}
	result := sortedRoutingLeaseValues(leases)
	book.reservations = nextReservations
	book.runs[runID] = &routingLeaseRun{
		decision:     cloneRoutingDecision(decision),
		leases:       cloneRoutingLeaseMap(leases),
		reservations: append([]nodeCapacityReservation(nil), requested...),
		manifest:     cloneRoutingManifest(manifest),
		units:        cloneRoutingExecutionUnits(units),
		heartbeatTTL: heartbeatTTL,
	}
	return result, nil
}

// effectiveRoutingSnapshot 从来源快照扣除同一探针生命周期内尚未释放的本簿预留。
func effectiveRoutingSnapshot(source SnapshotSet, current map[nodeReservationKey]ResourceCapacity) (SnapshotSet, error) {
	if current == nil {
		return SnapshotSet{}, fmt.Errorf("routing lease book reservations map must be non-nil")
	}
	effective := cloneRoutingSnapshotSet(source)
	for index := range effective.Nodes {
		node := &effective.Nodes[index]
		key := nodeReservationKey{
			nodeID:        node.NodeID,
			probeIdentity: node.ProbeIdentity,
			probeBootID:   node.ProbeBootID,
		}
		reserved := current[key]
		if reserved == (ResourceCapacity{}) {
			continue
		}
		available, err := checkedSubtractReservation(node.Available, reserved)
		if err != nil {
			return SnapshotSet{}, fmt.Errorf("subtract routing lease reservations from node %q source capacity: %w", node.NodeID, err)
		}
		node.Available = available
	}
	return effective, nil
}

// cloneRoutingSnapshotSet 深拷贝快照中的节点、能力和资格回执。
func cloneRoutingSnapshotSet(source SnapshotSet) SnapshotSet {
	clone := source
	clone.Nodes = cloneRoutingSlice(source.Nodes)
	for nodeIndex := range clone.Nodes {
		active := source.Nodes[nodeIndex].ActiveCapabilities
		clone.Nodes[nodeIndex].ActiveCapabilities = cloneRoutingSlice(active)
		for capabilityIndex := range clone.Nodes[nodeIndex].ActiveCapabilities {
			receipt := source.Nodes[nodeIndex].ActiveCapabilities[capabilityIndex].Qualification
			if receipt == nil {
				continue
			}
			receiptClone := *receipt
			clone.Nodes[nodeIndex].ActiveCapabilities[capabilityIndex].Qualification = &receiptClone
		}
	}
	return clone
}

// cloneRoutingManifest 深拷贝清单节点及其能力切片，并保留 nil 与显式空切片区别。
func cloneRoutingManifest(source Manifest) Manifest {
	clone := source
	clone.Nodes = cloneRoutingSlice(source.Nodes)
	for nodeIndex := range clone.Nodes {
		clone.Nodes[nodeIndex].Capabilities = cloneRoutingSlice(source.Nodes[nodeIndex].Capabilities)
	}
	return clone
}

// cloneRoutingExecutionUnits 深拷贝规划上下文中的工作单元切片。
func cloneRoutingExecutionUnits(source []ExecutionUnit) []ExecutionUnit {
	return cloneRoutingSlice(source)
}

// cloneRoutingPackingPlan 深拷贝装箱计划中的节点、波次和工作单元切片。
func cloneRoutingPackingPlan(source PackingPlan) PackingPlan {
	clone := source
	clone.NodeShards = cloneRoutingSlice(source.NodeShards)
	for shardIndex := range clone.NodeShards {
		waves := source.NodeShards[shardIndex].Waves
		clone.NodeShards[shardIndex].Waves = cloneRoutingSlice(waves)
		for waveIndex := range clone.NodeShards[shardIndex].Waves {
			unitIDs := waves[waveIndex].UnitIDs
			clone.NodeShards[shardIndex].Waves[waveIndex].UnitIDs = cloneRoutingSlice(unitIDs)
		}
	}
	clone.UnassignedUnitIDs = cloneRoutingSlice(source.UnassignedUnitIDs)
	return clone
}

// cloneRoutingSlice 在复制切片的同时保留 nil 与显式空切片的区别。
func cloneRoutingSlice[T any](source []T) []T {
	if source == nil {
		return nil
	}
	clone := make([]T, len(source))
	copy(clone, source)
	return clone
}

// cloneReservationMap 复制容量预留以便在提交前执行事务式预演。
func cloneReservationMap(source map[nodeReservationKey]ResourceCapacity, extra int) map[nodeReservationKey]ResourceCapacity {
	clone := make(map[nodeReservationKey]ResourceCapacity, len(source)+extra)
	maps.Copy(clone, source)
	return clone
}

// reservationAvailableCapacity 按节点及探针身份精确读取本地可用容量。
func reservationAvailableCapacity(snapshot SnapshotSet, key nodeReservationKey) (ResourceCapacity, error) {
	for _, node := range snapshot.Nodes {
		if node.NodeID != key.nodeID {
			continue
		}
		if node.ProbeIdentity != key.probeIdentity || node.ProbeBootID != key.probeBootID {
			return ResourceCapacity{}, fmt.Errorf("reservation node %q probe identity does not match snapshot", key.nodeID)
		}
		return node.Available, nil
	}
	return ResourceCapacity{}, fmt.Errorf("reservation node %q is missing from snapshot", key.nodeID)
}

// cloneRoutingLeaseMap 复制租约值，避免运行状态与调用方共享 map。
func cloneRoutingLeaseMap(source map[string]RoutingLease) map[string]RoutingLease {
	clone := make(map[string]RoutingLease, len(source))
	maps.Copy(clone, source)
	return clone
}

// cloneRoutingDecision 复制决策及其绑定切片，隔离运行时状态。
func cloneRoutingDecision(source RoutingDecision) RoutingDecision {
	source.Assignments = cloneRoutingSlice(source.Assignments)
	return source
}

// sortedRoutingLeaseValues 按工作单元标识返回排序后的租约值切片。
func sortedRoutingLeaseValues(source map[string]RoutingLease) []RoutingLease {
	leases := make([]RoutingLease, 0, len(source))
	for _, lease := range source {
		leases = append(leases, lease)
	}
	sort.Slice(leases, func(left, right int) bool {
		return leases[left].UnitID < leases[right].UnitID
	})
	return leases
}

// snapshotRoutingLeaseRunLocked 返回指定运行的排序租约；调用方必须持有 book.mu。
func snapshotRoutingLeaseRunLocked(book *RoutingLeaseBook, runID string) ([]RoutingLease, error) {
	if book == nil {
		return nil, fmt.Errorf("routing lease book is nil")
	}
	if book.runs == nil {
		return nil, fmt.Errorf("routing lease book runs map is nil")
	}
	run, exists := book.runs[runID]
	if !exists || run == nil {
		return nil, fmt.Errorf("routing lease run id %q does not exist", runID)
	}
	if run.leases == nil {
		return nil, fmt.Errorf("routing lease run leases map is nil")
	}
	return sortedRoutingLeaseValues(run.leases), nil
}
