// Package gate 提供独立 CI workload 的确定性建模与分片规划。
package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
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

// BuildWorkloadExecutionPlan 使用固定账本 generation 构建可复算、可持久化的 LPT 计划。
func BuildWorkloadExecutionPlan(
	gatePlan GatePlan,
	catalog WorkloadCatalog,
	snapshot DurationLedgerSnapshot,
	context PlanningContext,
) (WorkloadExecutionPlan, error) {
	return BuildWorkloadExecutionPlanWithReuse(gatePlan, catalog, snapshot, context, nil)
}

// BuildWorkloadExecutionPlanWithReuse 只规划缓存未命中项，并把全部复用 workload 绑定到计划摘要。
func BuildWorkloadExecutionPlanWithReuse(
	gatePlan GatePlan,
	catalog WorkloadCatalog,
	snapshot DurationLedgerSnapshot,
	context PlanningContext,
	reusedWorkloads []string,
) (WorkloadExecutionPlan, error) {
	index, err := prepareWorkloadExecutionPlanInputs(gatePlan, catalog, snapshot, context)
	if err != nil {
		return WorkloadExecutionPlan{}, err
	}
	shards, err := planUncachedWorkloads(catalog, index, reusedWorkloads)
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
	plan := WorkloadExecutionPlan{
		SchemaVersion: workloadExecutionPlanSchemaVersion, GatePlanDigest: gatePlan.PlanDigest,
		CatalogDigest: catalogDigest, LedgerGeneration: snapshot.Generation, Context: context,
		Catalog: catalog, ReusedWorkloads: append([]string(nil), reusedWorkloads...),
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

func planUncachedWorkloads(
	catalog WorkloadCatalog,
	index DurationSampleIndex,
	reusedWorkloads []string,
) ([]ShardPlan, error) {
	reused, remaining, err := validateReusedWorkloads(catalog, reusedWorkloads)
	if err != nil {
		return nil, err
	}
	if remaining == 0 {
		return nil, nil
	}
	return planLPTWithIndex(workloadPlanningCatalog(catalog, reused), index)
}

// Validate 使用指定账本快照重新规划，拒绝估时、generation 或分组漂移。
func (plan WorkloadExecutionPlan) Validate(gatePlan GatePlan, snapshot DurationLedgerSnapshot) error {
	if err := plan.ValidateStored(gatePlan); err != nil {
		return err
	}
	expected, err := BuildWorkloadExecutionPlanWithReuse(
		gatePlan, plan.Catalog, snapshot, plan.Context, plan.ReusedWorkloads,
	)
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

// validateStoredWorkloadShards 校验 shard 数量、顺序、估时总和与精确 workload 覆盖。
func validateStoredWorkloadShards(plan WorkloadExecutionPlan) error {
	reused, remaining, err := validateReusedWorkloads(plan.Catalog, plan.ReusedWorkloads)
	if err != nil {
		return err
	}
	if err := validateStoredWorkloadShardCount(plan, remaining); err != nil {
		return err
	}
	if remaining == 0 {
		return nil
	}
	seen := make(map[string]struct{}, shardableWorkloadCount(plan.Catalog))
	catalog := make(map[string]Workload, len(plan.Catalog.Workloads))
	for _, workload := range plan.Catalog.Workloads {
		catalog[workload.ID] = workload
	}
	for index, shard := range plan.Shards {
		if err := validateStoredWorkloadShard(shard, index, seen, catalog); err != nil {
			return err
		}
	}
	if err := validateStoredWorkloadCoverage(plan.Catalog, reused, seen); err != nil {
		return err
	}
	return validateStoredWorkloadShardLayout(plan)
}

func validateStoredWorkloadShardCount(plan WorkloadExecutionPlan, remaining int) error {
	if remaining == 0 {
		if len(plan.Shards) != 0 {
			return errors.New("workload execution plan has shards after every workload was reused")
		}
		return nil
	}
	if len(plan.Shards) == 0 || len(plan.Shards) > remaining {
		return errors.New("workload execution plan shard count is invalid")
	}
	return nil
}

// validateStoredWorkloadShardLayout 拒绝偏离 100 秒目标的过度分片计划。
func validateStoredWorkloadShardLayout(plan WorkloadExecutionPlan) error {
	planned := make([]PlannedWorkload, 0, shardableWorkloadCount(plan.Catalog)-len(plan.ReusedWorkloads))
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

// validateStoredWorkloadCoverage 校验持久化计划完整覆盖未复用的可分片 workload。
func validateStoredWorkloadCoverage(
	catalog WorkloadCatalog,
	reused map[string]struct{},
	seen map[string]struct{},
) error {
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			if _, skipped := reused[workload.ID]; skipped {
				if _, observed := seen[workload.ID]; observed {
					return fmt.Errorf("workload execution plan executes reused workload %q", workload.ID)
				}
				continue
			}
			if _, ok := seen[workload.ID]; !ok {
				return fmt.Errorf("workload execution plan omits workload %q", workload.ID)
			}
		}
	}
	return nil
}

// validateReusedWorkloads 校验复用列表属于当前目录并保持唯一的目录顺序。
func validateReusedWorkloads(catalog WorkloadCatalog, reusedWorkloads []string) (map[string]struct{}, int, error) {
	positions := make(map[string]int, len(catalog.Workloads))
	shardable := make(map[string]bool, len(catalog.Workloads))
	remaining := 0
	for index, workload := range catalog.Workloads {
		positions[workload.ID] = index
		shardable[workload.ID] = workload.Shardable
		if workload.Shardable {
			remaining++
		}
	}
	reused := make(map[string]struct{}, len(reusedWorkloads))
	last := -1
	for _, workloadID := range reusedWorkloads {
		position, exists := positions[workloadID]
		if !exists || !shardable[workloadID] {
			return nil, 0, fmt.Errorf("reused workload %q is not a shardable catalog workload", workloadID)
		}
		if position <= last {
			return nil, 0, errors.New("reused workloads must be unique and preserve catalog order")
		}
		reused[workloadID] = struct{}{}
		remaining--
		last = position
	}
	return reused, remaining, nil
}

func workloadPlanningCatalog(catalog WorkloadCatalog, reused map[string]struct{}) WorkloadCatalog {
	planning := WorkloadCatalog{
		Version:       catalog.Version,
		Authoritative: catalog.Authoritative,
		Workloads:     make([]Workload, 0, len(catalog.Workloads)-len(reused)),
	}
	for _, workload := range catalog.Workloads {
		if _, skip := reused[workload.ID]; !skip {
			planning.Workloads = append(planning.Workloads, workload)
		}
	}
	return planning
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
