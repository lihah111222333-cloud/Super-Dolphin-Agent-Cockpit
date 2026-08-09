package gate

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

type durationSampleIndexKey struct {
	workloadID        string
	commandDigest     string
	inputDigest       string
	executionMode     string
	resourceClassID   string
	resourceCPU       float64
	resourceMemoryGiB float64
}

type durationSampleAggregate struct {
	successTotalMS     int64
	successCount       int64
	maxFailureDuration int64
}

// DurationSampleIndex 是单个账本 generation 和执行环境的只读时长索引。
type DurationSampleIndex struct {
	context PlanningContext
	buckets map[durationSampleIndexKey]durationSampleAggregate
	// CompileTimingIndex 是 owner 资源选择使用的只读 compile-group 历史投影。
	// 它与 workload 索引并列保存，确保 planner snapshot 不会混入不同规划上下文或 generation 的耗时行。
	CompileTimingIndex CompileTimingIndex
}

// BuildDurationSampleIndex 对当前 generation 只扫描一次账本，并隔离不同比较环境。
func BuildDurationSampleIndex(ledger DurationLedger, context PlanningContext) (DurationSampleIndex, error) {
	if err := ValidateDurationLedger(ledger); err != nil {
		return DurationSampleIndex{}, err
	}
	resolved, err := ResolvePlanningContext(context, ledger)
	if err != nil {
		return DurationSampleIndex{}, err
	}
	context = resolved
	if err := validateDurationEnvironment(context.Platform, context.Runner, context.Toolchain); err != nil {
		return DurationSampleIndex{}, err
	}
	index := DurationSampleIndex{
		context: context,
		buckets: make(map[durationSampleIndexKey]durationSampleAggregate),
	}
	compileTimingIndex, err := BuildCompileTimingIndex(nil)
	if err != nil {
		return DurationSampleIndex{}, err
	}
	index.CompileTimingIndex = compileTimingIndex
	const maximumInt64 = int64(^uint64(0) >> 1)
	for _, sample := range ledger.Samples {
		if err := index.addSample(sample, maximumInt64); err != nil {
			return DurationSampleIndex{}, err
		}
	}
	return index, nil
}

// addSample 按当前比较环境把一条样本合并到确定性的 workload 聚合中。
func (index DurationSampleIndex) addSample(sample DurationSample, maximumInt64 int64) error {
	if sample.Bucket.Platform != index.context.Platform || sample.Bucket.Runner != index.context.Runner || sample.Bucket.Toolchain != index.context.Toolchain {
		return nil
	}
	expectedMode := DurationExecutionModeNormal
	if index.context.Calibration {
		expectedMode = DurationExecutionModeCalibration
	}
	if sample.Bucket.ExecutionMode != expectedMode {
		return nil
	}
	if sample.DurationMS <= 0 {
		return fmt.Errorf("duration sample for workload %q must be positive", sample.Bucket.WorkloadID)
	}
	key := durationSampleIndexKey{
		workloadID:        sample.Bucket.WorkloadID,
		commandDigest:     sample.Bucket.CommandDigest,
		inputDigest:       sample.Bucket.InputDigest,
		executionMode:     sample.Bucket.ExecutionMode,
		resourceClassID:   sample.Bucket.ResourceClassID,
		resourceCPU:       sample.Bucket.ResourceCPU,
		resourceMemoryGiB: sample.Bucket.ResourceMemoryGiB,
	}
	aggregate := index.buckets[key]
	if !sample.Succeeded {
		if sample.DurationMS > aggregate.maxFailureDuration {
			aggregate.maxFailureDuration = sample.DurationMS
		}
		index.buckets[key] = aggregate
		return nil
	}
	if sample.DurationMS > maximumInt64-aggregate.successTotalMS {
		return fmt.Errorf("duration estimate overflows for workload %q", sample.Bucket.WorkloadID)
	}
	aggregate.successTotalMS += sample.DurationMS
	aggregate.successCount++
	index.buckets[key] = aggregate
	return nil
}

// DurationSampleIndexFromSnapshot 优先复用 SQLite 聚合索引，并拒绝环境不一致的快照。
func DurationSampleIndexFromSnapshot(snapshot DurationLedgerSnapshot, context PlanningContext) (DurationSampleIndex, error) {
	resolved, err := ResolvePlanningContext(context, snapshot.Ledger)
	if err != nil {
		return DurationSampleIndex{}, err
	}
	context = resolved
	if snapshot.SampleIndex == nil {
		index, err := BuildDurationSampleIndex(snapshot.Ledger, context)
		if err != nil {
			return DurationSampleIndex{}, err
		}
		if snapshot.CompileTimingIndex != nil {
			index.CompileTimingIndex = *snapshot.CompileTimingIndex
		}
		return index, nil
	}
	index := *snapshot.SampleIndex
	if index.context != context {
		return DurationSampleIndex{}, errors.New("duration sample index planning context does not match")
	}
	if index.buckets == nil {
		return DurationSampleIndex{}, errors.New("duration sample index buckets are missing")
	}
	if snapshot.CompileTimingIndex != nil {
		index.CompileTimingIndex = *snapshot.CompileTimingIndex
	}
	return index, nil
}

// HasComparableSuccessfulDurationSample 判断索引中是否已有同命令成功样本。
func (index DurationSampleIndex) HasComparableSuccessfulDurationSample(workload Workload) bool {
	if index.context.Calibration {
		return index.hasComparableCalibrationSample(workload)
	}
	return index.hasComparableNormalSample(workload)
}

// hasComparableCalibrationSample 检查固定校准资源的成功样本。
func (index DurationSampleIndex) hasComparableCalibrationSample(workload Workload) bool {
	cpu, memoryGiB, err := index.calibrationResource()
	if err != nil {
		return false
	}
	aggregate, _, found, err := index.aggregateForResource(workload, DurationExecutionModeCalibration, cpu, memoryGiB)
	return err == nil && found && aggregate.successCount > 0
}

// hasComparableNormalSample 检查 bootstrap 所在 normal 档位的成功样本。
func (index DurationSampleIndex) hasComparableNormalSample(workload Workload) bool {
	tier, err := cicontract.ClassifyWorkloadResourceDuration(workload.BootstrapEstimateMS)
	if err != nil {
		return false
	}
	cpu, memoryGiB, err := normalResourceForTier(tier)
	if err != nil {
		return false
	}
	aggregate, _, found, err := index.aggregateForResource(workload, DurationExecutionModeNormal, cpu, memoryGiB)
	return err == nil && found && aggregate.successCount > 0
}

// EstimateWorkloadDurationMS 使用预聚合成功样本估算单个 workload。
func (index DurationSampleIndex) EstimateWorkloadDurationMS(workload Workload) (int64, error) {
	estimate, _, err := index.estimateWorkloadDuration(workload)
	return estimate, err
}

type durationSampleResource struct {
	classID     string
	inputDigest string
	cpu         float64
	memoryGiB   float64
}

// normalResourceForTier 将分类结果映射为固定 normal 资源，未知分类直接报错。
func normalResourceForTier(tier cicontract.WorkloadResourceTier) (float64, float64, error) {
	switch tier {
	case cicontract.WorkloadResourceTierFast:
		return 2, 4, nil
	case cicontract.WorkloadResourceTierMedium:
		return 4, 8, nil
	case cicontract.WorkloadResourceTierSlow:
		return 8, 16, nil
	default:
		return 0, 0, fmt.Errorf("unsupported workload resource tier %q", tier)
	}
}

// normalResourceTierForTuple 将计划持久化的固定 CPU/内存 tuple 映射为档位。
// 任何非协议资源都直接失败，避免 owner hint 静默改变资源身份。
func normalResourceTierForTuple(cpu, memoryGiB float64) (cicontract.WorkloadResourceTier, error) {
	switch {
	case cpu == 2 && memoryGiB == 4:
		return cicontract.WorkloadResourceTierFast, nil
	case cpu == 4 && memoryGiB == 8:
		return cicontract.WorkloadResourceTierMedium, nil
	case cpu == 8 && memoryGiB == 16:
		return cicontract.WorkloadResourceTierSlow, nil
	default:
		return 0, fmt.Errorf("unsupported resource %.gC/%.gGiB", cpu, memoryGiB)
	}
}

// normalResourceTierRank 返回仅用于单次规划收敛的显式策略顺序，
// 不从枚举值推断顺序。
func normalResourceTierRank(tier cicontract.WorkloadResourceTier) (int, error) {
	switch tier {
	case cicontract.WorkloadResourceTierFast:
		return 1, nil
	case cicontract.WorkloadResourceTierMedium:
		return 2, nil
	case cicontract.WorkloadResourceTierSlow:
		return 3, nil
	default:
		return 0, fmt.Errorf("unsupported workload resource tier %q", tier)
	}
}

// normalResourceTierTransition 解析一次耗时样本，禁止同一规划轮次中的降档撤销此前升档。
func normalResourceTierTransition(current cicontract.WorkloadResourceTier, estimateMS int64) (cicontract.WorkloadResourceTier, bool, error) {
	next, err := cicontract.ClassifyWorkloadResourceDuration(estimateMS)
	if err != nil {
		return 0, false, err
	}
	currentRank, err := normalResourceTierRank(current)
	if err != nil {
		return 0, false, err
	}
	nextRank, err := normalResourceTierRank(next)
	if err != nil {
		return 0, false, err
	}
	if nextRank <= currentRank {
		return current, true, nil
	}
	return next, false, nil
}

// aggregateForResource 按 workload、输入、模式和资源查找唯一样本聚合。
func (index DurationSampleIndex) aggregateForResource(workload Workload, executionMode string, cpu, memoryGiB float64) (durationSampleAggregate, durationSampleResource, bool, error) {
	var (
		aggregate durationSampleAggregate
		resource  durationSampleResource
		found     bool
	)
	for key, candidate := range index.buckets {
		if !matchesDurationResourceKey(key, workload, executionMode, cpu, memoryGiB) {
			continue
		}
		if durationResourceIdentityAmbiguous(found, resource, key) {
			return durationSampleAggregate{}, durationSampleResource{}, false,
				fmt.Errorf("duration samples for workload %q have ambiguous input/resource identity at %.0fC/%.0fGiB", workload.ID, cpu, memoryGiB)
		}
		aggregate = candidate
		resource = durationSampleResource{classID: key.resourceClassID, inputDigest: key.inputDigest, cpu: cpu, memoryGiB: memoryGiB}
		found = true
	}
	return aggregate, resource, found, nil
}

// matchesDurationResourceKey 判断索引键是否完全匹配当前 workload 资源。
func matchesDurationResourceKey(
	key durationSampleIndexKey,
	workload Workload,
	executionMode string,
	cpu, memoryGiB float64,
) bool {
	return key.workloadID == workload.ID && key.commandDigest == workload.CommandDigest &&
		(workload.InputDigest == "" || key.inputDigest == workload.InputDigest) &&
		key.executionMode == executionMode && key.resourceCPU == cpu && key.resourceMemoryGiB == memoryGiB
}

func durationResourceIdentityAmbiguous(found bool, resource durationSampleResource, key durationSampleIndexKey) bool {
	return found && (resource.classID != key.resourceClassID || resource.inputDigest != key.inputDigest)
}

// estimateWorkloadDuration 按执行模式选择固定资源或 normal 档位估算路径。
func (index DurationSampleIndex) estimateWorkloadDuration(workload Workload) (int64, durationSampleResource, error) {
	targetDurationMS, err := index.context.EffectiveTargetDurationMS()
	if err != nil {
		return 0, durationSampleResource{}, err
	}
	if index.context.Calibration {
		return index.estimateCalibrationWorkload(workload, targetDurationMS)
	}
	return index.estimateNormalWorkload(workload, targetDurationMS)
}

// estimateCalibrationWorkload 只使用当前校准 identity 对应的 4C/8GiB 样本。
func (index DurationSampleIndex) estimateCalibrationWorkload(workload Workload, targetDurationMS int64) (int64, durationSampleResource, error) {
	cpu, memoryGiB, err := index.calibrationResource()
	if err != nil {
		return 0, durationSampleResource{}, err
	}
	aggregate, resource, found, err := index.aggregateForResource(workload, DurationExecutionModeCalibration, cpu, memoryGiB)
	if err != nil {
		return 0, durationSampleResource{}, err
	}
	if !found || aggregate.successCount == 0 {
		return workload.BootstrapEstimateMS, durationSampleResource{classID: index.context.CalibrationResourceClassID, cpu: cpu, memoryGiB: memoryGiB}, nil
	}
	return aggregate.successTotalMS / aggregate.successCount, resource, nil
}

func (index DurationSampleIndex) calibrationResource() (float64, float64, error) {
	if !index.context.Calibration {
		return 0, 0, errors.New("duration sample index is not in calibration mode")
	}
	if err := cicontract.ValidateCalibrationResources(index.context.CalibrationResourceClassID, index.context.CalibrationResourceCPU, index.context.CalibrationResourceMemoryGiB); err != nil {
		return 0, 0, fmt.Errorf("calibration resource identity: %w", err)
	}
	return index.context.CalibrationResourceCPU, index.context.CalibrationResourceMemoryGiB, nil
}

// estimateNormalWorkload 在 normal 档位内执行有界固定点解析。
func (index DurationSampleIndex) estimateNormalWorkload(workload Workload, targetDurationMS int64) (int64, durationSampleResource, error) {
	if _, err := cicontract.ClassifyWorkloadResourceDuration(workload.BootstrapEstimateMS); err != nil {
		return 0, durationSampleResource{}, err
	}
	tier := cicontract.WorkloadResourceTierFast
	visited := map[cicontract.WorkloadResourceTier]struct{}{}
	carriedEstimateMS := workload.BootstrapEstimateMS
	for range 3 {
		if _, exists := visited[tier]; exists {
			return 0, durationSampleResource{}, fmt.Errorf("normal resource fixed point oscillates for workload %q", workload.ID)
		}
		visited[tier] = struct{}{}
		estimate, resource, nextTier, done, err := index.resolveNormalTierStep(workload, tier, targetDurationMS, carriedEstimateMS)
		if err != nil {
			return 0, durationSampleResource{}, err
		}
		if done {
			return estimate, resource, nil
		}
		carriedEstimateMS = estimate
		tier = nextTier
	}
	return 0, durationSampleResource{}, fmt.Errorf("normal resource fixed point did not converge for workload %q", workload.ID)
}

// resolveNormalTierStep 读取一个 normal 档位并计算下一档位。
func (index DurationSampleIndex) resolveNormalTierStep(workload Workload, tier cicontract.WorkloadResourceTier, targetDurationMS int64, carriedEstimateMS int64) (int64, durationSampleResource, cicontract.WorkloadResourceTier, bool, error) {
	cpu, memoryGiB, err := normalResourceForTier(tier)
	if err != nil {
		return 0, durationSampleResource{}, tier, false, err
	}
	aggregate, resource, found, err := index.aggregateForResource(workload, DurationExecutionModeNormal, cpu, memoryGiB)
	if err != nil {
		return 0, durationSampleResource{}, tier, false, err
	}
	if !found || aggregate.successCount == 0 {
		if carriedEstimateMS <= 0 {
			return 0, durationSampleResource{}, tier, false,
				fmt.Errorf("normal resource fixed point for workload %q has invalid carried estimate %dms", workload.ID, carriedEstimateMS)
		}
		// 首次升档不可能已有新档样本；保留上一档权威实测估值选择新资源，不伪造样本。
		return carriedEstimateMS, durationSampleResource{cpu: cpu, memoryGiB: memoryGiB}, tier, true, nil
	}
	estimate := aggregate.successTotalMS / aggregate.successCount
	nextTier, done, err := normalResourceTierTransition(tier, estimate)
	if err != nil {
		return 0, durationSampleResource{}, tier, false, err
	}
	if done {
		// 单次资源规划只允许升档；更高档位的更快样本不能在同一 fixed-point 降档。
		// 经过最小样本数验证的降档由独立 downsizing 策略负责。
		return estimate, resource, tier, true, nil
	}
	return estimate, resource, nextTier, false, nil
}

// GoTestDurationMSAtResource 返回指定父 workload 资源档位的精确测试体均值。
//
// 资源档位由调用方（通常是已持久化的 owner resource）显式提供；这里
// 不再根据测试体的 bootstrap 或当前估时重新分类，避免在 compile-group
// 规划阶段把 selector 体时长偷偷升档。selector body 直接按规范的合成
// target identity 查询，不读取父 workload 聚合；这样 normal exact selector
// 不会被 calibration-only 的父样本或缺少 normal 父样本误导。
func (index DurationSampleIndex) GoTestDurationMSAtResource(parent Workload, testName string, cpu, memoryGiB float64) (int64, bool) {
	if !validGoTestDurationRequest(parent, testName, cpu, memoryGiB) {
		return 0, false
	}
	mode := index.goTestDurationMode()
	workload := goTestDurationWorkload(parent, testName, parent.InputDigest)
	aggregate, _, found, err := index.aggregateForResource(workload, mode, cpu, memoryGiB)
	if err != nil || !found || aggregate.successCount == 0 {
		return 0, false
	}
	return aggregate.successTotalMS / aggregate.successCount, true
}

// validGoTestDurationRequest 校验 selector body 精确资源查询的输入。
func validGoTestDurationRequest(parent Workload, testName string, cpu, memoryGiB float64) bool {
	return strings.TrimSpace(parent.ID) != "" && isSHA256Digest(parent.CommandDigest) &&
		isPrefixedSHA256Digest(parent.InputDigest) && strings.TrimSpace(testName) != "" && cpu > 0 && memoryGiB > 0
}

// goTestDurationMode 返回当前规划上下文的 workload 执行模式。
func (index DurationSampleIndex) goTestDurationMode() string {
	if index.context.Calibration {
		return DurationExecutionModeCalibration
	}
	return DurationExecutionModeNormal
}

// goTestDurationWorkload 构造带父输入 digest 的 selector body workload。
func goTestDurationWorkload(parent Workload, testName, inputDigest string) Workload {
	return Workload{
		ID:                  GoTestDurationWorkloadID(parent.ID, testName),
		CommandDigest:       GoTestDurationCommandDigest(parent.CommandDigest, testName),
		InputDigest:         inputDigest,
		BootstrapEstimateMS: 1,
	}
}
