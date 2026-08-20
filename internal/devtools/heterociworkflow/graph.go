package heterociworkflow

import (
	"errors"
	"fmt"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heterociowner"
)

// BatchPlan 是一个不可拆分的节点波次或单 unit ECI 执行批次。
// Batch 内所有 unit 必须先逐一 Start，再恰好调用一次 BatchExecutor.ExecuteBatch。
type BatchPlan struct {
	Batch   ExecutionBatch
	Planned []heteroci.RoutingLease
}

// BuildBatches 将 durable plan/decision/lease 闭合为确定性的执行批次图。
// local NodeShard 的每个 wave 是一个 batch；ECI assignment 固定为单 unit batch。
func BuildBatches(
	runID string,
	manifest heteroci.Manifest,
	planned heterociowner.DurableReservedRoutingPlan,
	units []heteroci.ExecutionUnit,
	now time.Time,
	heartbeatTTL time.Duration,
) ([]BatchPlan, error) {
	if err := validateBatchInputs(runID, manifest, planned, units, now, heartbeatTTL); err != nil {
		return nil, err
	}
	unitIndex := indexBatchUnits(units)
	assignments, err := indexBatchAssignments(planned.Decision.Assignments)
	if err != nil {
		return nil, err
	}
	leases, err := indexBatchLeases(runID, planned.Decision, planned.Leases, now)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(units))
	batches := make([]BatchPlan, 0, len(planned.Plan.NodeShards)+len(planned.Plan.UnassignedUnitIDs))
	for _, shard := range planned.Plan.NodeShards {
		for _, wave := range shard.Waves {
			batch, plannedLeases, err := buildLocalBatch(runID, shard, wave, unitIndex, assignments, leases, seen)
			if err != nil {
				return nil, err
			}
			batches = append(batches, BatchPlan{Batch: batch, Planned: plannedLeases})
		}
	}
	for _, unitID := range planned.Plan.UnassignedUnitIDs {
		batch, plannedLease, err := buildECIBatch(runID, unitID, unitIndex, assignments, leases, seen)
		if err != nil {
			return nil, err
		}
		batches = append(batches, BatchPlan{Batch: batch, Planned: []heteroci.RoutingLease{plannedLease}})
	}
	if err := validateBatchCoverage(units, assignments, leases, seen); err != nil {
		return nil, err
	}
	return batches, nil
}

// validateBatchInputs 校验 batch graph 的 owner fence、快照、规划和路由投影。
func validateBatchInputs(runID string, manifest heteroci.Manifest, planned heterociowner.DurableReservedRoutingPlan, units []heteroci.ExecutionUnit, now time.Time, heartbeatTTL time.Duration) error {
	if err := validateBatchIdentity(runID, manifest, planned, units); err != nil {
		return err
	}
	if err := validateBatchSnapshots(manifest, planned, now, heartbeatTTL); err != nil {
		return err
	}
	return validateBatchRoutingProjection(manifest, planned, units, now, heartbeatTTL)
}

// validateBatchIdentity 校验 batch graph 的 run、manifest、unit、fence 与 operation identity。
func validateBatchIdentity(runID string, manifest heteroci.Manifest, planned heterociowner.DurableReservedRoutingPlan, units []heteroci.ExecutionUnit) error {
	if err := validateWorkflowRunID(runID); err != nil {
		return err
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("validate workflow batch manifest: %w", err)
	}
	if err := heteroci.ValidateExecutionUnits(units); err != nil {
		return fmt.Errorf("validate workflow batch units: %w", err)
	}
	if err := planned.Fence.Validate(); err != nil {
		return fmt.Errorf("validate workflow batch fence: %w", err)
	}
	if err := planned.OperationID.Validate(); err != nil {
		return errors.New("heteroci workflow durable plan operation is missing")
	}
	return nil
}

// validateBatchSnapshots 校验 source/effective snapshot 的 owner epoch 与实时有效性。
func validateBatchSnapshots(manifest heteroci.Manifest, planned heterociowner.DurableReservedRoutingPlan, now time.Time, heartbeatTTL time.Duration) error {
	if planned.EffectiveSnapshot.OwnerEpoch != planned.Fence.OwnerEpoch || planned.SourceSnapshot.OwnerEpoch != planned.Fence.OwnerEpoch {
		return errors.New("heteroci workflow batch snapshot owner epoch drifted")
	}
	if err := planned.SourceSnapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate workflow source snapshot: %w", err)
	}
	if err := planned.EffectiveSnapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate workflow effective snapshot: %w", err)
	}
	return nil
}

// validateBatchRoutingProjection 校验 packing、decision 与 lease 集合的完整绑定。
func validateBatchRoutingProjection(manifest heteroci.Manifest, planned heterociowner.DurableReservedRoutingPlan, units []heteroci.ExecutionUnit, now time.Time, heartbeatTTL time.Duration) error {
	if err := planned.Plan.ValidateAgainst(manifest, planned.EffectiveSnapshot, units, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate workflow packing plan: %w", err)
	}
	if err := planned.Decision.ValidateAgainst(manifest, planned.EffectiveSnapshot, units, planned.Plan, now, heartbeatTTL); err != nil {
		return fmt.Errorf("validate workflow routing decision: %w", err)
	}
	if planned.Leases == nil {
		return errors.New("heteroci workflow durable leases are nil")
	}
	return nil
}

// indexBatchUnits 建立已验证工作单元的稳定索引。
func indexBatchUnits(units []heteroci.ExecutionUnit) map[string]heteroci.ExecutionUnit {
	index := make(map[string]heteroci.ExecutionUnit, len(units))
	for _, unit := range units {
		index[unit.UnitID] = unit
	}
	return index
}

// indexBatchAssignments 建立唯一路由 assignment 索引并拒绝重复 unit。
func indexBatchAssignments(assignments []heteroci.RouteAssignment) (map[string]heteroci.RouteAssignment, error) {
	if assignments == nil {
		return nil, errors.New("heteroci workflow routing assignments are nil")
	}
	index := make(map[string]heteroci.RouteAssignment, len(assignments))
	for _, assignment := range assignments {
		if !validWorkflowUnitID(assignment.UnitID) {
			return nil, errors.New("heteroci workflow routing assignment unit is invalid")
		}
		if _, exists := index[assignment.UnitID]; exists {
			return nil, errors.New("heteroci workflow routing assignment is duplicated")
		}
		index[assignment.UnitID] = assignment
	}
	return index, nil
}

// indexBatchLeases 建立 planned lease 索引并校验每个 lease 的决策绑定。
func indexBatchLeases(runID string, decision heteroci.RoutingDecision, leases []heteroci.RoutingLease, now time.Time) (map[string]heteroci.RoutingLease, error) {
	index := make(map[string]heteroci.RoutingLease, len(leases))
	for _, lease := range leases {
		if lease.RunID != runID || lease.State != heteroci.LeaseStatePlanned {
			return nil, errors.New("heteroci workflow durable lease is not planned for requested run")
		}
		if _, exists := index[lease.UnitID]; exists {
			return nil, errors.New("heteroci workflow durable lease is duplicated")
		}
		if err := lease.ValidateAgainst(decision, now); err != nil {
			return nil, fmt.Errorf("validate workflow durable lease: %w", err)
		}
		index[lease.UnitID] = lease
	}
	return index, nil
}

// buildLocalBatch 构造一个节点 wave batch，保持规划中的 unit 顺序。
func buildLocalBatch(runID string, shard heteroci.NodeShard, wave heteroci.ExecutionWave, unitIndex map[string]heteroci.ExecutionUnit, assignments map[string]heteroci.RouteAssignment, leases map[string]heteroci.RoutingLease, seen map[string]struct{}) (ExecutionBatch, []heteroci.RoutingLease, error) {
	key := ExecutorKey{Kind: heteroci.ExecutorKindLocalNode, NodeID: shard.NodeID}
	if err := key.Validate(); err != nil {
		return ExecutionBatch{}, nil, err
	}
	material, err := collectLocalBatchMaterial(shard.NodeID, wave.UnitIDs, unitIndex, assignments, leases, seen)
	if err != nil {
		return ExecutionBatch{}, nil, err
	}
	batchID, err := DeriveBatchID(runID, key, wave.Wave, wave.UnitIDs)
	if err != nil {
		return ExecutionBatch{}, nil, err
	}
	return ExecutionBatch{BatchID: batchID, RunID: runID, Key: key, Wave: wave.Wave, Units: material.units, Assignments: material.assignments}, material.leases, nil
}

type localBatchMaterial struct {
	units       []heteroci.ExecutionUnit
	assignments []heteroci.RouteAssignment
	leases      []heteroci.RoutingLease
}

// collectLocalBatchMaterial 收集一个 wave 的 unit、assignment 和 lease 三路投影。
func collectLocalBatchMaterial(nodeID string, unitIDs []string, unitIndex map[string]heteroci.ExecutionUnit, assignments map[string]heteroci.RouteAssignment, leases map[string]heteroci.RoutingLease, seen map[string]struct{}) (localBatchMaterial, error) {
	batchUnits := make([]heteroci.ExecutionUnit, 0, len(unitIDs))
	batchAssignments := make([]heteroci.RouteAssignment, 0, len(unitIDs))
	batchLeases := make([]heteroci.RoutingLease, 0, len(unitIDs))
	for _, unitID := range unitIDs {
		unit, assignment, lease, err := resolveLocalBatchUnit(nodeID, unitID, unitIndex, assignments, leases, seen)
		if err != nil {
			return localBatchMaterial{}, err
		}
		batchUnits = append(batchUnits, unit)
		batchAssignments = append(batchAssignments, assignment)
		batchLeases = append(batchLeases, lease)
	}
	return localBatchMaterial{units: batchUnits, assignments: batchAssignments, leases: batchLeases}, nil
}

// resolveLocalBatchUnit 校验并返回一个 local unit 的三路绑定。
func resolveLocalBatchUnit(nodeID, unitID string, unitIndex map[string]heteroci.ExecutionUnit, assignments map[string]heteroci.RouteAssignment, leases map[string]heteroci.RoutingLease, seen map[string]struct{}) (heteroci.ExecutionUnit, heteroci.RouteAssignment, heteroci.RoutingLease, error) {
	unit, ok := unitIndex[unitID]
	if !ok {
		return heteroci.ExecutionUnit{}, heteroci.RouteAssignment{}, heteroci.RoutingLease{}, errors.New("heteroci workflow local batch references unknown unit")
	}
	assignment, ok := assignments[unitID]
	if !validLocalAssignment(ok, assignment, nodeID) {
		return heteroci.ExecutionUnit{}, heteroci.RouteAssignment{}, heteroci.RoutingLease{}, errors.New("heteroci workflow local batch assignment drifted")
	}
	lease, ok := leases[unitID]
	if !ok {
		return heteroci.ExecutionUnit{}, heteroci.RouteAssignment{}, heteroci.RoutingLease{}, errors.New("heteroci workflow local batch lease is missing")
	}
	if _, duplicate := seen[unitID]; duplicate {
		return heteroci.ExecutionUnit{}, heteroci.RouteAssignment{}, heteroci.RoutingLease{}, errors.New("heteroci workflow unit appears in more than one batch")
	}
	seen[unitID] = struct{}{}
	return unit, assignment, lease, nil
}

// validLocalAssignment 判断 assignment 是否绑定当前 local node 且不携带 ECI tier。
func validLocalAssignment(found bool, assignment heteroci.RouteAssignment, nodeID string) bool {
	return found && assignment.Executor == heteroci.ExecutorKindLocalNode && assignment.NodeID == nodeID && assignment.ECITier == ""
}

// buildECIBatch 构造一个固定单 unit 的 ECI batch。
func buildECIBatch(runID, unitID string, unitIndex map[string]heteroci.ExecutionUnit, assignments map[string]heteroci.RouteAssignment, leases map[string]heteroci.RoutingLease, seen map[string]struct{}) (ExecutionBatch, heteroci.RoutingLease, error) {
	unit, assignment, lease, err := resolveECIBatchUnit(unitID, unitIndex, assignments, leases, seen)
	if err != nil {
		return ExecutionBatch{}, heteroci.RoutingLease{}, err
	}
	key := ExecutorKey{Kind: heteroci.ExecutorKindAliyunECI, ECITier: assignment.ECITier}
	batchID, err := DeriveBatchID(runID, key, -1, []string{unitID})
	if err != nil {
		return ExecutionBatch{}, heteroci.RoutingLease{}, err
	}
	return ExecutionBatch{BatchID: batchID, RunID: runID, Key: key, Wave: -1, Units: []heteroci.ExecutionUnit{unit}, Assignments: []heteroci.RouteAssignment{assignment}}, lease, nil
}

// resolveECIBatchUnit 校验并返回一个 ECI unit 的三路绑定。
func resolveECIBatchUnit(unitID string, unitIndex map[string]heteroci.ExecutionUnit, assignments map[string]heteroci.RouteAssignment, leases map[string]heteroci.RoutingLease, seen map[string]struct{}) (heteroci.ExecutionUnit, heteroci.RouteAssignment, heteroci.RoutingLease, error) {
	unit, ok := unitIndex[unitID]
	if !ok {
		return heteroci.ExecutionUnit{}, heteroci.RouteAssignment{}, heteroci.RoutingLease{}, errors.New("heteroci workflow ECI batch references unknown unit")
	}
	assignment, ok := assignments[unitID]
	if !validECIAssignment(ok, assignment) {
		return heteroci.ExecutionUnit{}, heteroci.RouteAssignment{}, heteroci.RoutingLease{}, errors.New("heteroci workflow ECI batch assignment drifted")
	}
	lease, ok := leases[unitID]
	if !ok {
		return heteroci.ExecutionUnit{}, heteroci.RouteAssignment{}, heteroci.RoutingLease{}, errors.New("heteroci workflow ECI batch lease is missing")
	}
	if _, duplicate := seen[unitID]; duplicate {
		return heteroci.ExecutionUnit{}, heteroci.RouteAssignment{}, heteroci.RoutingLease{}, errors.New("heteroci workflow unit appears in more than one batch")
	}
	seen[unitID] = struct{}{}
	return unit, assignment, lease, nil
}

// validECIAssignment 判断 ECI assignment 是否没有伪造本地节点身份。
func validECIAssignment(found bool, assignment heteroci.RouteAssignment) bool {
	return found && assignment.Executor == heteroci.ExecutorKindAliyunECI && assignment.NodeID == "" && assignment.Endpoint == "" && assignment.ProbeIdentity == "" && assignment.ProbeBootID == ""
}

// validateBatchCoverage 确保每个 unit、assignment 和 durable lease 恰好被消费一次。
func validateBatchCoverage(units []heteroci.ExecutionUnit, assignments map[string]heteroci.RouteAssignment, leases map[string]heteroci.RoutingLease, seen map[string]struct{}) error {
	if len(seen) != len(units) || len(assignments) != len(units) || len(leases) != len(units) {
		return errors.New("heteroci workflow batch graph does not cover every unit exactly once")
	}
	for _, unit := range units {
		if _, ok := seen[unit.UnitID]; !ok {
			return errors.New("heteroci workflow batch graph leaves a unit uncovered")
		}
	}
	return nil
}
