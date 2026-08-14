package gate

import (
	"errors"
	"fmt"
	"slices"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// workloadPackingEvidenceStats 汇总单一资源档的 packing 证明输入。
type workloadPackingEvidenceStats struct {
	planned, packable, isolated, serialDomains int
	ordinaryTotal                              int64
	ordinaryOversize                           int
}

// deriveWorkloadPackingEvidence 从 canonical shards/compile groups 重算可审计分档证据。
func deriveWorkloadPackingEvidence(shards []ShardPlan, groups []CompileGroup, context PlanningContext) ([]WorkloadPackingEvidence, error) {
	if len(shards) == 0 {
		return nil, errors.New("packing evidence requires at least one shard")
	}
	target, err := context.EffectiveTargetDurationMS()
	if err != nil {
		return nil, err
	}
	stats, workloads, err := collectPackingEvidenceShards(shards, context)
	if err != nil {
		return nil, err
	}
	covered := make(map[GateID]struct{})
	if err := collectPackingEvidenceGroups(stats, workloads, covered, groups, context); err != nil {
		return nil, err
	}
	if err := collectPackingEvidenceOrdinary(stats, workloads, covered, target, context); err != nil {
		return nil, err
	}
	return finalizePackingEvidence(stats, target)
}

// compileGroupShardCoverageMismatch 返回 shard 相对其 compile groups 的确定性覆盖差集。
func compileGroupShardCoverageMismatch(groups map[string]CompileGroup, shard ShardPlan) ([]string, []string) {
	covered, actual := make(map[string]struct{}), make(map[string]struct{})
	for _, groupID := range shard.CompileGroupIDs {
		for _, workloadID := range groups[groupID].WorkloadIDs {
			covered[string(workloadID)] = struct{}{}
		}
	}
	for _, workload := range shard.Workloads {
		actual[workload.Workload.ID] = struct{}{}
	}
	extra, missing := make([]string, 0), make([]string, 0)
	for workloadID := range actual {
		if _, ok := covered[workloadID]; !ok {
			extra = append(extra, workloadID)
		}
	}
	for workloadID := range covered {
		if _, ok := actual[workloadID]; !ok {
			missing = append(missing, workloadID)
		}
	}
	slices.Sort(extra)
	slices.Sort(missing)
	return extra, missing
}

// collectPackingEvidenceShards 建立 shard 实际覆盖和资源档计数。
func collectPackingEvidenceShards(shards []ShardPlan, context PlanningContext) (map[cicontract.WorkloadResourceTier]*workloadPackingEvidenceStats, map[GateID]PlannedWorkload, error) {
	stats := make(map[cicontract.WorkloadResourceTier]*workloadPackingEvidenceStats)
	workloads := make(map[GateID]PlannedWorkload)
	for _, shard := range shards {
		tier, entries, err := collectPackingEvidenceShard(shard, context)
		if err != nil {
			return nil, nil, err
		}
		packingEvidenceStatsFor(stats, tier).planned++
		for id, workload := range entries {
			if _, exists := workloads[id]; exists {
				return nil, nil, fmt.Errorf("workload %q appears in multiple shards", id)
			}
			workloads[id] = workload
		}
	}
	return stats, workloads, nil
}

// collectPackingEvidenceShard 校验单个 shard 的资源档一致性并返回 workload 覆盖。
func collectPackingEvidenceShard(shard ShardPlan, context PlanningContext) (cicontract.WorkloadResourceTier, map[GateID]PlannedWorkload, error) {
	if len(shard.Workloads) == 0 {
		return 0, nil, errors.New("packing evidence cannot classify an empty shard")
	}
	tier, err := packingEvidenceTier(shard.Workloads[0], context)
	if err != nil {
		return 0, nil, err
	}
	entries := make(map[GateID]PlannedWorkload, len(shard.Workloads))
	for _, workload := range shard.Workloads {
		other, err := packingEvidenceTier(workload, context)
		if err != nil {
			return 0, nil, err
		}
		if other != tier {
			return 0, nil, fmt.Errorf("shard %d mixes resource tiers", shard.Index)
		}
		entries[GateID(workload.Workload.ID)] = workload
	}
	return tier, entries, nil
}

// collectPackingEvidenceGroups 将每个 compile group 的独立 domain 计入证据。
func collectPackingEvidenceGroups(stats map[cicontract.WorkloadResourceTier]*workloadPackingEvidenceStats, workloads map[GateID]PlannedWorkload, covered map[GateID]struct{}, groups []CompileGroup, context PlanningContext) error {
	for _, group := range groups {
		tier, err := collectPackingEvidenceGroup(group, workloads, covered, context)
		if err != nil {
			return err
		}
		bucket := packingEvidenceStatsFor(stats, tier)
		if CompileGroupSerialPackingEligible(group) {
			bucket.packable++
			bucket.serialDomains++
		} else {
			bucket.isolated++
		}
	}
	return nil
}

// collectPackingEvidenceGroup 校验 compile group 成员覆盖及资源档一致性。
func collectPackingEvidenceGroup(group CompileGroup, workloads map[GateID]PlannedWorkload, covered map[GateID]struct{}, context PlanningContext) (cicontract.WorkloadResourceTier, error) {
	if len(group.WorkloadIDs) == 0 {
		return 0, fmt.Errorf("compile group %q has no workloads", group.GroupID)
	}
	first, ok := workloads[group.WorkloadIDs[0]]
	if !ok {
		return 0, fmt.Errorf("compile group %q references unknown workload", group.GroupID)
	}
	tier, err := packingEvidenceTier(first, context)
	if err != nil {
		return 0, err
	}
	for _, id := range group.WorkloadIDs {
		workload, ok := workloads[id]
		if !ok {
			return 0, fmt.Errorf("compile group %q references unknown workload %q", group.GroupID, id)
		}
		other, err := packingEvidenceTier(workload, context)
		if err != nil || other != tier {
			return 0, fmt.Errorf("compile group %q mixes resource tiers", group.GroupID)
		}
		if _, exists := covered[id]; exists {
			return 0, fmt.Errorf("workload %q appears in multiple compile groups", id)
		}
		covered[id] = struct{}{}
	}
	return tier, nil
}

// collectPackingEvidenceOrdinary 将未归入 compile group 的 workload 计为 ordinary packable unit。
func collectPackingEvidenceOrdinary(stats map[cicontract.WorkloadResourceTier]*workloadPackingEvidenceStats, workloads map[GateID]PlannedWorkload, covered map[GateID]struct{}, target int64, context PlanningContext) error {
	for id, workload := range workloads {
		if _, grouped := covered[id]; grouped {
			continue
		}
		tier, err := packingEvidenceTier(workload, context)
		if err != nil {
			return err
		}
		bucket := packingEvidenceStatsFor(stats, tier)
		bucket.packable++
		if err := addOrdinaryDuration(bucket, workload.EstimatedDurationMS, target); err != nil {
			return err
		}
	}
	return nil
}

func packingEvidenceStatsFor(stats map[cicontract.WorkloadResourceTier]*workloadPackingEvidenceStats, tier cicontract.WorkloadResourceTier) *workloadPackingEvidenceStats {
	if stats[tier] == nil {
		stats[tier] = &workloadPackingEvidenceStats{}
	}
	return stats[tier]
}

func addOrdinaryDuration(bucket *workloadPackingEvidenceStats, duration, target int64) error {
	if duration > target {
		bucket.ordinaryOversize++
		return nil
	}
	var err error
	bucket.ordinaryTotal, err = addCompilePackingDuration(bucket.ordinaryTotal, duration)
	return err
}

// finalizePackingEvidence 计算 sound lower bound、gap 和实际 solver mode。
func finalizePackingEvidence(stats map[cicontract.WorkloadResourceTier]*workloadPackingEvidenceStats, target int64) ([]WorkloadPackingEvidence, error) {
	tiers := make([]cicontract.WorkloadResourceTier, 0, len(stats))
	for tier := range stats {
		tiers = append(tiers, tier)
	}
	slices.Sort(tiers)
	evidence := make([]WorkloadPackingEvidence, 0, len(tiers))
	for _, tier := range tiers {
		bucket := stats[tier]
		lower, err := packingEvidenceLowerBound(bucket, target)
		if err != nil {
			return nil, err
		}
		if bucket.planned < lower {
			return nil, fmt.Errorf("tier %d planned shards below sound lower bound", tier)
		}
		mode, gap := packingEvidenceMode(bucket, lower)
		if bucket.packable <= cicontract.WorkloadPlanningExactPackableUnitThreshold {
			lower, gap = bucket.planned, 0
		}
		if gap < 0 {
			return nil, fmt.Errorf("tier %d planned shards below lower bound", tier)
		}
		evidence = append(evidence, WorkloadPackingEvidence{ResourceTier: tier, SolverMode: mode, PackableUnitCount: bucket.packable, IsolatedUnitCount: bucket.isolated, LowerBoundShards: lower, PlannedShards: bucket.planned, HeuristicGapShards: gap})
	}
	return evidence, nil
}

func packingEvidenceLowerBound(bucket *workloadPackingEvidenceStats, target int64) (int, error) {
	ordinary, err := compilePackingCapacityLowerBound(bucket.ordinaryTotal, bucket.ordinaryOversize, target)
	if err != nil {
		return 0, err
	}
	total, err := addCompilePackingShardCounts(ordinary, bucket.serialDomains)
	if err != nil {
		return 0, err
	}
	return addCompilePackingShardCounts(bucket.isolated, total)
}

func packingEvidenceMode(bucket *workloadPackingEvidenceStats, lower int) (string, int) {
	if bucket.packable == 0 {
		return cicontract.WorkloadPlanningIsolatedSolverModeID, 0
	}
	if bucket.packable <= cicontract.WorkloadPlanningExactPackableUnitThreshold {
		return cicontract.WorkloadPlanningExactSolverModeID, 0
	}
	return cicontract.WorkloadPlanningHeuristicSolverModeID, bucket.planned - lower
}

func packingEvidenceTier(workload PlannedWorkload, context PlanningContext) (cicontract.WorkloadResourceTier, error) {
	if context.Calibration {
		return cicontract.WorkloadResourceTierMedium, nil
	}
	return plannedWorkloadResourceTier(workload)
}
