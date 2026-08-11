package gate

import (
	"errors"
	"fmt"
	"slices"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// planWorkloadShards 依据 compile-aware 开关选择唯一的 workload 分片规划入口。
func planWorkloadShards(executionCatalog WorkloadCatalog, index DurationSampleIndex, compileInputs map[GateID]CompileGroupInput, compileAware bool) ([]ShardPlan, []CompileGroup, error) {
	if compileAware {
		return planLPTWithCompileInputs(executionCatalog, index, compileInputs)
	}
	shards, err := planLPTWithIndex(executionCatalog, index)
	return shards, nil, err
}

// finalizeWorkloadExecutionPlan 组装摘要、证据、digest 并执行生成计划的完整契约校验。
func finalizeWorkloadExecutionPlan(gatePlan GatePlan, catalog WorkloadCatalog, snapshot DurationLedgerSnapshot, resolvedContext PlanningContext, executionIDs []GateID, index DurationSampleIndex, shards []ShardPlan, compileGroups []CompileGroup) (WorkloadExecutionPlan, error) {
	packingEvidence, err := deriveWorkloadPackingEvidence(shards, compileGroups, resolvedContext)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	ownerDuration, catalogDigest, executionDigest, err := workloadExecutionPlanDerivedFields(catalog, index, executionIDs)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	estimationPolicyDigest, err := workloadEstimationPolicyDigest(resolvedContext)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	planningPolicyDigest, err := cicontract.WorkloadPlanningPolicyDigest()
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	plan := WorkloadExecutionPlan{
		SchemaVersion: workloadExecutionPlanSchemaVersion, AlgorithmID: WorkloadPlanningAlgorithmID,
		ObjectiveDigest: workloadPlanningObjectiveDigest(), PlanningPolicyDigest: planningPolicyDigest, GatePlanDigest: gatePlan.PlanDigest,
		CatalogDigest: catalogDigest, LedgerGeneration: snapshot.Generation, Context: resolvedContext,
		Catalog: catalog, ExecutionWorkloadIDs: slices.Clone(executionIDs), ExecutionWorkloadDigest: executionDigest,
		CompileGroups: compileGroups, Shards: shards, PackingEvidence: packingEvidence, OwnerEstimatedDurationMS: ownerDuration,
	}
	plan.EstimationPolicyDigest = estimationPolicyDigest
	if err := cicontract.ValidateWorkloadPlanContract(
		plan.SchemaVersion, plan.AlgorithmID, plan.ObjectiveDigest, plan.PlanningPolicyDigest, plan.EstimationPolicyDigest,
		workloadEstimationPolicyMaterial(plan.Context),
	); err != nil {
		return WorkloadExecutionPlan{}, fmt.Errorf("validate produced workload execution plan contract: %w", err)
	}
	plan.PlanDigest, err = plan.digest()
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	if err := plan.ValidateStored(gatePlan); err != nil {
		return WorkloadExecutionPlan{}, err
	}
	return plan, nil
}

// validateWorkloadPackingEvidence 校验每个 resource tier 的结构化 D-CPAP 见证。
func validateWorkloadPackingEvidence(plan WorkloadExecutionPlan) error {
	if len(plan.PackingEvidence) == 0 {
		return errors.New("workload execution plan packing evidence is required")
	}
	actualShards, err := collectPackingEvidenceShardCounts(plan)
	if err != nil {
		return err
	}
	if err := validatePackingEvidenceEntries(plan.PackingEvidence, actualShards); err != nil {
		return err
	}
	if len(actualShards) != 0 {
		return errors.New("workload execution plan has resource tiers without packing evidence")
	}
	return validatePackingEvidenceCalibration(plan)
}

// collectPackingEvidenceShardCounts 统计执行计划中每个资源档的实际 shard 数量。
func collectPackingEvidenceShardCounts(plan WorkloadExecutionPlan) (map[cicontract.WorkloadResourceTier]int, error) {
	actualShards := make(map[cicontract.WorkloadResourceTier]int)
	for shardIndex, shard := range plan.Shards {
		tier, err := validatePackingEvidenceShard(shardIndex, shard)
		if err != nil {
			return nil, err
		}
		actualShards[tier]++
	}
	return actualShards, nil
}

// validatePackingEvidenceShard 校验单个 shard 非空且所有 workload 属于同一资源档。
func validatePackingEvidenceShard(shardIndex int, shard ShardPlan) (cicontract.WorkloadResourceTier, error) {
	if len(shard.Workloads) == 0 {
		return 0, fmt.Errorf("workload shard %d is empty", shardIndex)
	}
	tier, err := plannedWorkloadResourceTier(shard.Workloads[0])
	if err != nil {
		return 0, fmt.Errorf("packing evidence shard %d resource tier: %w", shardIndex, err)
	}
	for _, workload := range shard.Workloads[1:] {
		other, err := plannedWorkloadResourceTier(workload)
		if err != nil {
			return 0, fmt.Errorf("packing evidence shard %d resource tier: %w", shardIndex, err)
		}
		if other != tier {
			return 0, fmt.Errorf("workload shard %d mixes resource tiers", shardIndex)
		}
	}
	return tier, nil
}

// validatePackingEvidenceEntries 校验证据排序、shard 绑定、计数关系和 solver mode。
func validatePackingEvidenceEntries(entries []WorkloadPackingEvidence, actualShards map[cicontract.WorkloadResourceTier]int) error {
	previousTier := cicontract.WorkloadResourceTier(0)
	for index, evidence := range entries {
		if err := validatePackingEvidenceTierIdentity(index, evidence, previousTier); err != nil {
			return err
		}
		previousTier = evidence.ResourceTier
		if err := validatePackingEvidenceShardBinding(index, evidence, actualShards); err != nil {
			return err
		}
		delete(actualShards, evidence.ResourceTier)
		if err := validatePackingEvidenceCounts(index, evidence); err != nil {
			return err
		}
		if err := validatePackingEvidenceMode(index, evidence); err != nil {
			return err
		}
	}
	return nil
}

// validatePackingEvidenceTierIdentity 校验 resource tier 范围及 canonical 严格递增顺序。
func validatePackingEvidenceTierIdentity(index int, evidence WorkloadPackingEvidence, previousTier cicontract.WorkloadResourceTier) error {
	if evidence.ResourceTier < cicontract.WorkloadResourceTierFast || evidence.ResourceTier > cicontract.WorkloadResourceTierSlow {
		return fmt.Errorf("packing evidence %d has unsupported resource tier %d", index, evidence.ResourceTier)
	}
	if evidence.ResourceTier <= previousTier {
		return errors.New("packing evidence resource tiers must be strictly canonical")
	}
	return nil
}

// validatePackingEvidenceShardBinding 要求每条证据的 planned shard 数绑定实际计划。
func validatePackingEvidenceShardBinding(index int, evidence WorkloadPackingEvidence, actualShards map[cicontract.WorkloadResourceTier]int) error {
	if evidence.PlannedShards != actualShards[evidence.ResourceTier] {
		return fmt.Errorf("packing evidence %d planned shard count does not match plan shards", index)
	}
	return nil
}

// validatePackingEvidenceCounts 校验非负计数、lower bound 和 heuristic gap 的一致性。
func validatePackingEvidenceCounts(index int, evidence WorkloadPackingEvidence) error {
	if evidence.PackableUnitCount < 0 || evidence.IsolatedUnitCount < 0 || evidence.LowerBoundShards < 0 || evidence.PlannedShards < 0 || evidence.HeuristicGapShards < 0 {
		return fmt.Errorf("packing evidence %d has negative counts", index)
	}
	if evidence.PlannedShards < evidence.LowerBoundShards || evidence.HeuristicGapShards != evidence.PlannedShards-evidence.LowerBoundShards {
		return fmt.Errorf("packing evidence %d has inconsistent shard lower bound/gap", index)
	}
	if evidence.LowerBoundShards < evidence.IsolatedUnitCount {
		return fmt.Errorf("packing evidence %d lower bound omits isolated units", index)
	}
	return nil
}

// validatePackingEvidenceMode 校验 packable 数量对应的 solver mode 和 gap 约束。
func validatePackingEvidenceMode(index int, evidence WorkloadPackingEvidence) error {
	if evidence.PackableUnitCount == 0 {
		if evidence.SolverMode != cicontract.WorkloadPlanningIsolatedSolverModeID || evidence.HeuristicGapShards != 0 || evidence.PlannedShards != evidence.LowerBoundShards {
			return fmt.Errorf("packing evidence %d isolated-only mode is inconsistent", index)
		}
		return nil
	}
	if evidence.PackableUnitCount <= cicontract.WorkloadPlanningExactPackableUnitThreshold {
		if evidence.SolverMode != cicontract.WorkloadPlanningExactSolverModeID || evidence.HeuristicGapShards != 0 {
			return fmt.Errorf("packing evidence %d exact mode is inconsistent", index)
		}
		return nil
	}
	if evidence.SolverMode != cicontract.WorkloadPlanningHeuristicSolverModeID {
		return fmt.Errorf("packing evidence %d heuristic mode is inconsistent", index)
	}
	return nil
}

// validatePackingEvidenceCalibration 校验 calibration 计划只使用 medium resource tier。
func validatePackingEvidenceCalibration(plan WorkloadExecutionPlan) error {
	if !plan.Context.Calibration {
		return nil
	}
	for _, evidence := range plan.PackingEvidence {
		if evidence.ResourceTier != cicontract.WorkloadResourceTierMedium {
			return errors.New("calibration workload packing evidence must use the medium resource tier")
		}
	}
	return nil
}
