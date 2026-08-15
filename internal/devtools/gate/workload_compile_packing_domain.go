package gate

import (
	"fmt"
	"strings"
)

const (
	compilePackingResourceClassPrefix = "resource-class:"
	compilePackingEligibleGroupMarker = "compile-group-eligible"
)

func compilePlanningUnitResourceClass(unit compilePlanningUnit) string {
	if unit.group != nil {
		return unit.group.ResourceClassID
	}
	return unit.resourceClassID
}

func compilePlanningUnitResourceClassKey(unit compilePlanningUnit) string {
	classID := compilePlanningUnitResourceClass(unit)
	if classID == "" {
		return ""
	}
	return compilePackingResourceClassPrefix + classID
}

func compileGroupCanShareShard(shard ShardPlan, affinities, artifactKeys map[string]struct{}, unit compilePlanningUnit) bool {
	if len(shard.Workloads) > 0 && len(shard.CompileGroupIDs) == 0 {
		return false
	}
	resourceClassKey := compilePlanningUnitResourceClassKey(unit)
	if resourceClassKey == "" || !compilePackingResourceClassMatches(affinities, resourceClassKey) {
		return false
	}
	if !compilePackingGroupDomainMatches(shard, affinities, unit) {
		return false
	}
	artifactKey, err := CompileArtifactKey(*unit.group)
	_, duplicate := artifactKeys[artifactKey]
	return err == nil && !duplicate
}

func compilePackingResourceClassMatches(affinities map[string]struct{}, resourceClassKey string) bool {
	for key := range affinities {
		if strings.HasPrefix(key, compilePackingResourceClassPrefix) && key != resourceClassKey {
			return false
		}
	}
	return true
}

func compilePackingGroupDomainMatches(shard ShardPlan, affinities map[string]struct{}, unit compilePlanningUnit) bool {
	if len(shard.CompileGroupIDs) == 0 {
		return true
	}
	if !CompileGroupSerialPackingEligible(*unit.group) {
		return false
	}
	_, eligible := affinities[compilePackingEligibleGroupMarker]
	return eligible
}

// compileGroupAffinityFromShardIDs 校验 compile group 的 canonical 顺序、重复
// artifact 与显式共箱政策；资源档位一致性由外层 plan validator 负责。
func compileGroupAffinityFromShardIDs(groups map[string]CompileGroup, shard ShardPlan) error {
	seen := make(map[string]string, len(shard.CompileGroupIDs))
	canonical := true
	for index, groupID := range shard.CompileGroupIDs {
		if index > 0 && shard.CompileGroupIDs[index-1] >= groupID {
			canonical = false
		}
		if err := registerCompileGroupShardAffinity(seen, groups, shard.Index, groupID); err != nil {
			return err
		}
	}
	if err := validateCompileGroupShardClasses(groups, shard); err != nil {
		return err
	}
	if !canonical {
		return fmt.Errorf("shard %d compile groups are not canonical sorted", shard.Index)
	}
	if !compileGroupsCoverShardWorkloads(groups, shard) {
		extra, missing := compileGroupShardCoverageMismatch(groups, shard)
		return fmt.Errorf("shard %d mixes grouped and ordinary workloads: extra=%v missing=%v", shard.Index, extra, missing)
	}
	if err := validateCompileGroupSerialEligibility(groups, shard); err != nil {
		return err
	}
	return nil
}

func validateCompileGroupSerialEligibility(groups map[string]CompileGroup, shard ShardPlan) error {
	if len(shard.CompileGroupIDs) <= 1 {
		return nil
	}
	for _, groupID := range shard.CompileGroupIDs {
		if !CompileGroupSerialPackingEligible(groups[groupID]) {
			return fmt.Errorf("shard %d compile group %q is not eligible for serial packing", shard.Index, groupID)
		}
	}
	return nil
}

func validateCompileGroupShardClasses(groups map[string]CompileGroup, shard ShardPlan) error {
	resourceClassID := ""
	for _, groupID := range shard.CompileGroupIDs {
		group := groups[groupID]
		if err := validateCompileGroupResourceClass(group.ResourceClassID); err != nil {
			return fmt.Errorf("shard %d compile group %q resource class: %w", shard.Index, groupID, err)
		}
		if resourceClassID != "" && resourceClassID != group.ResourceClassID {
			return fmt.Errorf("shard %d compile groups mix resource classes %q and %q", shard.Index, resourceClassID, group.ResourceClassID)
		}
		resourceClassID = group.ResourceClassID
	}
	return nil
}

// compileGroupsCoverShardWorkloads 拒绝把 ordinary workload 塞入任一 compile-group shard。
func compileGroupsCoverShardWorkloads(groups map[string]CompileGroup, shard ShardPlan) bool {
	if len(shard.Workloads) == 0 || len(shard.CompileGroupIDs) == 0 {
		return true
	}
	covered := make(map[string]struct{})
	for _, groupID := range shard.CompileGroupIDs {
		for _, workloadID := range groups[groupID].WorkloadIDs {
			covered[string(workloadID)] = struct{}{}
		}
	}
	if len(covered) != len(shard.Workloads) {
		return false
	}
	for _, workload := range shard.Workloads {
		if _, ok := covered[workload.Workload.ID]; !ok {
			return false
		}
	}
	return true
}

// registerCompileGroupShardAffinity 记录 group 与 artifact，并拒绝重复引用。
func registerCompileGroupShardAffinity(seen map[string]string, groups map[string]CompileGroup, shardIndex int, groupID string) error {
	if _, duplicate := seen[groupID]; duplicate {
		return fmt.Errorf("shard %d repeats compile group %q", shardIndex, groupID)
	}
	group, ok := groups[groupID]
	if !ok {
		return fmt.Errorf("shard %d references unknown compile group %q", shardIndex, groupID)
	}
	artifactKey, err := CompileArtifactKey(group)
	if err != nil {
		return err
	}
	for otherID, otherKey := range seen {
		if otherID != groupID && otherKey == artifactKey {
			return fmt.Errorf("shard %d contains multiple groups for compile artifact %q", shardIndex, artifactKey)
		}
	}
	seen[groupID] = artifactKey
	return nil
}

func compileGroupIDsEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
