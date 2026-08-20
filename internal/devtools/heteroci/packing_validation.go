package heteroci

import (
	"fmt"
	"math"
	"time"
)

// ValidateAgainst 校验装箱规划与清单、快照及工作单元的严格一致性。
func (plan PackingPlan) ValidateAgainst(
	manifest Manifest,
	snapshot SnapshotSet,
	units []ExecutionUnit,
	now time.Time,
	heartbeatTTL time.Duration,
) error {
	indexes, err := validatePackingInputs(plan, manifest, snapshot, units, now, heartbeatTTL)
	if err != nil {
		return err
	}
	return validatePackingCoverage(plan, units, indexes)
}

type packingValidationIndexes struct {
	units     map[string]ExecutionUnit
	liveNodes map[string]NodeSnapshot
}

// validatePackingInputs 校验输入边界并建立规划校验所需的索引。
func validatePackingInputs(
	plan PackingPlan,
	manifest Manifest,
	snapshot SnapshotSet,
	units []ExecutionUnit,
	now time.Time,
	heartbeatTTL time.Duration,
) (packingValidationIndexes, error) {
	if err := manifest.Validate(); err != nil {
		return packingValidationIndexes{}, fmt.Errorf("validate packing manifest: %w", err)
	}
	if err := snapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return packingValidationIndexes{}, fmt.Errorf("validate packing snapshot: %w", err)
	}
	if err := ValidateExecutionUnits(units); err != nil {
		return packingValidationIndexes{}, fmt.Errorf("validate packing execution units: %w", err)
	}
	if err := validatePackingIdentity(plan, snapshot); err != nil {
		return packingValidationIndexes{}, err
	}
	unitIndex, err := indexExecutionUnits(units)
	if err != nil {
		return packingValidationIndexes{}, err
	}
	liveNodes, err := indexLiveSnapshots(snapshot)
	if err != nil {
		return packingValidationIndexes{}, err
	}
	return packingValidationIndexes{units: unitIndex, liveNodes: liveNodes}, nil
}

// validatePackingCoverage 校验节点分片、未分配单元及完整覆盖关系。
func validatePackingCoverage(plan PackingPlan, units []ExecutionUnit, indexes packingValidationIndexes) error {
	assigned := make(map[string]struct{}, len(units))
	if plan.NodeShards == nil || plan.UnassignedUnitIDs == nil {
		return fmt.Errorf("packing plan node_shards and unassigned_unit_ids must be non-nil")
	}
	if err := validatePackingShards(plan.NodeShards, indexes.units, indexes.liveNodes, assigned); err != nil {
		return err
	}
	if err := validatePackingUnassigned(plan.UnassignedUnitIDs, indexes.units, indexes.liveNodes, assigned); err != nil {
		return err
	}
	if len(assigned) != len(units) {
		return fmt.Errorf("packing plan covers %d of %d execution units", len(assigned), len(units))
	}
	return nil
}

// validatePackingIdentity 校验规划版本、权威边界和快照采集时刻。
func validatePackingIdentity(plan PackingPlan, snapshot SnapshotSet) error {
	if plan.SchemaVersion != PackingPlanSchemaVersion {
		return fmt.Errorf("packing plan schema_version %q is invalid", plan.SchemaVersion)
	}
	if plan.Authority != AuthorityLocalNonAuthoritative {
		return fmt.Errorf("packing plan authority %q is invalid", plan.Authority)
	}
	if plan.SnapshotCapturedAtUnixMilli != snapshot.CapturedAtUnixMilli {
		return fmt.Errorf("packing plan snapshot capture %d does not match snapshot %d", plan.SnapshotCapturedAtUnixMilli, snapshot.CapturedAtUnixMilli)
	}
	return nil
}

// indexExecutionUnits 建立唯一工作单元索引，并拒绝无法覆盖的空标识。
func indexExecutionUnits(units []ExecutionUnit) (map[string]ExecutionUnit, error) {
	index := make(map[string]ExecutionUnit, len(units))
	for _, unit := range units {
		if unit.UnitID == "" {
			return nil, fmt.Errorf("execution unit id must not be empty")
		}
		if _, exists := index[unit.UnitID]; exists {
			return nil, fmt.Errorf("execution unit id %q is duplicated", unit.UnitID)
		}
		index[unit.UnitID] = unit
	}
	return index, nil
}

// indexLiveSnapshots 索引快照节点并保留 live 节点作为规划候选。
func indexLiveSnapshots(snapshot SnapshotSet) (map[string]NodeSnapshot, error) {
	index := make(map[string]NodeSnapshot, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		if node.Status == NodeStatusLive {
			index[node.NodeID] = node
		}
	}
	return index, nil
}

// validatePackingShards 校验节点分片顺序、节点能力和所有已分配波次。
func validatePackingShards(
	shards []NodeShard,
	units map[string]ExecutionUnit,
	liveNodes map[string]NodeSnapshot,
	assigned map[string]struct{},
) error {
	previousNodeID := ""
	for index, shard := range shards {
		if index > 0 && shard.NodeID <= previousNodeID {
			return fmt.Errorf("packing node_shards must be strictly ascending by node_id")
		}
		if _, exists := liveNodes[shard.NodeID]; !exists {
			return fmt.Errorf("packing node shard node %q is not a live snapshot node", shard.NodeID)
		}
		if err := validateNodeShard(shard, liveNodes[shard.NodeID], units, assigned); err != nil {
			return err
		}
		previousNodeID = shard.NodeID
	}
	return nil
}

// validateNodeShard 校验单节点波次连续性、资源重算和关键路径总和。
func validateNodeShard(
	shard NodeShard,
	node NodeSnapshot,
	units map[string]ExecutionUnit,
	assigned map[string]struct{},
) error {
	if len(shard.Waves) == 0 {
		return fmt.Errorf("packing node shard %q waves must be non-nil and non-empty", shard.NodeID)
	}
	makespan := int64(0)
	for index, wave := range shard.Waves {
		if wave.Wave != index {
			return fmt.Errorf("packing node shard %q wave %d is not continuous", shard.NodeID, wave.Wave)
		}
		duration, err := validateExecutionWave(wave, node, units, assigned)
		if err != nil {
			return fmt.Errorf("validate packing node shard %q wave %d: %w", shard.NodeID, wave.Wave, err)
		}
		makespan, err = checkedPackingAdd(makespan, duration)
		if err != nil {
			return fmt.Errorf("packing node shard %q makespan overflows: %w", shard.NodeID, err)
		}
	}
	if shard.EstimatedMakespanMS != makespan {
		return fmt.Errorf("packing node shard %q makespan %d does not match checked wave sum %d", shard.NodeID, shard.EstimatedMakespanMS, makespan)
	}
	return nil
}

// validateExecutionWave 校验波次单元顺序、运行时可执行性及资源字段重算。
func validateExecutionWave(
	wave ExecutionWave,
	node NodeSnapshot,
	units map[string]ExecutionUnit,
	assigned map[string]struct{},
) (int64, error) {
	if err := validateExecutionWaveShape(wave); err != nil {
		return 0, err
	}
	aggregate, err := aggregateExecutionWave(wave, node, units, assigned)
	if err != nil {
		return 0, err
	}
	duration, err := finalizeExecutionWave(wave, node, aggregate)
	if err != nil {
		return 0, err
	}
	commitExecutionWaveUnits(assigned, aggregate.unitIDs)
	return duration, nil
}

// validateExecutionWaveShape 校验波次形状、并发数量及正数约束。
func validateExecutionWaveShape(wave ExecutionWave) error {
	if len(wave.UnitIDs) == 0 {
		return fmt.Errorf("wave unit_ids must be non-nil and non-empty")
	}
	if wave.Concurrency != len(wave.UnitIDs) || wave.Concurrency <= 0 {
		return fmt.Errorf("wave concurrency %d does not match unit count %d", wave.Concurrency, len(wave.UnitIDs))
	}
	if wave.CPUMilli <= 0 || wave.MemoryBytes <= 0 || wave.EstimatedDurationMS <= 0 {
		return fmt.Errorf("wave resources and duration must be positive")
	}
	return nil
}

type executionWaveAggregate struct {
	cpuMilli    int64
	memoryBytes int64
	durationMS  int64
	unitIDs     []string
}

// aggregateExecutionWave 按规范顺序校验单元并汇总资源与关键路径时长。
func aggregateExecutionWave(
	wave ExecutionWave,
	node NodeSnapshot,
	units map[string]ExecutionUnit,
	assigned map[string]struct{},
) (executionWaveAggregate, error) {
	var aggregate executionWaveAggregate
	pendingIDs := make([]string, 0, len(wave.UnitIDs))
	previousUnitID := ""
	for index, unitID := range wave.UnitIDs {
		if index > 0 && unitID <= previousUnitID {
			return executionWaveAggregate{}, fmt.Errorf("wave unit_ids must be strictly ascending")
		}
		unit, exists := units[unitID]
		if !exists {
			return executionWaveAggregate{}, fmt.Errorf("wave references unknown execution unit %q", unitID)
		}
		if _, exists := assigned[unitID]; exists {
			return executionWaveAggregate{}, fmt.Errorf("execution unit %q is assigned more than once", unitID)
		}
		if !executionUnitCanRunOnNode(unit, node) {
			return executionWaveAggregate{}, fmt.Errorf("execution unit %q cannot run on node %q", unitID, node.NodeID)
		}
		var err error
		aggregate.cpuMilli, err = checkedPackingAdd(aggregate.cpuMilli, unit.CPUMilli)
		if err != nil {
			return executionWaveAggregate{}, fmt.Errorf("wave cpu sum overflows: %w", err)
		}
		aggregate.memoryBytes, err = checkedPackingAdd(aggregate.memoryBytes, unit.MemoryBytes)
		if err != nil {
			return executionWaveAggregate{}, fmt.Errorf("wave memory sum overflows: %w", err)
		}
		if unit.EstimatedDurationMS > aggregate.durationMS {
			aggregate.durationMS = unit.EstimatedDurationMS
		}
		pendingIDs = append(pendingIDs, unitID)
		previousUnitID = unitID
	}
	aggregate.unitIDs = pendingIDs
	return aggregate, nil
}

// commitExecutionWaveUnits 在整波次局部校验成功后提交已分配标识。
func commitExecutionWaveUnits(assigned map[string]struct{}, unitIDs []string) {
	for _, unitID := range unitIDs {
		assigned[unitID] = struct{}{}
	}
}

// finalizeExecutionWave 校验汇总字段与节点容量并返回关键路径时长。
func finalizeExecutionWave(wave ExecutionWave, node NodeSnapshot, aggregate executionWaveAggregate) (int64, error) {
	if aggregate.cpuMilli != wave.CPUMilli || aggregate.memoryBytes != wave.MemoryBytes || aggregate.durationMS != wave.EstimatedDurationMS {
		return 0, fmt.Errorf("wave fields do not match checked unit aggregates")
	}
	if wave.CPUMilli > node.Available.CPUMilli || wave.MemoryBytes > node.Available.MemoryBytes || wave.Concurrency > node.Available.MaxConcurrency {
		return 0, fmt.Errorf("wave exceeds node %q available capacity", node.NodeID)
	}
	return aggregate.durationMS, nil
}

// validatePackingUnassigned 校验未分配列表覆盖、排序和不可装入节点证明。
func validatePackingUnassigned(
	ids []string,
	units map[string]ExecutionUnit,
	liveNodes map[string]NodeSnapshot,
	assigned map[string]struct{},
) error {
	previousID := ""
	for index, unitID := range ids {
		if index > 0 && unitID <= previousID {
			return fmt.Errorf("unassigned_unit_ids must be strictly ascending")
		}
		unit, exists := units[unitID]
		if !exists {
			return fmt.Errorf("unassigned list references unknown execution unit %q", unitID)
		}
		if _, exists := assigned[unitID]; exists {
			return fmt.Errorf("execution unit %q is assigned more than once", unitID)
		}
		if executionUnitFitsAnyLiveNode(unit, liveNodes) {
			return fmt.Errorf("unassigned execution unit %q fits a live node", unitID)
		}
		assigned[unitID] = struct{}{}
		previousID = unitID
	}
	return nil
}

// executionUnitFitsAnyLiveNode 判断未分配单元是否仍可独立装入任一 live 节点。
func executionUnitFitsAnyLiveNode(unit ExecutionUnit, liveNodes map[string]NodeSnapshot) bool {
	for _, node := range liveNodes {
		if executionUnitCanRunOnNode(unit, node) && node.Available.CPUMilli > 0 && node.Available.MemoryBytes > 0 && node.Available.MaxConcurrency > 0 && unit.CPUMilli <= node.Available.CPUMilli && unit.MemoryBytes <= node.Available.MemoryBytes {
			return true
		}
	}
	return false
}

// checkedPackingAdd 以有符号边界检查执行装箱整数加法。
func checkedPackingAdd(left, right int64) (int64, error) {
	if right > 0 && left > math.MaxInt64-right {
		return 0, fmt.Errorf("integer addition overflow")
	}
	if right < 0 && left < math.MinInt64-right {
		return 0, fmt.Errorf("integer addition underflow")
	}
	return left + right, nil
}
