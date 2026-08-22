package heteroci

import (
	"fmt"
	"math"
	"math/big"
	"sort"
	"time"
)

// anchorNodeState 保存单个节点的确定性波次规划状态。
type anchorNodeState struct {
	snapshot NodeSnapshot
	waves    []ExecutionWave
	makespan int64
	assigned int
}

// anchorCandidate 是一次装箱候选及其完整的确定性比较键。
type anchorCandidate struct {
	nodeIndex           int
	waveIndex           int
	existing            bool
	projectedGlobal     int64
	projectedNode       int64
	projectedAssigned   int
	assignedRatio       *big.Rat
	cpuSlack            int64
	memorySlack         int64
	projectedWaveCPU    int64
	projectedWaveMemory int64
	projectedWaveCount  int
	projectedWaveMS     int64
}

// BuildAnchorWavePlan 按实时节点容量为工作单元构造确定性的顺序波次规划。
func BuildAnchorWavePlan(manifest Manifest, snapshot SnapshotSet, units []ExecutionUnit, now time.Time, heartbeatTTL time.Duration) (PackingPlan, error) {
	if err := manifest.Validate(); err != nil {
		return PackingPlan{}, fmt.Errorf("validate anchor wave manifest: %w", err)
	}
	if err := snapshot.ValidateAgainst(manifest, now, heartbeatTTL); err != nil {
		return PackingPlan{}, fmt.Errorf("validate anchor wave snapshot: %w", err)
	}
	if err := ValidateExecutionUnits(units); err != nil {
		return PackingPlan{}, fmt.Errorf("validate anchor wave units: %w", err)
	}

	states := newAnchorNodeStates(snapshot.Nodes)
	orderedUnits := append([]ExecutionUnit(nil), units...)
	sort.Slice(orderedUnits, func(i, j int) bool {
		return anchorUnitLess(orderedUnits[i], orderedUnits[j])
	})
	globalMakespan := int64(0)
	blockers := make([]CapacityBlocker, 0)
	for _, unit := range orderedUnits {
		candidate, found, err := chooseAnchorCandidate(states, unit, globalMakespan)
		if err != nil {
			return PackingPlan{}, fmt.Errorf("choose anchor wave for unit %q: %w", unit.UnitID, err)
		}
		if !found {
			blockers = append(blockers, *newCapacityBlocker(unit, routingBlockerCapacity))
			continue
		}
		if err := applyAnchorCandidate(states, candidate, unit); err != nil {
			return PackingPlan{}, fmt.Errorf("apply anchor wave for unit %q: %w", unit.UnitID, err)
		}
		globalMakespan = candidate.projectedGlobal
	}
	if len(blockers) != 0 {
		sort.Slice(blockers, func(left, right int) bool { return capacityBlockerLess(blockers[left], blockers[right]) })
		return PackingPlan{}, &CapacityBlockerError{Blockers: blockers}
	}

	plan := anchorPlanFromStates(snapshot, states, nil)
	if err := plan.ValidateAgainst(manifest, snapshot, units, now, heartbeatTTL); err != nil {
		return PackingPlan{}, fmt.Errorf("validate anchor wave plan: %w", err)
	}
	return plan, nil
}

// newAnchorNodeStates 复制节点观测并按稳定 ID 排序。
func newAnchorNodeStates(nodes []NodeSnapshot) []anchorNodeState {
	ordered := append([]NodeSnapshot(nil), nodes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].NodeID < ordered[j].NodeID })
	states := make([]anchorNodeState, len(ordered))
	for index, node := range ordered {
		states[index] = anchorNodeState{snapshot: node, waves: make([]ExecutionWave, 0)}
	}
	return states
}

// anchorUnitLess 实现工作单元的冻结排序，避免依赖输入顺序。
func anchorUnitLess(left, right ExecutionUnit) bool {
	if left.EstimatedDurationMS != right.EstimatedDurationMS {
		return left.EstimatedDurationMS > right.EstimatedDurationMS
	}
	if left.CPUMilli != right.CPUMilli {
		return left.CPUMilli > right.CPUMilli
	}
	if left.MemoryBytes != right.MemoryBytes {
		return left.MemoryBytes > right.MemoryBytes
	}
	return left.UnitID < right.UnitID
}

// chooseAnchorCandidate 枚举节点和既有/新波次并选择最小确定性比较键。
func chooseAnchorCandidate(states []anchorNodeState, unit ExecutionUnit, globalMakespan int64) (anchorCandidate, bool, error) {
	var best anchorCandidate
	found := false
	overflowed := false
	for nodeIndex := range states {
		candidates, nodeOverflowed := collectAnchorNodeCandidates(states[nodeIndex], nodeIndex, unit, globalMakespan)
		overflowed = overflowed || nodeOverflowed
		for _, candidate := range candidates {
			if !found || anchorCandidateLess(candidate, best, states) {
				best, found = candidate, true
			}
		}
	}
	if !found && overflowed {
		return anchorCandidate{}, false, fmt.Errorf("anchor wave candidate arithmetic overflows")
	}
	return best, found, nil
}

// collectAnchorNodeCandidates 为一个可执行节点生成既有及新波次候选。
func collectAnchorNodeCandidates(state anchorNodeState, nodeIndex int, unit ExecutionUnit, globalMakespan int64) ([]anchorCandidate, bool) {
	if !executionUnitCanRunOnNode(unit, state.snapshot) || !anchorUnitFitsNode(unit, state.snapshot.Available) {
		return nil, false
	}
	candidates := make([]anchorCandidate, 0, len(state.waves)+1)
	overflowed := false
	for waveIndex := range state.waves {
		candidate, ok, candidateOverflowed := existingAnchorCandidate(state, nodeIndex, waveIndex, unit, globalMakespan)
		overflowed = overflowed || candidateOverflowed
		if ok {
			candidates = append(candidates, candidate)
		}
	}
	candidate, ok, candidateOverflowed := newAnchorWaveCandidate(state, nodeIndex, unit, globalMakespan)
	overflowed = overflowed || candidateOverflowed
	if ok {
		candidates = append(candidates, candidate)
	}
	return candidates, overflowed
}

// existingAnchorCandidate 构造加入既有波次的候选，算术溢出时跳过该候选。
func existingAnchorCandidate(state anchorNodeState, nodeIndex, waveIndex int, unit ExecutionUnit, globalMakespan int64) (anchorCandidate, bool, bool) {
	wave := state.waves[waveIndex]
	if !anchorWaveFits(wave, unit, state.snapshot.Available) {
		return anchorCandidate{}, false, false
	}
	waveMS := max(wave.EstimatedDurationMS, unit.EstimatedDurationMS)
	projectedNode, err := checkedAnchorAdd(state.makespan, waveMS-wave.EstimatedDurationMS)
	if err != nil {
		return anchorCandidate{}, false, true
	}
	projectedWaveCPU, err := checkedAnchorAdd(wave.CPUMilli, unit.CPUMilli)
	if err != nil {
		return anchorCandidate{}, false, true
	}
	projectedWaveMemory, err := checkedAnchorAdd(wave.MemoryBytes, unit.MemoryBytes)
	if err != nil {
		return anchorCandidate{}, false, true
	}
	projectedWaveCount, err := checkedAnchorCount(wave.Concurrency)
	if err != nil {
		return anchorCandidate{}, false, true
	}
	candidate, err := newAnchorCandidate(nodeIndex, waveIndex, true, state, state.snapshot.Available, globalMakespan, projectedNode, waveMS, projectedWaveCPU, projectedWaveMemory, projectedWaveCount)
	if err != nil {
		return anchorCandidate{}, false, true
	}
	return candidate, true, false
}

// newAnchorWaveCandidate 构造新建波次的候选，算术溢出时跳过该候选。
func newAnchorWaveCandidate(state anchorNodeState, nodeIndex int, unit ExecutionUnit, globalMakespan int64) (anchorCandidate, bool, bool) {
	projectedNode, err := checkedAnchorAdd(state.makespan, unit.EstimatedDurationMS)
	if err != nil {
		return anchorCandidate{}, false, true
	}
	candidate, err := newAnchorCandidate(nodeIndex, len(state.waves), false, state, state.snapshot.Available, globalMakespan, projectedNode, unit.EstimatedDurationMS, unit.CPUMilli, unit.MemoryBytes, 1)
	if err != nil {
		return anchorCandidate{}, false, true
	}
	return candidate, true, false
}

// newAnchorCandidate 计算一个波次放置的完整比较元组。
func newAnchorCandidate(nodeIndex, waveIndex int, existing bool, state anchorNodeState, available ResourceCapacity, globalMakespan, projectedNode, waveMS, waveCPU, waveMemory int64, waveCount int) (anchorCandidate, error) {
	projectedAssigned, err := checkedAnchorCount(state.assigned)
	if err != nil {
		return anchorCandidate{}, err
	}
	projectedGlobal := max(globalMakespan, projectedNode)
	return anchorCandidate{
		nodeIndex: nodeIndex, waveIndex: waveIndex, existing: existing,
		projectedGlobal: projectedGlobal, projectedNode: projectedNode,
		projectedAssigned: projectedAssigned,
		assignedRatio:     new(big.Rat).SetFrac64(int64(projectedAssigned), int64(available.MaxConcurrency)),
		cpuSlack:          available.CPUMilli - waveCPU, memorySlack: available.MemoryBytes - waveMemory,
		projectedWaveCPU: waveCPU, projectedWaveMemory: waveMemory,
		projectedWaveCount: waveCount, projectedWaveMS: waveMS,
	}, nil
}

// anchorCandidateLess 比较冻结 tuple：makespan、负载比例、资源 slack 与布局。
func anchorCandidateLess(left, right anchorCandidate, states []anchorNodeState) bool {
	if left.projectedGlobal != right.projectedGlobal {
		return left.projectedGlobal < right.projectedGlobal
	}
	if left.projectedNode != right.projectedNode {
		return left.projectedNode < right.projectedNode
	}
	if ratio := left.assignedRatio.Cmp(right.assignedRatio); ratio != 0 {
		return ratio < 0
	}
	if left.existing != right.existing {
		return left.existing
	}
	if left.cpuSlack != right.cpuSlack {
		return left.cpuSlack < right.cpuSlack
	}
	if left.memorySlack != right.memorySlack {
		return left.memorySlack < right.memorySlack
	}
	leftNodeID, rightNodeID := states[left.nodeIndex].snapshot.NodeID, states[right.nodeIndex].snapshot.NodeID
	if leftNodeID != rightNodeID {
		return leftNodeID < rightNodeID
	}
	return left.waveIndex < right.waveIndex
}

// anchorUnitFitsNode 检查单元在节点可用容量中是否有独立落点。
func anchorUnitFitsNode(unit ExecutionUnit, available ResourceCapacity) bool {
	return available.CPUMilli > 0 && available.MemoryBytes > 0 && available.MaxConcurrency > 0 &&
		unit.CPUMilli <= available.CPUMilli && unit.MemoryBytes <= available.MemoryBytes
}

// anchorWaveFits 检查将单元加入既有波次时的 CPU、内存和计数预算。
func anchorWaveFits(wave ExecutionWave, unit ExecutionUnit, available ResourceCapacity) bool {
	return wave.CPUMilli <= available.CPUMilli-unit.CPUMilli &&
		wave.MemoryBytes <= available.MemoryBytes-unit.MemoryBytes &&
		wave.Concurrency < available.MaxConcurrency
}

// applyAnchorCandidate 将候选单元写入节点波次并保持资源字段精确。
func applyAnchorCandidate(states []anchorNodeState, candidate anchorCandidate, unit ExecutionUnit) error {
	state := &states[candidate.nodeIndex]
	if candidate.existing {
		wave := &state.waves[candidate.waveIndex]
		wave.CPUMilli = candidate.projectedWaveCPU
		wave.MemoryBytes = candidate.projectedWaveMemory
		wave.Concurrency = candidate.projectedWaveCount
		wave.EstimatedDurationMS = candidate.projectedWaveMS
		wave.UnitIDs = append(wave.UnitIDs, unit.UnitID)
	} else {
		state.waves = append(state.waves, ExecutionWave{
			Wave: candidate.waveIndex, UnitIDs: []string{unit.UnitID},
			CPUMilli: unit.CPUMilli, MemoryBytes: unit.MemoryBytes,
			Concurrency: 1, EstimatedDurationMS: unit.EstimatedDurationMS,
		})
	}
	state.makespan = candidate.projectedNode
	state.assigned = candidate.projectedAssigned
	return nil
}

// anchorPlanFromStates 生成固定 schema 的 plan，并显式初始化所有 slice。
func anchorPlanFromStates(snapshot SnapshotSet, states []anchorNodeState, unassigned []string) PackingPlan {
	plan := PackingPlan{
		SchemaVersion: PackingPlanSchemaVersion, Authority: AuthorityLocalNonAuthoritative,
		SnapshotCapturedAtUnixMilli: snapshot.CapturedAtUnixMilli,
		NodeShards:                  make([]NodeShard, 0, len(states)), UnassignedUnitIDs: make([]string, len(unassigned)),
	}
	copy(plan.UnassignedUnitIDs, unassigned)
	sort.Strings(plan.UnassignedUnitIDs)
	for _, state := range states {
		if len(state.waves) == 0 {
			continue
		}
		waves := make([]ExecutionWave, len(state.waves))
		copy(waves, state.waves)
		for index := range waves {
			waves[index].Wave = index
			waves[index].UnitIDs = append([]string(nil), waves[index].UnitIDs...)
			sort.Strings(waves[index].UnitIDs)
		}
		plan.NodeShards = append(plan.NodeShards, NodeShard{NodeID: state.snapshot.NodeID, Waves: waves, EstimatedMakespanMS: state.makespan})
	}
	return plan
}

// checkedAnchorAdd 对正数毫秒或资源值执行不会回绕的 int64 加法。
func checkedAnchorAdd(left, right int64) (int64, error) {
	if left < 0 || right < 0 || right > math.MaxInt64-left {
		return 0, fmt.Errorf("anchor wave duration addition overflows int64")
	}
	return left + right, nil
}

// checkedAnchorCount 对非负计数执行不会回绕的自增。
func checkedAnchorCount(value int) (int, error) {
	if value < 0 || value == math.MaxInt {
		return 0, fmt.Errorf("anchor wave assignment count overflows int")
	}
	return value + 1, nil
}
