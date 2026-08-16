package gate

import (
	"fmt"
	"slices"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func rebuildPackingEvidenceUnits(shards []ShardPlan, groups []CompileGroup, context PlanningContext) (map[cicontract.WorkloadResourceTier][]compilePlanningUnit, map[cicontract.WorkloadResourceTier][]ShardPlan, error) {
	workloads, shardByWorkload, err := indexPackingEvidenceShards(shards, context)
	if err != nil {
		return nil, nil, err
	}
	return rebuildPackingEvidenceUnitsFromIndexes(shards, groups, workloads, shardByWorkload, context)
}

func rebuildPackingEvidenceUnitsFromIndexes(shards []ShardPlan, groups []CompileGroup, workloads map[GateID]PlannedWorkload, shardByWorkload map[GateID]ShardPlan, context PlanningContext) (map[cicontract.WorkloadResourceTier][]compilePlanningUnit, map[cicontract.WorkloadResourceTier][]ShardPlan, error) {
	groupByID, err := indexPackingEvidenceGroups(groups)
	if err != nil {
		return nil, nil, err
	}
	units, err := rebuildPackingEvidenceGroupUnits(shards, groupByID, workloads, shardByWorkload, context)
	if err != nil {
		return nil, nil, err
	}
	if err := appendPackingEvidenceOrdinaryUnits(units, workloads, context); err != nil {
		return nil, nil, err
	}
	actual, err := packingEvidenceActualShards(shards, context)
	if err != nil {
		return nil, nil, err
	}
	return units, actual, nil
}

func indexPackingEvidenceGroups(groups []CompileGroup) (map[string]CompileGroup, error) {
	result := make(map[string]CompileGroup, len(groups))
	for _, group := range groups {
		if _, exists := result[group.GroupID]; exists {
			return nil, fmt.Errorf("compile group %q appears more than once", group.GroupID)
		}
		result[group.GroupID] = group
	}
	return result, nil
}

func rebuildPackingEvidenceGroupUnits(shards []ShardPlan, groups map[string]CompileGroup, workloads map[GateID]PlannedWorkload, shardByWorkload map[GateID]ShardPlan, context PlanningContext) (map[cicontract.WorkloadResourceTier][]compilePlanningUnit, error) {
	result := make(map[cicontract.WorkloadResourceTier][]compilePlanningUnit)
	grouped := make(map[GateID]struct{})
	for _, shard := range shards {
		for _, groupID := range shard.CompileGroupIDs {
			group, ok := groups[groupID]
			if !ok {
				return nil, fmt.Errorf("shard %d references unknown compile group %q", shard.Index, groupID)
			}
			unit, tier, err := rebuildPackingEvidenceGroupUnit(group, workloads, shardByWorkload, grouped, context)
			if err != nil {
				return nil, err
			}
			result[tier] = append(result[tier], unit)
		}
	}
	for tier := range result {
		sortCompilePlanningUnits(result[tier])
	}
	return result, nil
}

func appendPackingEvidenceOrdinaryUnits(units map[cicontract.WorkloadResourceTier][]compilePlanningUnit, workloads map[GateID]PlannedWorkload, context PlanningContext) error {
	grouped := make(map[GateID]struct{})
	for _, tierUnits := range units {
		for _, unit := range tierUnits {
			for _, workload := range unit.workloads {
				grouped[GateID(workload.Workload.ID)] = struct{}{}
			}
		}
	}
	for id, workload := range workloads {
		if _, ok := grouped[id]; ok {
			continue
		}
		tier, err := packingEvidenceTier(workload, context)
		if err != nil {
			return err
		}
		reason, err := canonicalCompileUnitHardIsolationReason(workload.Workload.ID)
		if err != nil {
			return fmt.Errorf("rebuild ordinary workload %q hard isolation: %w", workload.Workload.ID, err)
		}
		units[tier] = append(units[tier], compilePlanningUnit{workloads: []PlannedWorkload{workload}, costMS: workload.EstimatedDurationMS, affinityKey: "ordinary:" + workload.Workload.ID, sortID: workload.Workload.ID, tier: tier, resourceClassID: normalCompileResourceClass(tier), hardIsolationReason: reason})
	}
	for tier := range units {
		sortCompilePlanningUnits(units[tier])
	}
	return nil
}

func packingEvidenceActualShards(shards []ShardPlan, context PlanningContext) (map[cicontract.WorkloadResourceTier][]ShardPlan, error) {
	actual := make(map[cicontract.WorkloadResourceTier][]ShardPlan)
	for _, shard := range shards {
		tier, err := packingEvidenceTier(shard.Workloads[0], context)
		if err != nil {
			return nil, err
		}
		actual[tier] = append(actual[tier], shard)
	}
	return actual, nil
}

func indexPackingEvidenceShards(shards []ShardPlan, context PlanningContext) (map[GateID]PlannedWorkload, map[GateID]ShardPlan, error) {
	workloads := make(map[GateID]PlannedWorkload)
	shardByWorkload := make(map[GateID]ShardPlan)
	for _, shard := range shards {
		if len(shard.Workloads) == 0 {
			return nil, nil, fmt.Errorf("shard %d has no workloads", shard.Index)
		}
		tier, err := packingEvidenceTier(shard.Workloads[0], context)
		if err != nil {
			return nil, nil, err
		}
		for _, workload := range shard.Workloads {
			other, err := packingEvidenceTier(workload, context)
			if err != nil || other != tier {
				return nil, nil, fmt.Errorf("shard %d mixes resource tiers", shard.Index)
			}
			id := GateID(workload.Workload.ID)
			if _, exists := workloads[id]; exists {
				return nil, nil, fmt.Errorf("workload %q appears in multiple shards", id)
			}
			workloads[id] = workload
			shardByWorkload[id] = shard
		}
	}
	return workloads, shardByWorkload, nil
}

func rebuildPackingEvidenceGroupUnit(group CompileGroup, workloads map[GateID]PlannedWorkload, shardByWorkload map[GateID]ShardPlan, grouped map[GateID]struct{}, context PlanningContext) (compilePlanningUnit, cicontract.WorkloadResourceTier, error) {
	if len(group.WorkloadIDs) == 0 {
		return compilePlanningUnit{}, 0, fmt.Errorf("compile group %q has no workloads", group.GroupID)
	}
	first, ok := workloads[group.WorkloadIDs[0]]
	if !ok {
		return compilePlanningUnit{}, 0, fmt.Errorf("compile group %q references unknown workload", group.GroupID)
	}
	tier, err := packingEvidenceTier(first, context)
	if err != nil {
		return compilePlanningUnit{}, 0, err
	}
	shard := shardByWorkload[group.WorkloadIDs[0]]
	for _, id := range group.WorkloadIDs {
		if err := validatePackingEvidenceGroupMember(group, id, workloads, shardByWorkload, grouped, tier, shard, context); err != nil {
			return compilePlanningUnit{}, 0, err
		}
	}
	affinity, err := CompileArtifactKey(group)
	if err != nil {
		return compilePlanningUnit{}, 0, err
	}
	memberWorkloads := make([]PlannedWorkload, 0, len(group.WorkloadIDs))
	for _, id := range group.WorkloadIDs {
		memberWorkloads = append(memberWorkloads, workloads[id])
	}
	reason, err := deriveCompileUnitHardIsolationReason(memberWorkloads)
	if err != nil {
		return compilePlanningUnit{}, 0, fmt.Errorf("rebuild compile group %q hard isolation: %w", group.GroupID, err)
	}
	return compilePlanningUnit{workloads: memberWorkloads, group: &group, costMS: compileGroupCriticalDurationMS(group), affinityKey: affinity, sortID: group.GroupID, tier: tier, resourceClassID: group.ResourceClassID, hardIsolationReason: reason}, tier, nil
}

func validatePackingEvidenceGroupMember(group CompileGroup, id GateID, workloads map[GateID]PlannedWorkload, shardByWorkload map[GateID]ShardPlan, grouped map[GateID]struct{}, tier cicontract.WorkloadResourceTier, shard ShardPlan, context PlanningContext) error {
	workload, ok := workloads[id]
	if !ok || shardByWorkload[id].Index != shard.Index {
		return fmt.Errorf("compile group %q coverage is incomplete", group.GroupID)
	}
	if _, exists := grouped[id]; exists {
		return fmt.Errorf("workload %q appears in multiple compile groups", id)
	}
	grouped[id] = struct{}{}
	other, err := packingEvidenceTier(workload, context)
	if err != nil || other != tier {
		return fmt.Errorf("compile group %q mixes resource tiers", group.GroupID)
	}
	return nil
}

func measuredPackingEvidenceMatches(bucket *workloadPackingEvidenceStats, target int64, compileAware bool) error {
	expected, err := packingEvidenceProofLayout(bucket.units, target, compileAware)
	if err != nil {
		return err
	}
	actual := bucket.shards
	if !slices.EqualFunc(expected, actual, func(left, right ShardPlan) bool {
		return left.Index+actual[0].Index == right.Index &&
			left.EstimatedDurationMS == right.EstimatedDurationMS &&
			slices.Equal(left.Workloads, right.Workloads) &&
			slices.Equal(left.CompileGroupIDs, right.CompileGroupIDs)
	}) {
		return fmt.Errorf("measured workload packing evidence does not match canonical plan")
	}
	expectedScore, err := compilePackingScoreForShards(expected, target)
	if err != nil {
		return err
	}
	actualScore, err := compilePackingScoreForShards(actual, target)
	if err != nil {
		return err
	}
	if expectedScore != actualScore {
		return fmt.Errorf("measured workload packing objective does not match canonical plan")
	}
	return nil
}

func packingEvidenceProofLayout(units []compilePlanningUnit, target int64, compileAware bool) ([]ShardPlan, error) {
	if compileAware {
		return provenCompileUnitPacking(units, target)
	}
	planned := make([]PlannedWorkload, 0, len(units))
	for _, unit := range units {
		if unit.group != nil {
			return nil, fmt.Errorf("ordinary packing proof received compile group unit")
		}
		planned = append(planned, unit.workloads...)
	}
	return distributeDCPAPWithHardIsolation(planned, target)
}
