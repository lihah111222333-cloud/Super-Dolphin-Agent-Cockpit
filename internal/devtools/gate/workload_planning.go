// Package gate 提供独立 CI workload 的确定性建模与分片规划。
package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// PlanLPT 使用当前比较环境的账本样本生成最长处理时间优先的分片。
func PlanLPT(catalog WorkloadCatalog, ledger DurationLedger, context PlanningContext) ([]ShardPlan, error) {
	if err := validateLPTInputs(catalog, ledger, context); err != nil {
		return nil, err
	}
	index, err := BuildDurationSampleIndex(ledger, context)
	if err != nil {
		return nil, err
	}
	return planLPTWithIndex(catalog, index)
}

func planLPTWithIndex(catalog WorkloadCatalog, index DurationSampleIndex) ([]ShardPlan, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
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

	return distributeLPTWithinTarget(planned, index.context), nil
}

// distributeLPTWithinTarget 只按 100 秒目标选择最小分片数；每个 workload 都可独立执行。
func distributeLPTWithinTarget(planned []PlannedWorkload, context PlanningContext) []ShardPlan {
	for shardCount := 1; shardCount <= len(planned); shardCount++ {
		shards := distributeLPT(planned, shardCount)
		if shardCount == len(planned) || lptShardsMeetTarget(shards, context.TargetDurationMS) {
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

// validateLPTInputs 校验 LPT 计算的目录、账本和固定 100 秒规划上下文。
func validateLPTInputs(catalog WorkloadCatalog, ledger DurationLedger, context PlanningContext) error {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return err
	}
	if err := ValidateDurationLedger(ledger); err != nil {
		return err
	}
	return validatePlanningContext(context)
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

// BuildWorkloadExecutionPlan 使用完整权威 catalog 构建全执行 LPT 计划。
func BuildWorkloadExecutionPlan(
	gatePlan GatePlan,
	catalog WorkloadCatalog,
	snapshot DurationLedgerSnapshot,
	context PlanningContext,
) (WorkloadExecutionPlan, error) {
	return BuildWorkloadExecutionPlanForWorkloads(gatePlan, catalog, snapshot, context, allShardableWorkloadIDs(catalog))
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
	index, err := prepareWorkloadExecutionPlanInputs(gatePlan, catalog, snapshot, context)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	executionCatalog, err := workloadExecutionCatalog(catalog, executionIDs)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	shards, err := planLPTWithIndex(executionCatalog, index)
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
		CatalogDigest: catalogDigest, LedgerGeneration: snapshot.Generation, Context: context,
		Catalog: catalog, ExecutionWorkloadIDs: slices.Clone(executionIDs), ExecutionWorkloadDigest: executionDigest,
		Shards: shards, OwnerEstimatedDurationMS: ownerDuration,
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
func prepareWorkloadExecutionPlanInputs(gatePlan GatePlan, catalog WorkloadCatalog, snapshot DurationLedgerSnapshot, context PlanningContext) (DurationSampleIndex, error) {
	if err := gatePlan.Validate(); err != nil {
		return DurationSampleIndex{}, fmt.Errorf("validate gate plan: %w", err)
	}
	if snapshot.Generation == 0 {
		return DurationSampleIndex{}, errors.New("duration ledger generation must be positive")
	}
	if err := validateCurrentWorkloadCatalog(gatePlan, catalog); err != nil {
		return DurationSampleIndex{}, err
	}
	if err := ValidateDurationLedger(snapshot.Ledger); err != nil {
		return DurationSampleIndex{}, err
	}
	if err := validatePlanningContext(context); err != nil {
		return DurationSampleIndex{}, err
	}
	return DurationSampleIndexFromSnapshot(snapshot, context)
}

// Validate 使用指定账本快照重新规划，拒绝估时、generation 或分组漂移。
func (plan WorkloadExecutionPlan) Validate(gatePlan GatePlan, snapshot DurationLedgerSnapshot) error {
	if err := plan.ValidateStored(gatePlan); err != nil {
		return err
	}
	expected, err := BuildWorkloadExecutionPlanForWorkloads(gatePlan, plan.Catalog, snapshot, plan.Context, plan.ExecutionWorkloadIDs)
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

func allShardableWorkloadIDs(catalog WorkloadCatalog) []GateID {
	ids := make([]GateID, 0, shardableWorkloadCount(catalog))
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			ids = append(ids, GateID(workload.ID))
		}
	}
	return ids
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
	for index, shard := range plan.Shards {
		if err := validateStoredWorkloadShard(shard, index, seen, catalog); err != nil {
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
	expected := distributeLPTWithinTarget(planned, plan.Context)
	if !equalShardPlans(plan.Shards, expected) {
		return errors.New("workload execution plan does not use the minimal target-bound shard layout")
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
) error {
	if shard.Index != index || len(shard.Workloads) == 0 || shard.EstimatedDurationMS <= 0 {
		return fmt.Errorf("workload shard %d identity is invalid", index)
	}
	var total int64
	for _, planned := range shard.Workloads {
		duration, err := validateStoredPlannedWorkload(planned, index, seen, catalog)
		if err != nil {
			return err
		}
		if duration > int64(^uint64(0)>>1)-total {
			return fmt.Errorf("workload shard %d duration overflows", index)
		}
		total += duration
	}
	if total != shard.EstimatedDurationMS {
		return fmt.Errorf("workload shard %d estimated duration mismatch", index)
	}
	return nil
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

func isPrefixedSHA256Digest(value string) bool {
	const prefix = "sha256:"
	return strings.HasPrefix(value, prefix) && isSHA256Digest(strings.TrimPrefix(value, prefix))
}

func planShardableWorkloads(
	catalog WorkloadCatalog,
	index DurationSampleIndex,
) ([]PlannedWorkload, error) {
	planned := make([]PlannedWorkload, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		if !workload.Shardable {
			continue
		}
		estimate, err := index.EstimateWorkloadDurationMS(workload)
		if err != nil {
			return nil, err
		}
		planned = append(planned, PlannedWorkload{Workload: workload, EstimatedDurationMS: estimate})
	}
	if len(planned) == 0 {
		return nil, errors.New("workload catalog contains no shardable workload")
	}
	return planned, nil
}
