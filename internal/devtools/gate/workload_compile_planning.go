package gate

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const compileParentBootstrapEstimateMS int64 = 15_000

// planLPTWithCompileInputs 返回 compile-aware shard 与其严格绑定的 compile groups。
func planLPTWithCompileInputs(catalog WorkloadCatalog, index DurationSampleIndex, compileInputs map[GateID]CompileGroupInput) ([]ShardPlan, []CompileGroup, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return nil, nil, err
	}
	if err := validateCompileInputProjection(catalog, compileInputs); err != nil {
		return nil, nil, err
	}
	base, err := estimateShardableWorkloads(catalog, index)
	if err != nil {
		return nil, nil, err
	}
	ownerHints, err := BuildCompileOwnerHints(base, compileInputs, index.CompileTimingIndex, index.context)
	if err != nil {
		return nil, nil, err
	}
	planned, err := plannedWorkloadsFromEstimates(base, ownerHints)
	if err != nil {
		return nil, nil, err
	}
	order := workloadCanonicalOrder(catalog)
	units, groups, err := buildCompileUnits(planned, index, compileInputs, order, ownerHints)
	if err != nil {
		return nil, nil, err
	}
	shards, err := distributeCompileUnitsForPlanningContext(units, index.context)
	if err != nil {
		return nil, nil, err
	}
	return shards, SortCompileGroupsByID(groups), nil
}

func validateCompileInputProjection(catalog WorkloadCatalog, compileInputs map[GateID]CompileGroupInput) error {
	workloads := make(map[GateID]Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		workloads[GateID(workload.ID)] = workload
	}
	for workloadID := range compileInputs {
		_, ok := workloads[workloadID]
		if !ok {
			return fmt.Errorf("compile input for %q is outside the execution catalog", workloadID)
		}
		if !CompileGroupWorkloadSupported(workloadID) {
			return fmt.Errorf("compile input for %q is not a groupable execution workload", workloadID)
		}
	}
	return nil
}

type compilePlanningSelector struct {
	planned           PlannedWorkload
	parent            GateID
	targetKind        WorkloadTargetKind
	target            GoTestTarget
	input             CompileGroupInput
	bodyEstimateMS    int64
	compileEstimateMS int64
	canonicalOrder    int
	resourceTier      cicontract.WorkloadResourceTier
}

type compilePlanningBucketKey struct {
	affinityKey  string
	resourceTier cicontract.WorkloadResourceTier
}

type compilePlanningBucket struct {
	affinityKey  string
	resourceTier cicontract.WorkloadResourceTier
	parent       GateID
	targetKind   WorkloadTargetKind
	selectors    []compilePlanningSelector
}

type compilePlanningUnit struct {
	workloads   []PlannedWorkload
	group       *CompileGroup
	costMS      int64
	affinityKey string
	sortID      string
	tier        cicontract.WorkloadResourceTier
}

func workloadCanonicalOrder(catalog WorkloadCatalog) map[string]int {
	order := make(map[string]int, len(catalog.Workloads))
	for index, workload := range catalog.Workloads {
		order[workload.ID] = index
	}
	return order
}

// buildCompileUnits 将 exact miss 规划 workload 聚合为确定性的编译单元与编译组。
func buildCompileUnits(planned []PlannedWorkload, index DurationSampleIndex, compileInputs map[GateID]CompileGroupInput, order map[string]int, hintSets ...CompileOwnerHints) ([]compilePlanningUnit, []CompileGroup, error) {
	hints, err := compileOwnerHintsFromSet(hintSets...)
	if err != nil {
		return nil, nil, err
	}
	buckets := make(map[compilePlanningBucketKey]*compilePlanningBucket)
	units := make([]compilePlanningUnit, 0, len(planned))
	for _, item := range planned {
		if err := appendCompilePlanningItem(item, index, compileInputs, order, buckets, &units, hints); err != nil {
			return nil, nil, err
		}
	}
	keys := make([]compilePlanningBucketKey, 0, len(buckets))
	for key := range buckets {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left, right int) bool {
		if keys[left].affinityKey != keys[right].affinityKey {
			return keys[left].affinityKey < keys[right].affinityKey
		}
		return keys[left].resourceTier < keys[right].resourceTier
	})
	groups := make([]CompileGroup, 0)
	for _, key := range keys {
		bucketUnits, bucketGroups, err := splitCompilePlanningBucket(*buckets[key], index.context)
		if err != nil {
			return nil, nil, err
		}
		units = append(units, bucketUnits...)
		groups = append(groups, bucketGroups...)
	}
	sortCompilePlanningUnits(units)
	return units, groups, nil
}

// ordinaryCompilePlanningUnit 为不可分组 workload 构造带 canonical 资源档位的普通编译单元。
func ordinaryCompilePlanningUnit(item PlannedWorkload, context PlanningContext) (compilePlanningUnit, error) {
	tier, err := canonicalCompileResourceTier(item, context)
	if err != nil {
		return compilePlanningUnit{}, fmt.Errorf("classify ordinary workload %q: %w", item.Workload.ID, err)
	}
	return compilePlanningUnit{workloads: []PlannedWorkload{item}, costMS: item.EstimatedDurationMS, affinityKey: "ordinary:" + item.Workload.ID, sortID: item.Workload.ID, tier: tier}, nil
}

// splitCompilePlanningBucket 将同一编译输入和资源档位收敛为确定性的编译组。
// 普通 package 保持单一共享编译组；archtest 则按唯一 owner 的 selector 上限
// 拆成多个独立组，使每个 ECI shard 只承载一个有界 test-binary 进程。每个组
// 仍只执行一次 go test -c；跨 shard 允许 Go 自身的增量编译，但不共享 CAS。
func splitCompilePlanningBucket(bucket compilePlanningBucket, context PlanningContext) ([]compilePlanningUnit, []CompileGroup, error) {
	if len(bucket.selectors) == 0 {
		return nil, nil, errors.New("compile planning bucket is empty")
	}
	sort.SliceStable(bucket.selectors, func(left, right int) bool {
		if bucket.selectors[left].bodyEstimateMS != bucket.selectors[right].bodyEstimateMS {
			return bucket.selectors[left].bodyEstimateMS > bucket.selectors[right].bodyEstimateMS
		}
		return bucket.selectors[left].planned.Workload.ID < bucket.selectors[right].planned.Workload.ID
	})
	partitions, err := splitCompilePlanningPartitions(bucket)
	if err != nil {
		return nil, nil, err
	}
	groups := make([]CompileGroup, 0, len(partitions))
	for _, partition := range partitions {
		compileEstimate := maxCompileEstimate(partition)
		group, err := compileGroupFromPartition(partition, compileEstimate, bucket.affinityKey, context)
		if err != nil {
			return nil, nil, err
		}
		groups = append(groups, group)
	}
	units := make([]compilePlanningUnit, 0, len(partitions))
	for index, partition := range partitions {
		group := &groups[index]
		units = append(units, compilePlanningUnit{workloads: plannedWorkloads(partition), group: group, costMS: compileGroupCriticalDurationMS(*group), affinityKey: bucket.affinityKey, sortID: group.GroupID, tier: bucket.resourceTier})
	}
	return units, groups, nil
}

// splitCompilePlanningPartitions 返回稳定的 selector 分区。所有 atomic Go package
// 使用统一有界分区；普通 package 保留单一 compile group 的 artifact identity。
func splitCompilePlanningPartitions(bucket compilePlanningBucket) ([][]compilePlanningSelector, error) {
	if len(bucket.selectors) == 0 {
		return nil, errors.New("compile planning bucket is empty")
	}
	if !isAtomicGoPackageTarget(bucket.selectors[0].input.PackageTarget) {
		return [][]compilePlanningSelector{bucket.selectors}, nil
	}
	maxSelectors := cicontract.CompileGroupMaxSelectors
	if maxSelectors <= 0 {
		return nil, errors.New("compile-group selector bound must be positive")
	}
	groupCount := (len(bucket.selectors) + maxSelectors - 1) / maxSelectors
	partitions := make([][]compilePlanningSelector, groupCount)
	partitionBodies := make([]int64, groupCount)
	ordered := sortArchtestCompilePlanningSelectors(bucket.selectors)
	for _, selector := range ordered {
		if err := appendArchtestCompilePlanningSelector(partitions, partitionBodies, selector, maxSelectors); err != nil {
			return nil, err
		}
	}
	for _, partition := range partitions {
		sortArchtestCompilePlanningPartition(partition)
	}
	return partitions, nil
}

func sortArchtestCompilePlanningSelectors(selectors []compilePlanningSelector) []compilePlanningSelector {
	ordered := append([]compilePlanningSelector(nil), selectors...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].bodyEstimateMS != ordered[right].bodyEstimateMS {
			return ordered[left].bodyEstimateMS > ordered[right].bodyEstimateMS
		}
		return ordered[left].planned.Workload.ID < ordered[right].planned.Workload.ID
	})
	return ordered
}

func appendArchtestCompilePlanningSelector(partitions [][]compilePlanningSelector, bodies []int64, selector compilePlanningSelector, maxSelectors int) error {
	partitionIndex := archtestCompilePlanningPartitionIndex(partitions, bodies, maxSelectors)
	if partitionIndex < 0 {
		return errors.New("archtest compile-group partition capacity exhausted")
	}
	if selector.bodyEstimateMS > int64(^uint64(0)>>1)-bodies[partitionIndex] {
		return errors.New("archtest compile-group partition body estimate overflows")
	}
	bodies[partitionIndex] += selector.bodyEstimateMS
	partitions[partitionIndex] = append(partitions[partitionIndex], selector)
	return nil
}

func archtestCompilePlanningPartitionIndex(partitions [][]compilePlanningSelector, bodies []int64, maxSelectors int) int {
	partitionIndex := -1
	for candidate := range partitions {
		if len(partitions[candidate]) >= maxSelectors {
			continue
		}
		if partitionIndex == -1 || bodies[candidate] < bodies[partitionIndex] {
			partitionIndex = candidate
		}
	}
	return partitionIndex
}

func sortArchtestCompilePlanningPartition(partition []compilePlanningSelector) {
	sort.SliceStable(partition, func(left, right int) bool {
		if partition[left].canonicalOrder != partition[right].canonicalOrder {
			return partition[left].canonicalOrder < partition[right].canonicalOrder
		}
		return partition[left].planned.Workload.ID < partition[right].planned.Workload.ID
	})
}

func maxCompileEstimate(selectors []compilePlanningSelector) int64 {
	maximum := int64(1)
	for _, selector := range selectors {
		if selector.compileEstimateMS > maximum {
			maximum = selector.compileEstimateMS
		}
	}
	return maximum
}

// compileGroupFromPartition 以同档 selector 生成带稳定资源身份和成本闭包的编译组。
func compileGroupFromPartition(partition []compilePlanningSelector, compileEstimate int64, affinityKey string, context PlanningContext) (CompileGroup, error) {
	if len(partition) == 0 {
		return CompileGroup{}, errors.New("compile planning partition is empty")
	}
	sort.Slice(partition, func(left, right int) bool {
		if partition[left].canonicalOrder != partition[right].canonicalOrder {
			return partition[left].canonicalOrder < partition[right].canonicalOrder
		}
		return partition[left].planned.Workload.ID < partition[right].planned.Workload.ID
	})
	ids, bodyEstimate, err := compileGroupPartitionIDsAndBody(partition)
	if err != nil {
		return CompileGroup{}, err
	}
	if compileEstimate > int64(^uint64(0)>>1)-bodyEstimate {
		return CompileGroup{}, errors.New("compile group estimate overflows")
	}
	totalEstimate := compileEstimate + bodyEstimate
	resourceClassID, err := compileGroupResourceClass(partition, context)
	if err != nil {
		return CompileGroup{}, fmt.Errorf("compile group %q resource identity: %w", affinityKey, err)
	}
	selectorEstimates, batchPlan, batchWarning, err := compileGroupBatchPlan(partition, compileEstimate, resourceClassID)
	if err != nil {
		return CompileGroup{}, fmt.Errorf("compile group %q batch plan: %w", affinityKey, err)
	}
	group := CompileGroup{PackageTarget: partition[0].input.PackageTarget, SemanticKey: partition[0].input.SemanticKey, SharedInputDigest: partition[0].input.SharedInputDigest, ProfileDigest: partition[0].input.ProfileDigest, ResourceClassID: resourceClassID, WorkloadIDs: ids, SelectorEstimates: selectorEstimates, BatchPlan: batchPlan, BatchPlanWarning: batchWarning, CompileEstimateMS: compileEstimate, BodyEstimateMS: bodyEstimate, EstimatedDurationMS: totalEstimate}
	return finalizeCompileGroup(group, affinityKey)
}

// compileGroupPartitionIDsAndBody 生成 canonical selector IDs 并汇总正文估时。
func compileGroupPartitionIDsAndBody(partition []compilePlanningSelector) ([]GateID, int64, error) {
	ids := make([]GateID, len(partition))
	var bodyEstimate int64
	for index, selector := range partition {
		ids[index] = GateID(selector.planned.Workload.ID)
		if selector.bodyEstimateMS > int64(^uint64(0)>>1)-bodyEstimate {
			return nil, 0, errors.New("compile group body estimate overflows")
		}
		bodyEstimate += selector.bodyEstimateMS
	}
	return ids, bodyEstimate, nil
}

// finalizeCompileGroup 绑定 batch digest、GroupID 并执行最终结构校验。
func finalizeCompileGroup(group CompileGroup, affinityKey string) (CompileGroup, error) {
	var err error
	if len(group.BatchPlan) != 0 {
		group.BatchPlanDigest, err = CompileGroupBatchPlanDigest(group)
		if err != nil {
			return CompileGroup{}, fmt.Errorf("compile group %q batch plan digest: %w", affinityKey, err)
		}
	}
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		return CompileGroup{}, fmt.Errorf("compile group %q identity: %w", affinityKey, err)
	}
	if err := group.Validate(); err != nil {
		return CompileGroup{}, fmt.Errorf("compile group %q: %w", affinityKey, err)
	}
	return group, nil
}

// compileGroupBatchPlan 冻结确定性的 LPT selector 批次。K 是使“共享编译
// 加关键正文”达到目标的最小普通批次数，搜索上界只受 selector 数量约束；
// 即使最大 K 仍超目标，计划也保持有效并记录告警，不转为超时或失败。
func compileGroupBatchPlan(partition []compilePlanningSelector, compileEstimate int64, resourceClassID string) ([]CompileSelectorEstimate, []CompileGroupBatch, string, error) {
	if len(partition) == 0 {
		return nil, nil, "", errors.New("compile planning partition is empty")
	}
	estimates := compileGroupSelectorEstimates(partition)

	if partition[0].input.SemanticKey == CompileGroupSemanticGoBenchmark {
		return estimates, nil, "", nil
	}
	safe, exclusive := partitionCompileGroupSelectors(partition)
	maxBatches, err := compileGroupBatchCapacity(partition[0].input.PackageTarget, resourceClassID, len(safe))
	if err != nil {
		return nil, nil, "", err
	}
	if partition[0].input.PackageTarget == AtomicArchtestPackageTarget || partition[0].input.PackageTarget == AtomicSuperDolphinGatePackageTarget {
		// 每个有界 atomic compile group 只启动一个 test-binary batch，
		// 复用该组的 SSA/扫描状态；不同组由独立 ECI shard 并发执行。
		chosen, warning := chooseCompileGroupArchtestBatch(safe, compileEstimate)
		return estimates, appendCompileGroupExclusiveBatches(chosen, exclusive), warning, nil
	}
	chosen, warning := chooseCompileGroupSafeBatches(safe, compileEstimate, maxBatches)
	chosen = appendCompileGroupExclusiveBatches(chosen, exclusive)
	return estimates, chosen, warning, nil
}

// compileGroupSelectorEstimates 生成按 selector ID 排序的正文估时集合。
func compileGroupSelectorEstimates(partition []compilePlanningSelector) []CompileSelectorEstimate {
	estimates := make([]CompileSelectorEstimate, len(partition))
	for index, selector := range partition {
		estimates[index] = CompileSelectorEstimate{SelectorID: GateID(selector.planned.Workload.ID), BodyEstimateMS: selector.bodyEstimateMS}
	}
	sort.Slice(estimates, func(left, right int) bool { return estimates[left].SelectorID < estimates[right].SelectorID })
	return estimates
}

// partitionCompileGroupSelectors 分离普通批次与必须串行的 selector。
func partitionCompileGroupSelectors(partition []compilePlanningSelector) ([]compilePlanningSelector, []compilePlanningSelector) {
	safe := make([]compilePlanningSelector, 0, len(partition))
	exclusive := make([]compilePlanningSelector, 0)
	for _, selector := range partition {
		if compilePlanningSelectorExclusive(selector) {
			exclusive = append(exclusive, selector)
			continue
		}
		safe = append(safe, selector)
	}
	return safe, exclusive
}

// compileGroupBatchCapacity 返回 workload 驱动的普通批次搜索上界。
// 资源档位只校验 compile-group execution 身份，不得把 vCPU 当作全局批次上限；
// 每个 batch 仍复用同一 compile-group test binary。agent-terminal 额外要求
// TestMain 初始化的 rollback helper 只在一个进程中运行，因此保留其原子约束。
func compileGroupBatchCapacity(packageTarget, resourceClassID string, selectorCount int) (int, error) {
	if err := validateCompileGroupResourceClass(resourceClassID); err != nil {
		return 0, err
	}
	if selectorCount < 0 {
		return 0, errors.New("compile group batch capacity cannot be negative")
	}
	if packageTarget == AtomicAgentTerminalPackageTarget {
		return 1, nil
	}
	return selectorCount, nil
}

// chooseCompileGroupArchtestBatch 固定每个有界 archtest compile group 只启动
// 一个 test-binary 进程。超出目标时仍只返回结构化告警，绝不按 vCPU 复制进程
// 或终止执行；更大的 selector 集合已经在 bucket 层拆成独立 ECI shard。
func chooseCompileGroupArchtestBatch(selectors []compilePlanningSelector, compileEstimate int64) ([]CompileGroupBatch, string) {
	if len(selectors) == 0 {
		return nil, ""
	}
	chosen := deterministicCompileGroupBatches(selectors, 1)
	selectedCritical := compileEstimate + compileGroupCriticalPathMS(chosen)
	if selectedCritical <= CompileGroupBatchTargetMS {
		return chosen, ""
	}
	warning := fmt.Sprintf("critical_batch_plus_compile_ms=%d exceeds_target_ms=%d atomic_group_batch_limit=1 archtest_group_batch_limit=1", selectedCritical, CompileGroupBatchTargetMS)
	return chosen, warning
}

// chooseCompileGroupSafeBatches 选择达到目标的最小普通批次数，并记录超目标告警。
func chooseCompileGroupSafeBatches(selectors []compilePlanningSelector, compileEstimate int64, maxBatches int) ([]CompileGroupBatch, string) {
	if len(selectors) == 0 {
		return nil, ""
	}
	var selected []CompileGroupBatch
	selectedCritical := int64(0)
	for count := 1; count <= maxBatches; count++ {
		candidate := deterministicCompileGroupBatches(selectors, count)
		critical := compileEstimate + compileGroupCriticalPathMS(candidate)
		selected, selectedCritical = candidate, critical
		if critical <= CompileGroupBatchTargetMS {
			break
		}
	}
	if selectedCritical <= CompileGroupBatchTargetMS {
		return selected, ""
	}
	warning := fmt.Sprintf("critical_batch_plus_compile_ms=%d exceeds_target_ms=%d at_max_batches=%d", selectedCritical, CompileGroupBatchTargetMS, maxBatches)
	return selected, warning
}

// appendCompileGroupExclusiveBatches 将独占 selector 放入连续的串行 wave。
func appendCompileGroupExclusiveBatches(chosen []CompileGroupBatch, exclusive []compilePlanningSelector) []CompileGroupBatch {
	nextWave := 0
	for _, batch := range chosen {
		if batch.Wave >= nextWave {
			nextWave = batch.Wave + 1
		}
	}
	for _, selector := range exclusive {
		batchID := fmt.Sprintf("batch-%03d", len(chosen))
		chosen = append(chosen, CompileGroupBatch{BatchID: batchID, Wave: nextWave, SelectorIDs: []GateID{GateID(selector.planned.Workload.ID)}, EstimatedBodyMS: selector.bodyEstimateMS, Exclusive: true})
		nextWave++
	}
	return chosen
}

// compilePlanningSelectorExclusive 判断 selector 是否属于 codexapp 或 mcp-lsp 独占集合。
func compilePlanningSelectorExclusive(selector compilePlanningSelector) bool {
	return selector.targetKind == WorkloadTargetGoTest && (compileGroupSelectorIsCodexExclusive(selector.parent, selector.target) || compileGroupSelectorIsMcpLSPExclusive(selector.parent, selector.target))
}

// deterministicCompileGroupBatches 按正文估时降序和 canonical ID 平局规则进行
// deterministic critical-path-aware best-fit 分配；它不使用瞬时并发或随机状态。
func deterministicCompileGroupBatches(selectors []compilePlanningSelector, count int) []CompileGroupBatch {
	ordered := append([]compilePlanningSelector(nil), selectors...)
	sortCompilePlanningSelectors(ordered)
	if count <= 0 {
		return nil
	}
	bins := make([]dcpapBin, count)
	for _, selector := range ordered {
		item := dcpapItem{id: selector.planned.Workload.ID, durationMS: selector.bodyEstimateMS}
		index := chooseCompileBatchBin(bins, selector.bodyEstimateMS)
		bins[index].items = append(bins[index].items, item)
		bins[index].bodyMS += selector.bodyEstimateMS
	}
	canonical := dcpapCanonicalBins(bins)
	result := make([]CompileGroupBatch, 0, len(canonical))
	for index, current := range canonical {
		if len(current.items) == 0 {
			continue
		}
		ids := make([]GateID, len(current.items))
		for selectorIndex, item := range current.items {
			ids[selectorIndex] = GateID(item.id)
		}
		slices.Sort(ids)
		result = append(result, CompileGroupBatch{BatchID: fmt.Sprintf("batch-%03d", index), Wave: 0, SelectorIDs: ids, EstimatedBodyMS: current.bodyMS})
	}
	return result
}

// sortCompilePlanningSelectors 以正文估时降序和 selector ID 作为稳定平局键。
func sortCompilePlanningSelectors(selectors []compilePlanningSelector) {
	sort.SliceStable(selectors, func(left, right int) bool {
		if selectors[left].bodyEstimateMS != selectors[right].bodyEstimateMS {
			return selectors[left].bodyEstimateMS > selectors[right].bodyEstimateMS
		}
		return selectors[left].planned.Workload.ID < selectors[right].planned.Workload.ID
	})
}

// chooseCompileBatchBin 选择 best-fit 的编译批次，平局使用 canonical bin key。
func chooseCompileBatchBin(bins []dcpapBin, duration int64) int {
	index := leastLoadedDCPAPBin(bins)
	bestResidual := int64(^uint64(0) >> 1)
	for candidate := range bins {
		residual := bins[candidate].bodyMS + duration
		if residual < bestResidual || (residual == bestResidual && dcpapBinKey(bins[candidate]) < dcpapBinKey(bins[index])) {
			index, bestResidual = candidate, residual
		}
	}
	return index
}

// compileGroupCriticalPathMS 返回 body 的关键路径：每个 wave 取最大 batch，
// 再按 wave 顺序累加。exclusive batch 各自占串行 wave，因此自然累加；共享
// compile 由调用方只加一次，避免把 overhead 或 compile 成本重复计入 selector。
func compileGroupCriticalPathMS(batches []CompileGroupBatch) int64 {
	if len(batches) == 0 {
		return 0
	}
	maxByWave := make(map[int]int64)
	for _, batch := range batches {
		if batch.EstimatedBodyMS > maxByWave[batch.Wave] {
			maxByWave[batch.Wave] = batch.EstimatedBodyMS
		}
	}
	critical := int64(0)
	waves := make([]int, 0, len(maxByWave))
	for wave := range maxByWave {
		waves = append(waves, wave)
	}
	slices.Sort(waves)
	for _, wave := range waves {
		if maxByWave[wave] > int64(^uint64(0)>>1)-critical {
			return int64(^uint64(0) >> 1)
		}
		critical += maxByWave[wave]
	}
	return critical
}

// compileGroupCriticalDurationMS 是 planner/validator 共用的关键路径成本。
// wire EstimatedDurationMS 保留正文总和覆盖闭包；调度只支付一次 compile，再按 wave 取正文最大值。
func compileGroupCriticalDurationMS(group CompileGroup) int64 {
	if len(group.BatchPlan) == 0 {
		return group.EstimatedDurationMS
	}
	criticalBody := compileGroupCriticalPathMS(group.BatchPlan)
	if criticalBody > int64(^uint64(0)>>1)-group.CompileEstimateMS {
		return int64(^uint64(0) >> 1)
	}
	return group.CompileEstimateMS + criticalBody
}

// compileGroupResourceClass 校验组内 normal 档位一致性，或返回 calibration 固定资源身份。
func compileGroupResourceClass(partition []compilePlanningSelector, context PlanningContext) (string, error) {
	if len(partition) == 0 {
		return "", errors.New("compile planning partition is empty")
	}
	if context.Calibration {
		if err := cicontract.ValidateCalibrationResources(context.CalibrationResourceClassID, context.CalibrationResourceCPU, context.CalibrationResourceMemoryGiB); err != nil {
			return "", err
		}
		return context.CalibrationResourceClassID, nil
	}
	tier := partition[0].resourceTier
	if tier == 0 {
		return "", errors.New("normal compile planning partition has no resource tier")
	}
	for _, selector := range partition[1:] {
		if selector.resourceTier != tier {
			return "", errors.New("compile planning partition mixes normal resource tiers")
		}
	}
	return checkedNormalCompileResourceClass(tier)
}

func plannedWorkloads(selectors []compilePlanningSelector) []PlannedWorkload {
	workloads := make([]PlannedWorkload, len(selectors))
	for index, selector := range selectors {
		workloads[index] = selector.planned
	}
	return workloads
}

func sortCompilePlanningUnits(units []compilePlanningUnit) {
	sort.SliceStable(units, func(left, right int) bool {
		if units[left].costMS != units[right].costMS {
			return units[left].costMS > units[right].costMS
		}
		return units[left].sortID < units[right].sortID
	})
}

// distributeCompileUnitsForPlanningContext 按 normal 资源档位隔离分片，校准时使用固定档位。
func distributeCompileUnitsForPlanningContext(units []compilePlanningUnit, context PlanningContext) ([]ShardPlan, error) {
	if len(units) == 0 {
		return nil, errors.New("compile planning units are empty")
	}
	if context.Calibration {
		return distributeCompileUnitsWithinTarget(units, context)
	}
	tiers := make([][]compilePlanningUnit, int(cicontract.WorkloadResourceTierSlow))
	for _, unit := range units {
		if unit.tier < cicontract.WorkloadResourceTierFast || unit.tier > cicontract.WorkloadResourceTierSlow {
			return nil, fmt.Errorf("compile planning unit %q has unsupported resource tier %d", unit.sortID, unit.tier)
		}
		tiers[int(unit.tier)-1] = append(tiers[int(unit.tier)-1], unit)
	}
	shards := make([]ShardPlan, 0, len(units))
	for _, tierUnits := range tiers {
		if len(tierUnits) == 0 {
			continue
		}
		part, err := distributeCompileUnitsWithinTarget(tierUnits, context)
		if err != nil {
			return nil, err
		}
		for _, shard := range part {
			shard.Index = len(shards)
			shards = append(shards, shard)
		}
	}
	return shards, nil
}

// distributeCompileUnitsWithinTarget 在目标时长内寻找满足编译 affinity 约束的最小分片数。
func distributeCompileUnitsWithinTarget(units []compilePlanningUnit, context PlanningContext) ([]ShardPlan, error) {
	target, err := context.EffectiveTargetDurationMS()
	if err != nil {
		return nil, err
	}
	shards, err := provenCompileUnitPacking(units, target)
	if err != nil {
		return nil, fmt.Errorf("compile packing proof: %w", err)
	}
	return shards, nil
}

// distributeCompileUnits 按 affinity、artifact、串行政策和资源档确定性分配 compile units。
func distributeCompileUnits(units []compilePlanningUnit, count int, target int64) ([]ShardPlan, bool) {
	shards := make([]ShardPlan, count)
	affinities := make([]map[string]struct{}, count)
	artifactKeys := make([]map[string]struct{}, count)
	serialEligible := make([]bool, count)
	resourceClassIDs := make([]string, count)
	for index := range shards {
		shards[index].Index = index
		affinities[index] = make(map[string]struct{})
		artifactKeys[index] = make(map[string]struct{})
	}
	for _, unit := range units {
		index, ok := compileUnitShardIndexForCompileGroup(shards, affinities, artifactKeys, serialEligible, resourceClassIDs, unit, target)
		if !ok {
			return nil, false
		}
		shards[index].Workloads = append(shards[index].Workloads, unit.workloads...)
		if unit.group != nil {
			shards[index].CompileGroupIDs = append(shards[index].CompileGroupIDs, unit.group.GroupID)
			artifactKey, err := CompileArtifactKey(*unit.group)
			if err != nil {
				return nil, false
			}
			artifactKeys[index][artifactKey] = struct{}{}
			serialEligible[index] = serialEligible[index] || CompileGroupSerialPackingEligible(*unit.group)
			resourceClassIDs[index] = unit.group.ResourceClassID
		}
		if unit.costMS > int64(^uint64(0)>>1)-shards[index].EstimatedDurationMS {
			return nil, false
		}
		shards[index].EstimatedDurationMS += unit.costMS
		affinities[index][unit.affinityKey] = struct{}{}
	}
	for index := range shards {
		slices.Sort(shards[index].CompileGroupIDs)
	}
	return shards, true
}

// compileUnitShardIndexForCompileGroup 选择尚未占用同一编译 artifact 且当前负载最小的分片。
// 首期仅允许 CompileGroupSerialPackingEligible 显式放行的普通 Go test 组共箱；
// 特殊组、race、benchmark 和 exclusive wave 保持一组一 shard 的硬约束。
func compileUnitShardIndexForCompileGroup(shards []ShardPlan, affinities, artifactKeys []map[string]struct{}, serialEligible []bool, resourceClassIDs []string, unit compilePlanningUnit, target int64) (int, bool) {
	least := -1
	bestExcess := int64(^uint64(0) >> 1)
	bestResidual := int64(^uint64(0) >> 1)
	for index := range shards {
		if !compileUnitCanShareShard(shards[index], affinities[index], artifactKeys[index], serialEligible[index], resourceClassIDs[index], unit) {
			continue
		}
		excess, residual := compileUnitPlacementScore(shards[index].EstimatedDurationMS, unit.costMS, target)
		if least < 0 || excess < bestExcess || (excess == bestExcess && (residual < bestResidual || (residual == bestResidual && shards[index].Index < shards[least].Index))) {
			least, bestExcess, bestResidual = index, excess, residual
		}
	}
	if least >= 0 {
		return least, true
	}
	return 0, false
}

// compileUnitCanShareShard 施加 affinity、artifact 唯一性与显式共箱政策约束。
// 共箱的多个 compile group 还必须保持同一 ResourceClassID。
func compileUnitCanShareShard(shard ShardPlan, affinities, artifactKeys map[string]struct{}, serialEligible bool, resourceClassID string, unit compilePlanningUnit) bool {
	if _, duplicate := affinities[unit.affinityKey]; duplicate {
		return false
	}
	if unit.group == nil {
		return len(shard.CompileGroupIDs) == 0
	}
	artifactKey, err := CompileArtifactKey(*unit.group)
	if err != nil {
		return false
	}
	if _, duplicate := artifactKeys[artifactKey]; duplicate {
		return false
	}
	if len(shard.CompileGroupIDs) > 0 && unit.group.ResourceClassID != resourceClassID {
		return false
	}
	if len(shard.Workloads) == 0 {
		return true
	}
	return len(shard.CompileGroupIDs) > 0 && CompileGroupSerialPackingEligible(*unit.group) && serialEligible
}

// compileUnitPlacementScore 以 target excess 优先、再以剩余容量稳定平局。
func compileUnitPlacementScore(current, cost, target int64) (int64, int64) {
	projected := current
	if cost > int64(^uint64(0)>>1)-current {
		projected = int64(^uint64(0) >> 1)
	} else {
		projected += cost
	}
	residual := target - projected
	if residual < 0 {
		return -residual, int64(^uint64(0) >> 1)
	}
	return 0, residual
}

// compileShardsMeetTarget 判断编译组或不可再拆 workload 是否满足目标时长。
func compileShardsMeetTarget(shards []ShardPlan, target int64, units []compilePlanningUnit) bool {
	groupWorkloadCount := make(map[string]int)
	for _, unit := range units {
		if unit.group != nil {
			groupWorkloadCount[unit.group.GroupID] = len(unit.workloads)
		}
	}
	for _, shard := range shards {
		if shard.EstimatedDurationMS <= target {
			continue
		}
		if len(shard.CompileGroupIDs) == 1 && len(shard.Workloads) == groupWorkloadCount[shard.CompileGroupIDs[0]] {
			continue
		}
		if len(shard.Workloads) == 1 && len(shard.CompileGroupIDs) == 0 && shard.Workloads[0].EstimatedDurationMS > target {
			continue
		}
		return false
	}
	return true
}

func compileInputFromPlanGroup(group CompileGroup) (GateID, CompileGroupInput, error) {
	if len(group.WorkloadIDs) == 0 {
		return "", CompileGroupInput{}, errors.New("compile group workload IDs are empty")
	}
	parent, _, _, _, err := ParseWorkloadID(string(group.WorkloadIDs[0]))
	if err != nil {
		return "", CompileGroupInput{}, err
	}
	return parent, CompileGroupInput{PackageTarget: group.PackageTarget, SemanticKey: group.SemanticKey, SharedInputDigest: group.SharedInputDigest, ProfileDigest: group.ProfileDigest}, nil
}

// compileInputsFromPlan 从持久化计划重建 Validate 重规划所需的严格 builder 输入。
func compileInputsFromPlan(plan WorkloadExecutionPlan) (map[GateID]CompileGroupInput, error) {
	inputs := make(map[GateID]CompileGroupInput)
	for _, group := range plan.CompileGroups {
		_, input, err := compileInputFromPlanGroup(group)
		if err != nil {
			return nil, err
		}
		for _, workloadID := range group.WorkloadIDs {
			key := GateID(workloadID)
			if previous, ok := inputs[key]; ok && previous != input {
				return nil, fmt.Errorf("compile input for selector %q is inconsistent", workloadID)
			}
			inputs[key] = input
		}
	}
	return inputs, nil
}

// compileGroupAffinityFromShardIDs 校验 compile group 的 canonical 顺序、重复
// artifact 与首期显式共箱政策；资源档位一致性由外层 plan validator 负责。
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
	if !canonical {
		return fmt.Errorf("shard %d compile groups are not canonical sorted", shard.Index)
	}
	if !compileGroupsCoverShardWorkloads(groups, shard) {
		extra, missing := compileGroupShardCoverageMismatch(groups, shard)
		return fmt.Errorf("shard %d mixes grouped and ordinary workloads: extra=%v missing=%v", shard.Index, extra, missing)
	}
	if len(shard.CompileGroupIDs) > 1 {
		for _, groupID := range shard.CompileGroupIDs {
			if !CompileGroupSerialPackingEligible(groups[groupID]) {
				return fmt.Errorf("shard %d compile group %q is not eligible for serial packing", shard.Index, groupID)
			}
		}
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
