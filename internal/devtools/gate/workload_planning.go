// Package gate 提供独立 CI workload 的确定性建模与分片规划。
package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func planLPTWithIndex(catalog WorkloadCatalog, index DurationSampleIndex) ([]ShardPlan, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return nil, err
	}
	if err := rejectExpansionOnlyWorkloads(catalog); err != nil {
		return nil, err
	}
	planned, err := planShardableWorkloads(catalog, index)
	if err != nil {
		return nil, err
	}
	sort.Slice(planned, func(left, right int) bool {
		if planned[left].EstimatedDurationMS != planned[right].EstimatedDurationMS {
			return planned[left].EstimatedDurationMS > planned[right].EstimatedDurationMS
		}
		return planned[left].Workload.ID < planned[right].Workload.ID
	})

	return distributeLPTForPlanningContext(planned, index.context)
}

// distributeLPTForPlanningContext 在校准固定资源下跨时长档位打包；普通规划仍隔离资源档位。
func distributeLPTForPlanningContext(planned []PlannedWorkload, context PlanningContext) ([]ShardPlan, error) {
	if context.Calibration {
		return distributeLPTWithinTarget(planned, context), nil
	}
	return distributeTieredLPTWithinTarget(planned, context)
}

// distributeLPTWithinTarget 只按 100 秒目标选择最小分片数；每个 workload 都可独立执行。
func distributeLPTWithinTarget(planned []PlannedWorkload, context PlanningContext) []ShardPlan {
	targetDurationMS := context.TargetDurationMS
	if !context.Calibration {
		targetDurationMS -= context.ShardOverheadP95MS
	}
	for shardCount := 1; shardCount <= len(planned); shardCount++ {
		shards := distributeLPT(planned, shardCount)
		if shardCount == len(planned) || lptShardsMeetTarget(shards, targetDurationMS) {
			return shards
		}
	}
	panic("unreachable LPT shard count")
}

// lptShardsMeetTarget 允许不可再拆的单 workload 超过目标，其余分片必须处于目标内。
func lptShardsMeetTarget(shards []ShardPlan, targetDurationMS int64) bool {
	for _, shard := range shards {
		if shard.EstimatedDurationMS <= targetDurationMS {
			continue
		}
		if len(shard.Workloads) == 1 &&
			shard.Workloads[0].EstimatedDurationMS > targetDurationMS {
			continue
		}
		return false
	}
	return true
}

func distributeLPT(planned []PlannedWorkload, shardCount int) []ShardPlan {
	shards := make([]ShardPlan, shardCount)
	for index := range shards {
		shards[index].Index = index
	}
	for _, workload := range planned {
		shardIndex := leastLoadedShard(shards)
		shards[shardIndex].Workloads = append(shards[shardIndex].Workloads, workload)
		shards[shardIndex].EstimatedDurationMS += workload.EstimatedDurationMS
	}
	return shards
}

// BuildWorkloadExecutionPlanForWorkloads 将完整权威 catalog 与严格 execution 投影一同绑定。
// 投影只能包含当前 catalog 中可分片的 workload，且保持其 canonical 顺序；它不是另一个 catalog。
func BuildWorkloadExecutionPlanForWorkloads(
	gatePlan GatePlan,
	catalog WorkloadCatalog,
	snapshot DurationLedgerSnapshot,
	context PlanningContext,
	executionIDs []GateID,
) (WorkloadExecutionPlan, error) {
	return buildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, executionIDs, nil, false)
}

// BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs 将 partial-reuse miss 投影绑定到
// compile-aware planner；matched PASS workload 不在 executionIDs 中，绝不会进入分组或分片。
func BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(
	gatePlan GatePlan,
	catalog WorkloadCatalog,
	snapshot DurationLedgerSnapshot,
	context PlanningContext,
	executionIDs []GateID,
	compileInputs map[GateID]CompileGroupInput,
) (WorkloadExecutionPlan, error) {
	return buildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, executionIDs, compileInputs, true)
}

func buildWorkloadExecutionPlanForWorkloads(
	gatePlan GatePlan,
	catalog WorkloadCatalog,
	snapshot DurationLedgerSnapshot,
	context PlanningContext,
	executionIDs []GateID,
	compileInputs map[GateID]CompileGroupInput,
	compileAware bool,
) (WorkloadExecutionPlan, error) {
	index, resolvedContext, err := prepareWorkloadExecutionPlanInputs(gatePlan, catalog, snapshot, context)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	executionCatalog, err := workloadExecutionCatalog(catalog, executionIDs)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	var shards []ShardPlan
	var compileGroups []CompileGroup
	if compileAware {
		shards, compileGroups, err = planLPTWithCompileInputs(executionCatalog, index, compileInputs)
	} else {
		shards, err = planLPTWithIndex(executionCatalog, index)
	}
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	ownerDuration, err := estimateOwnerWorkloadDurationMS(catalog, index)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	catalogDigest, err := workloadCatalogDigest(catalog)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	executionDigest, err := workloadExecutionDigest(executionIDs)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	plan := WorkloadExecutionPlan{
		SchemaVersion: workloadExecutionPlanSchemaVersion, GatePlanDigest: gatePlan.PlanDigest,
		CatalogDigest: catalogDigest, LedgerGeneration: snapshot.Generation, Context: resolvedContext,
		Catalog: catalog, ExecutionWorkloadIDs: slices.Clone(executionIDs), ExecutionWorkloadDigest: executionDigest,
		CompileGroups: compileGroups, Shards: shards, OwnerEstimatedDurationMS: ownerDuration,
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

// prepareWorkloadExecutionPlanInputs 验证计划的全部权威输入并建立当前样本索引。
func prepareWorkloadExecutionPlanInputs(gatePlan GatePlan, catalog WorkloadCatalog, snapshot DurationLedgerSnapshot, context PlanningContext) (DurationSampleIndex, PlanningContext, error) {
	if err := gatePlan.Validate(); err != nil {
		return DurationSampleIndex{}, PlanningContext{}, fmt.Errorf("validate gate plan: %w", err)
	}
	if snapshot.Generation == 0 {
		return DurationSampleIndex{}, PlanningContext{}, errors.New("duration ledger generation must be positive")
	}
	if err := validateCurrentWorkloadCatalog(gatePlan, catalog); err != nil {
		return DurationSampleIndex{}, PlanningContext{}, err
	}
	if err := rejectExpansionOnlyWorkloads(catalog); err != nil {
		return DurationSampleIndex{}, PlanningContext{}, err
	}
	if err := ValidateDurationLedger(snapshot.Ledger); err != nil {
		return DurationSampleIndex{}, PlanningContext{}, err
	}
	resolved, err := ResolvePlanningContext(context, snapshot.Ledger)
	if err != nil {
		return DurationSampleIndex{}, PlanningContext{}, err
	}
	index, err := DurationSampleIndexFromSnapshot(snapshot, resolved)
	if err != nil {
		return DurationSampleIndex{}, PlanningContext{}, err
	}
	return index, resolved, nil
}

// Validate 使用指定账本快照重新规划，拒绝估时、generation 或分组漂移。
func (plan WorkloadExecutionPlan) Validate(gatePlan GatePlan, snapshot DurationLedgerSnapshot) error {
	if err := plan.ValidateStored(gatePlan); err != nil {
		return err
	}
	var expected WorkloadExecutionPlan
	var err error
	if len(plan.CompileGroups) > 0 {
		compileInputs, inputErr := compileInputsFromPlan(plan)
		if inputErr != nil {
			return inputErr
		}
		expected, err = BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(gatePlan, plan.Catalog, snapshot, plan.Context, plan.ExecutionWorkloadIDs, compileInputs)
	} else {
		expected, err = BuildWorkloadExecutionPlanForWorkloads(gatePlan, plan.Catalog, snapshot, plan.Context, plan.ExecutionWorkloadIDs)
	}
	if err != nil {
		return err
	}
	if plan.PlanDigest != expected.PlanDigest {
		return errors.New("workload execution plan does not match current ledger snapshot")
	}
	return nil
}

// ValidateStored 校验持久化计划的摘要、目录、覆盖和 owner-only 边界。
func (plan WorkloadExecutionPlan) ValidateStored(gatePlan GatePlan) error {
	if err := gatePlan.ValidateStored(); err != nil {
		return fmt.Errorf("validate stored gate plan: %w", err)
	}
	if err := plan.validateStoredPayload(); err != nil {
		return err
	}
	if plan.GatePlanDigest != gatePlan.PlanDigest {
		return errors.New("workload execution plan gate identity drifted")
	}
	if err := validateWorkloadCatalogForGatePlan(gatePlan, plan.Catalog); err != nil {
		return err
	}
	return nil
}

// validateStoredPayload 校验不依赖当前账本或 registry 的不可变历史计划内容。
func (plan WorkloadExecutionPlan) validateStoredPayload() error {
	if err := plan.validateHeader(); err != nil {
		return err
	}
	if err := validateStoredWorkloadShards(plan); err != nil {
		return err
	}
	digest, err := plan.digest()
	if err != nil {
		return err
	}
	if plan.PlanDigest != digest {
		return errors.New("workload execution plan digest mismatch")
	}
	return nil
}

// validateHeader 校验执行计划的 schema、上游身份、上下文和目录摘要。
func (plan WorkloadExecutionPlan) validateHeader() error {
	if err := validateWorkloadPlanIdentity(plan); err != nil {
		return err
	}
	if err := validatePlanningContext(plan.Context); err != nil {
		return err
	}
	if err := validateWorkloadPlanOwnerDuration(plan); err != nil {
		return err
	}
	catalogDigest, err := workloadCatalogDigest(plan.Catalog)
	if err != nil {
		return err
	}
	if plan.CatalogDigest != catalogDigest {
		return errors.New("workload execution plan catalog digest mismatch")
	}
	if !isPrefixedSHA256Digest(plan.PlanDigest) {
		return errors.New("workload execution plan digest is invalid")
	}
	return nil
}

// validateCurrentWorkloadCatalog 允许当前 canonical 目录或严格受限的选择性目录。
func validateCurrentWorkloadCatalog(gatePlan GatePlan, catalog WorkloadCatalog) error {
	if !catalog.Authoritative {
		return validateSelectedWorkloadCatalog(gatePlan, catalog)
	}
	canonical, err := BuildWorkloadCatalog(gatePlan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		return err
	}
	canonicalDigest, err := workloadCatalogDigest(canonical)
	if err != nil {
		return err
	}
	actualDigest, err := workloadCatalogDigest(catalog)
	if err != nil {
		return err
	}
	if actualDigest == canonicalDigest {
		return nil
	}
	return validateWorkloadCatalogForGatePlan(gatePlan, catalog)
}

func rejectExpansionOnlyWorkloads(catalog WorkloadCatalog) error {
	for _, workload := range catalog.Workloads {
		parent, _, _, targeted, err := parseTargetWorkloadID(workload.ID)
		if err != nil {
			return err
		}
		if isExpansionOnlyGate(parent) && !targeted {
			return fmt.Errorf("workload %q is an expansion descriptor and cannot enter execution planning", workload.ID)
		}
	}
	return nil
}

// workloadExecutionCatalog 从完整目录投影严格、按规范顺序的执行集合，不创建第二权威。
func workloadExecutionCatalog(catalog WorkloadCatalog, executionIDs []GateID) (WorkloadCatalog, error) {
	if len(executionIDs) == 0 {
		return WorkloadCatalog{}, errors.New("workload execution projection is empty")
	}
	selected, err := workloadExecutionIDSet(executionIDs)
	if err != nil {
		return WorkloadCatalog{}, err
	}
	projected := WorkloadCatalog{Version: catalog.Version, Authoritative: false}
	for _, workload := range catalog.Workloads {
		if _, ok := selected[GateID(workload.ID)]; !ok {
			continue
		}
		if !workload.Shardable {
			return WorkloadCatalog{}, fmt.Errorf("workload execution projection contains non-shardable workload %q", workload.ID)
		}
		projected.Workloads = append(projected.Workloads, workload)
	}
	if len(projected.Workloads) != len(executionIDs) {
		return WorkloadCatalog{}, errors.New("workload execution projection contains workload outside catalog")
	}
	for index, workload := range projected.Workloads {
		if GateID(workload.ID) != executionIDs[index] {
			return WorkloadCatalog{}, errors.New("workload execution projection does not preserve canonical order")
		}
	}
	return projected, nil
}

func workloadExecutionIDSet(executionIDs []GateID) (map[GateID]struct{}, error) {
	selected := make(map[GateID]struct{}, len(executionIDs))
	for _, id := range executionIDs {
		if id == "" {
			return nil, errors.New("workload execution projection contains an empty workload")
		}
		if _, duplicate := selected[id]; duplicate {
			return nil, fmt.Errorf("workload execution projection contains duplicate workload %q", id)
		}
		selected[id] = struct{}{}
	}
	return selected, nil
}

func workloadExecutionDigest(executionIDs []GateID) (string, error) {
	if len(executionIDs) == 0 {
		return "", errors.New("workload execution projection is empty")
	}
	encoded, err := json.Marshal(executionIDs)
	if err != nil {
		return "", fmt.Errorf("marshal workload execution projection: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// validateStoredWorkloadShards 校验计划分片只且完整覆盖已绑定的执行投影。
func validateStoredWorkloadShards(plan WorkloadExecutionPlan) error {
	executionCatalog, err := workloadExecutionCatalog(plan.Catalog, plan.ExecutionWorkloadIDs)
	if err != nil {
		return err
	}
	executionDigest, err := workloadExecutionDigest(plan.ExecutionWorkloadIDs)
	if err != nil || plan.ExecutionWorkloadDigest != executionDigest {
		return errors.New("workload execution projection digest mismatch")
	}
	if err := validateStoredWorkloadShardCount(plan, shardableWorkloadCount(executionCatalog)); err != nil {
		return err
	}
	seen := make(map[string]struct{}, shardableWorkloadCount(executionCatalog))
	catalog := make(map[string]Workload, len(executionCatalog.Workloads))
	for _, workload := range executionCatalog.Workloads {
		catalog[workload.ID] = workload
	}
	groups, groupedWorkloads, err := validateStoredCompileGroups(plan, executionCatalog)
	if err != nil {
		return err
	}
	for index, shard := range plan.Shards {
		if err := validateStoredWorkloadShard(shard, index, seen, catalog, groups, groupedWorkloads); err != nil {
			return err
		}
	}
	if err := validateStoredWorkloadCoverage(executionCatalog, seen); err != nil {
		return err
	}
	return validateStoredWorkloadShardLayout(plan)
}

func validateStoredWorkloadShardCount(plan WorkloadExecutionPlan, shardableCount int) error {
	if shardableCount == 0 {
		return nil
	}
	if len(plan.Shards) == 0 || len(plan.Shards) > shardableCount {
		return errors.New("workload execution plan shard count is invalid")
	}
	return nil
}

// validateStoredWorkloadShardLayout 拒绝偏离 100 秒目标的过度分片计划。
func validateStoredWorkloadShardLayout(plan WorkloadExecutionPlan) error {
	if len(plan.CompileGroups) > 0 {
		return nil
	}
	planned := make([]PlannedWorkload, 0, len(plan.ExecutionWorkloadIDs))
	for _, shard := range plan.Shards {
		planned = append(planned, shard.Workloads...)
	}
	sort.Slice(planned, func(left, right int) bool {
		if planned[left].EstimatedDurationMS != planned[right].EstimatedDurationMS {
			return planned[left].EstimatedDurationMS > planned[right].EstimatedDurationMS
		}
		return planned[left].Workload.ID < planned[right].Workload.ID
	})
	expected, err := distributeLPTForPlanningContext(planned, plan.Context)
	if err != nil {
		return fmt.Errorf("rebuild resource-tiered workload shard layout: %w", err)
	}
	if !equalShardPlans(plan.Shards, expected) {
		return errors.New("workload execution plan does not use the resource-tiered minimal target-bound shard layout")
	}
	return nil
}

// equalShardPlans 比较按 LPT 生成的分片顺序、估时和 workload 归属。
func equalShardPlans(left []ShardPlan, right []ShardPlan) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Index != right[index].Index ||
			left[index].EstimatedDurationMS != right[index].EstimatedDurationMS ||
			!compileGroupIDsEqual(left[index].CompileGroupIDs, right[index].CompileGroupIDs) ||
			len(left[index].Workloads) != len(right[index].Workloads) {
			return false
		}
		for workloadIndex := range left[index].Workloads {
			if left[index].Workloads[workloadIndex] != right[index].Workloads[workloadIndex] {
				return false
			}
		}
	}
	return true
}

// validateStoredWorkloadCoverage 校验持久化计划完整覆盖全部可分片 workload。
func validateStoredWorkloadCoverage(
	catalog WorkloadCatalog,
	seen map[string]struct{},
) error {
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			if _, ok := seen[workload.ID]; !ok {
				return fmt.Errorf("workload execution plan omits workload %q", workload.ID)
			}
		}
	}
	return nil
}

// validateStoredWorkloadShard 校验单个 shard 的顺序、非空集合与估时总和。
func validateStoredWorkloadShard(
	shard ShardPlan,
	index int,
	seen map[string]struct{},
	catalog map[string]Workload,
	groups map[string]CompileGroup,
	groupedWorkloads map[string]string,
) error {
	if shard.Index != index || len(shard.Workloads) == 0 || shard.EstimatedDurationMS <= 0 {
		return fmt.Errorf("workload shard %d identity is invalid", index)
	}
	ordinaryTotal, plannedGroupWorkloads, err := collectStoredShardWorkloads(shard, index, seen, catalog, groupedWorkloads)
	if err != nil {
		return err
	}
	groupTotal, err := collectStoredShardGroups(shard, index, groups, plannedGroupWorkloads)
	if err != nil {
		return err
	}
	if ordinaryTotal > int64(^uint64(0)>>1)-groupTotal {
		return fmt.Errorf("workload shard %d duration overflows", index)
	}
	total := ordinaryTotal + groupTotal
	if total != shard.EstimatedDurationMS {
		return fmt.Errorf("workload shard %d estimated duration mismatch", index)
	}
	return nil
}

func collectStoredShardWorkloads(shard ShardPlan, index int, seen map[string]struct{}, catalog map[string]Workload, grouped map[string]string) (int64, map[string]map[string]struct{}, error) {
	var total int64
	groupedWorkloads := make(map[string]map[string]struct{}, len(shard.CompileGroupIDs))
	for _, planned := range shard.Workloads {
		duration, err := validateStoredPlannedWorkload(planned, index, seen, catalog)
		if err != nil {
			return 0, nil, err
		}
		groupID, isGrouped := grouped[planned.Workload.ID]
		if isGrouped {
			if !slices.Contains(shard.CompileGroupIDs, groupID) {
				return 0, nil, fmt.Errorf("workload shard %d grouped workload %q is missing its compile group", index, planned.Workload.ID)
			}
			if groupedWorkloads[groupID] == nil {
				groupedWorkloads[groupID] = make(map[string]struct{})
			}
			groupedWorkloads[groupID][planned.Workload.ID] = struct{}{}
			continue
		}
		if total > int64(^uint64(0)>>1)-duration {
			return 0, nil, fmt.Errorf("workload shard %d duration overflows", index)
		}
		total += duration
	}
	return total, groupedWorkloads, nil
}

func collectStoredShardGroups(shard ShardPlan, index int, groups map[string]CompileGroup, planned map[string]map[string]struct{}) (int64, error) {
	var total int64
	for _, groupID := range shard.CompileGroupIDs {
		group, ok := groups[groupID]
		if !ok {
			return 0, fmt.Errorf("workload shard %d references unknown compile group %q", index, groupID)
		}
		if !sameStringSet(planned[groupID], gateIDStringSet(group.WorkloadIDs)) {
			return 0, fmt.Errorf("workload shard %d compile group %q workload coverage mismatch", index, groupID)
		}
		if total > int64(^uint64(0)>>1)-group.EstimatedDurationMS {
			return 0, fmt.Errorf("workload shard %d duration overflows", index)
		}
		total += group.EstimatedDurationMS
	}
	return total, nil
}

func validateStoredCompileGroups(plan WorkloadExecutionPlan, catalog WorkloadCatalog) (map[string]CompileGroup, map[string]string, error) {
	groups := make(map[string]CompileGroup, len(plan.CompileGroups))
	groupedWorkloads := make(map[string]string)
	artifactGroups := make(map[string]string)
	if len(plan.CompileGroups) == 0 {
		if err := rejectUnexpectedCompileGroupReferences(plan); err != nil {
			return nil, nil, err
		}
		return groups, groupedWorkloads, nil
	}
	order := workloadCanonicalOrder(catalog)
	execution := executionWorkloadSet(plan.ExecutionWorkloadIDs)
	for _, group := range plan.CompileGroups {
		if err := registerStoredCompileGroup(group, groups, artifactGroups, groupedWorkloads, execution, order); err != nil {
			return nil, nil, err
		}
	}
	if err := validateStoredCompileGroupReferences(plan, groups); err != nil {
		return nil, nil, err
	}
	if err := validateStoredCompileGroupExecution(plan.ExecutionWorkloadIDs, catalog, groupedWorkloads); err != nil {
		return nil, nil, err
	}
	return groups, groupedWorkloads, nil
}

func rejectUnexpectedCompileGroupReferences(plan WorkloadExecutionPlan) error {
	for _, shard := range plan.Shards {
		if len(shard.CompileGroupIDs) != 0 {
			return fmt.Errorf("shard %d references compile groups but plan has no groups", shard.Index)
		}
	}
	return nil
}

func executionWorkloadSet(ids []GateID) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[string(id)] = struct{}{}
	}
	return set
}

func registerStoredCompileGroup(group CompileGroup, groups map[string]CompileGroup, artifactGroups map[string]string, grouped map[string]string, execution map[string]struct{}, order map[string]int) error {
	if err := group.Validate(); err != nil {
		return err
	}
	if _, duplicate := groups[group.GroupID]; duplicate {
		return fmt.Errorf("duplicate compile group %q", group.GroupID)
	}
	if err := registerCompileArtifactGroupForPlan(group, artifactGroups); err != nil {
		return err
	}
	if err := validateStoredCompileGroupMembers(group, grouped, execution, order); err != nil {
		return err
	}
	groups[group.GroupID] = group
	return nil
}

// registerCompileArtifactGroupForPlan 校验整份计划的 artifact identity。
// atomic package 的有界 selector 分组可在不同 ECI shard 使用相同 selector-independent
// artifact key；同一 shard 的重复引用仍由 compileGroupAffinityFromShardIDs 拒绝。
func registerCompileArtifactGroupForPlan(group CompileGroup, artifactGroups map[string]string) error {
	return registerCompileArtifactGroupWithPolicy(group, artifactGroups, true)
}

// registerCompileArtifactGroup 校验单个 shard manifest 的 artifact identity。
// 这里不允许 atomic duplicate，因为一个 shard 必须只运行一个同 key test-binary。
func registerCompileArtifactGroup(group CompileGroup, artifactGroups map[string]string) error {
	return registerCompileArtifactGroupWithPolicy(group, artifactGroups, false)
}

func registerCompileArtifactGroupWithPolicy(group CompileGroup, artifactGroups map[string]string, allowAtomicPartitions bool) error {
	artifactKey, err := CompileArtifactKey(group)
	if err != nil {
		return err
	}
	binding := artifactKey + "\x00" + group.ResourceClassID
	if previous, duplicate := artifactGroups[binding]; duplicate {
		if allowAtomicPartitions && isBoundedAtomicCompileGroup(group) {
			return nil
		}
		return fmt.Errorf("compile artifact %q resource class %q is duplicated by groups %q and %q", artifactKey, group.ResourceClassID, previous, group.GroupID)
	}
	artifactGroups[binding] = group.GroupID
	return nil
}

// isBoundedAtomicCompileGroup 识别只允许跨 shard 共享 selector-independent
// artifact key 的有界 atomic 子组。CompileGroup.Validate 已验证完整 selector shape；
// 这里再次保留最小谓词，避免未来调用方绕开验证后扩大例外范围。
func isBoundedAtomicCompileGroup(group CompileGroup) bool {
	return isAtomicGoPackageTarget(group.PackageTarget) &&
		len(group.WorkloadIDs) <= cicontract.CompileGroupMaxSelectors &&
		compileGroupHasExactGoSelectors(group)
}

func validateStoredCompileGroupMembers(group CompileGroup, grouped map[string]string, execution map[string]struct{}, order map[string]int) error {
	last := -1
	var groupParent GateID
	var groupKind WorkloadTargetKind
	for _, id := range group.WorkloadIDs {
		current, parent, kind, err := validateStoredCompileGroupMember(group, id, execution, order)
		if err != nil {
			return err
		}
		if current <= last {
			return fmt.Errorf("compile group %q workload IDs are not canonical", group.GroupID)
		}
		last = current
		if groupParent == "" {
			groupParent, groupKind = parent, kind
		} else if groupParent != parent || groupKind != kind {
			return fmt.Errorf("compile group %q mixes execution semantics", group.GroupID)
		}
		if previous, duplicate := grouped[string(id)]; duplicate {
			return fmt.Errorf("workload %q belongs to compile groups %q and %q", id, previous, group.GroupID)
		}
		grouped[string(id)] = group.GroupID
	}
	return nil
}

func validateStoredCompileGroupMember(group CompileGroup, id GateID, execution map[string]struct{}, order map[string]int) (int, GateID, WorkloadTargetKind, error) {
	if _, ok := execution[string(id)]; !ok {
		return 0, "", "", fmt.Errorf("compile group %q contains workload outside execution projection", group.GroupID)
	}
	current, ok := order[string(id)]
	if !ok {
		return 0, "", "", fmt.Errorf("compile group %q workload is outside catalog", group.GroupID)
	}
	parent, kind, payload, targeted, err := ParseWorkloadID(string(id))
	if err != nil || !targeted || !compileGroupTargetKind(kind) {
		return 0, "", "", fmt.Errorf("compile group %q contains a non-selector workload", group.GroupID)
	}
	target, err := parseCompileGroupTarget(kind, payload)
	if err != nil || target.Package != group.PackageTarget {
		return 0, "", "", fmt.Errorf("compile group %q package target drifted", group.GroupID)
	}
	return current, parent, kind, nil
}

func validateStoredCompileGroupReferences(plan WorkloadExecutionPlan, groups map[string]CompileGroup) error {
	references := make(map[string]int, len(groups))
	for _, shard := range plan.Shards {
		if err := compileGroupAffinityFromShardIDs(groups, shard); err != nil {
			return err
		}
		for _, groupID := range shard.CompileGroupIDs {
			references[groupID]++
		}
	}
	for groupID := range groups {
		if references[groupID] != 1 {
			return fmt.Errorf("compile group %q must be referenced by exactly one shard", groupID)
		}
	}
	return nil
}

func validateStoredCompileGroupExecution(ids []GateID, catalog WorkloadCatalog, grouped map[string]string) error {
	for _, workloadID := range ids {
		workload, ok := catalogWorkloadByID(catalog, workloadID)
		if !ok || !workload.Shardable {
			return fmt.Errorf("compile-aware execution workload %q is missing from catalog", workloadID)
		}
		_, kind, _, targeted, err := ParseWorkloadID(workload.ID)
		if err == nil && targeted && compileGroupTargetKind(kind) {
			if _, ok := grouped[workload.ID]; !ok {
				return fmt.Errorf("compile-aware plan omits group for exact selector %q", workload.ID)
			}
		}
	}
	return nil
}

func catalogWorkloadByID(catalog WorkloadCatalog, id GateID) (Workload, bool) {
	for _, workload := range catalog.Workloads {
		if GateID(workload.ID) == id {
			return workload, true
		}
	}
	return Workload{}, false
}

func gateIDStringSet(ids []GateID) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[string(id)] = struct{}{}
	}
	return set
}

func sameStringSet(left, right map[string]struct{}) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if _, ok := right[value]; !ok {
			return false
		}
	}
	return true
}

// validateStoredPlannedWorkload 校验计划项与目录一致且只归属一个 shard。
func validateStoredPlannedWorkload(
	planned PlannedWorkload,
	shardIndex int,
	seen map[string]struct{},
	catalog map[string]Workload,
) (int64, error) {
	if err := validateWorkload(planned.Workload); err != nil {
		return 0, fmt.Errorf("workload shard %d: %w", shardIndex, err)
	}
	if !planned.Workload.Shardable || planned.EstimatedDurationMS <= 0 {
		return 0, fmt.Errorf("workload shard %d contains an invalid planned workload", shardIndex)
	}
	if _, err := plannedWorkloadResourceTier(planned); err != nil {
		return 0, fmt.Errorf("workload shard %d: %w", shardIndex, err)
	}
	if canonical, ok := catalog[planned.Workload.ID]; !ok || planned.Workload != canonical {
		return 0, fmt.Errorf("workload shard %d drifted from catalog", shardIndex)
	}
	if _, duplicate := seen[planned.Workload.ID]; duplicate {
		return 0, fmt.Errorf("workload %q appears in more than one shard", planned.Workload.ID)
	}
	seen[planned.Workload.ID] = struct{}{}
	return planned.EstimatedDurationMS, nil
}

func shardableWorkloadCount(catalog WorkloadCatalog) int {
	count := 0
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			count++
		}
	}
	return count
}

func workloadCatalogDigest(catalog WorkloadCatalog) (string, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		return "", fmt.Errorf("marshal workload catalog: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

// WorkloadCatalogDigest 返回规范目录的稳定摘要，供校准账本绑定完整目录。
func WorkloadCatalogDigest(catalog WorkloadCatalog) (string, error) {
	return workloadCatalogDigest(catalog)
}

func (plan WorkloadExecutionPlan) digest() (string, error) {
	material := plan
	material.PlanDigest = ""
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", fmt.Errorf("marshal workload execution plan: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func planShardableWorkloads(
	catalog WorkloadCatalog,
	index DurationSampleIndex,
) ([]PlannedWorkload, error) {
	base, err := estimateShardableWorkloads(catalog, index)
	if err != nil {
		return nil, err
	}
	return plannedWorkloadsFromEstimates(base, nil)
}
