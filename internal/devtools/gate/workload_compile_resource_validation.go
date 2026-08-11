package gate

import "fmt"

// validateStoredCompileGroupResources 将 group class 与 shard 中冻结资源绑定，
// 并拒绝同一 shard 混用 normal tier 或 calibration tuple。
func validateStoredCompileGroupResources(plan WorkloadExecutionPlan, groups map[string]CompileGroup) error {
	planned := make(map[string]PlannedWorkload)
	for _, shard := range plan.Shards {
		for _, item := range shard.Workloads {
			if _, duplicate := planned[item.Workload.ID]; duplicate {
				return fmt.Errorf("workload %q appears in multiple stored resource bindings", item.Workload.ID)
			}
			planned[item.Workload.ID] = item
		}
	}
	for groupID, group := range groups {
		if err := validateStoredCompileGroupResourceBinding(plan, groupID, group, planned); err != nil {
			return err
		}
	}
	for _, shard := range plan.Shards {
		if err := validateStoredShardResourceUniformity(plan.Context, shard, groups, planned); err != nil {
			return err
		}
	}
	return nil
}

func validateStoredCompileGroupResourceBinding(plan WorkloadExecutionPlan, groupID string, group CompileGroup, planned map[string]PlannedWorkload) error {
	for _, workloadID := range group.WorkloadIDs {
		item, ok := planned[string(workloadID)]
		if !ok {
			return fmt.Errorf("compile group %q workload %q is missing from stored shard resources", groupID, workloadID)
		}
		classID, err := storedWorkloadResourceClass(plan.Context, item)
		if err != nil {
			return fmt.Errorf("compile group %q workload %q resource: %w", groupID, workloadID, err)
		}
		if group.ResourceClassID != classID {
			return fmt.Errorf("compile group %q resource class %q does not match workload %q class %q", groupID, group.ResourceClassID, workloadID, classID)
		}
	}
	return nil
}

func storedWorkloadResourceClass(context PlanningContext, item PlannedWorkload) (string, error) {
	if context.Calibration {
		if item.ResourceCPU != context.CalibrationResourceCPU || item.ResourceMemoryGiB != context.CalibrationResourceMemoryGiB {
			return "", fmt.Errorf("workload %q does not match calibration resource tuple", item.Workload.ID)
		}
		return context.CalibrationResourceClassID, nil
	}
	tier, err := plannedWorkloadResourceTier(item)
	if err != nil {
		return "", err
	}
	return checkedNormalCompileResourceClass(tier)
}

// validateStoredShardResourceUniformity 拒绝已存分片混用不同 workload 或 compile-group 资源档。
func validateStoredShardResourceUniformity(context PlanningContext, shard ShardPlan, groups map[string]CompileGroup, planned map[string]PlannedWorkload) error {
	tupleKey := ""
	for _, item := range shard.Workloads {
		if _, err := storedWorkloadResourceClass(context, item); err != nil {
			return fmt.Errorf("workload shard %d resource: %w", shard.Index, err)
		}
		key := fmt.Sprintf("%.17g|%.17g", item.ResourceCPU, item.ResourceMemoryGiB)
		if tupleKey != "" && tupleKey != key {
			return fmt.Errorf("workload shard %d mixes workload resource tuples", shard.Index)
		}
		tupleKey = key
	}
	for _, groupID := range shard.CompileGroupIDs {
		group := groups[groupID]
		if len(group.WorkloadIDs) == 0 {
			continue
		}
		item := planned[string(group.WorkloadIDs[0])]
		key := fmt.Sprintf("%.17g|%.17g", item.ResourceCPU, item.ResourceMemoryGiB)
		if tupleKey != key {
			return fmt.Errorf("workload shard %d mixes compile group resource tuples", shard.Index)
		}
	}
	return nil
}
