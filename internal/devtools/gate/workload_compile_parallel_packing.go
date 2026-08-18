package gate

import (
	"errors"
	"math"
	"slices"
)

func partitionParallelCompileUnits(units []compilePlanningUnit) ([]compilePlanningUnit, []compilePlanningUnit) {
	parallel := make([]compilePlanningUnit, 0)
	conventional := make([]compilePlanningUnit, 0, len(units))
	for _, unit := range units {
		if unit.group != nil && CompileGroupParallelPackingEligible(*unit.group) && !unit.hardIsolationReason.hardIsolated() {
			parallel = append(parallel, unit)
		} else {
			conventional = append(conventional, unit)
		}
	}
	return parallel, conventional
}

// packParallelCompileUnits 使用历史/明确 bootstrap CPU 与 RSS 做确定性 BFD。
// 同一 shard 的组处于一个并发 wave，wall 成本取最大值而非求和。
func packParallelCompileUnits(units []compilePlanningUnit, target int64) ([]ShardPlan, error) {
	if len(units) == 0 {
		return nil, nil
	}
	ordered := append([]compilePlanningUnit(nil), units...)
	sortCompilePlanningUnits(ordered)
	shards := make([]ShardPlan, 0)
	for _, unit := range ordered {
		if err := validateParallelCompileUnit(unit); err != nil {
			return nil, err
		}
		shards = placeParallelCompileUnit(shards, unit)
	}
	return finalizeParallelCompileShards(shards, target)
}

func placeParallelCompileUnit(shards []ShardPlan, unit compilePlanningUnit) []ShardPlan {
	for index := range shards {
		if parallelCompileUnitFits(shards[index], unit) {
			applyParallelCompileUnit(&shards[index], unit)
			return shards
		}
	}
	shard := ShardPlan{}
	applyParallelCompileUnit(&shard, unit)
	return append(shards, shard)
}

func finalizeParallelCompileShards(shards []ShardPlan, target int64) ([]ShardPlan, error) {
	for index := range shards {
		shards[index].Index = index
		slices.Sort(shards[index].CompileGroupIDs)
		if shards[index].EstimatedDurationMS <= 0 || (shards[index].EstimatedDurationMS > target && len(shards[index].CompileGroupIDs) == 0) {
			return nil, errors.New("parallel compile packing produced invalid duration")
		}
	}
	return shards, nil
}

// validateParallelCompileUnit 校验并发组拥有完整 workload、成本和资源画像。
func validateParallelCompileUnit(unit compilePlanningUnit) error {
	if unit.group == nil || !CompileGroupParallelPackingEligible(*unit.group) || len(unit.workloads) == 0 {
		return errors.New("parallel compile packing received an ineligible unit")
	}
	if unit.estimatedCPUMilliCores <= 0 || unit.estimatedPeakRSSBytes <= 0 || unit.costMS <= 0 {
		return errors.New("parallel compile packing unit has incomplete resource estimate")
	}
	return nil
}

func parallelCompileUnitFits(shard ShardPlan, unit compilePlanningUnit) bool {
	if len(shard.Workloads) != 0 && (shard.Workloads[0].ResourceCPU != unit.workloads[0].ResourceCPU || shard.Workloads[0].ResourceMemoryGiB != unit.workloads[0].ResourceMemoryGiB) {
		return false
	}
	capacityCPU := int64(math.Ceil(unit.workloads[0].ResourceCPU * 1000))
	capacityRSS := int64(math.Ceil(unit.workloads[0].ResourceMemoryGiB * float64(1<<30)))
	return shard.EstimatedCPUMilliCores <= capacityCPU-unit.estimatedCPUMilliCores &&
		shard.EstimatedPeakRSSBytes <= capacityRSS-unit.estimatedPeakRSSBytes
}

func applyParallelCompileUnit(shard *ShardPlan, unit compilePlanningUnit) {
	shard.Workloads = append(shard.Workloads, unit.workloads...)
	shard.CompileGroupIDs = append(shard.CompileGroupIDs, unit.group.GroupID)
	shard.EstimatedDurationMS = maxInt64Value(shard.EstimatedDurationMS, unit.costMS)
	shard.EstimatedCPUMilliCores += unit.estimatedCPUMilliCores
	shard.EstimatedPeakRSSBytes += unit.estimatedPeakRSSBytes
}

// markParallelCompileGroups 把同一资源 wave 中至少两个 group 标为 worker 并发执行。
func markParallelCompileGroups(groups []CompileGroup, shards []ShardPlan) {
	byID := make(map[string]*CompileGroup, len(groups))
	for index := range groups {
		byID[groups[index].GroupID] = &groups[index]
	}
	for _, shard := range shards {
		parallelCount := 0
		for _, groupID := range shard.CompileGroupIDs {
			if group := byID[groupID]; group != nil && CompileGroupParallelPackingEligible(*group) {
				parallelCount++
			}
		}
		if parallelCount < 2 {
			continue
		}
		for _, groupID := range shard.CompileGroupIDs {
			if group := byID[groupID]; group != nil && CompileGroupParallelPackingEligible(*group) {
				group.ConcurrentExecution = true
			}
		}
	}
}
