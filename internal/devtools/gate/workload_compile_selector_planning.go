package gate

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// compileOwnerHintsFromSet 解析可选 owner hint 集合并拒绝多个来源造成的歧义。
func compileOwnerHintsFromSet(hintSets ...CompileOwnerHints) (CompileOwnerHints, error) {
	if len(hintSets) > 1 {
		return nil, errors.New("compile owner hint set is ambiguous")
	}
	if len(hintSets) == 1 {
		return hintSets[0], nil
	}
	return nil, nil
}

// appendCompilePlanningItem 按编译输入和 canonical normal 资源档位把 workload 放入分桶。
func appendCompilePlanningItem(item PlannedWorkload, index DurationSampleIndex, compileInputs map[GateID]CompileGroupInput, order map[string]int, buckets map[compilePlanningBucketKey]*compilePlanningBucket, units *[]compilePlanningUnit, hintSets ...CompileOwnerHints) error {
	hints, err := compileOwnerHintsFromSet(hintSets...)
	if err != nil {
		return err
	}
	selector, groupable, err := compilePlanningSelectorFor(item, index, compileInputs, order, hints)
	if err != nil {
		return err
	}
	if !groupable {
		return appendOrdinaryCompilePlanningItem(item, index.context, units)
	}
	return appendCompileSelectorToBucket(selector, buckets)
}

// appendOrdinaryCompilePlanningItem 将不可分组 workload 追加为普通规划单元。
func appendOrdinaryCompilePlanningItem(item PlannedWorkload, context PlanningContext, units *[]compilePlanningUnit) error {
	unit, err := ordinaryCompilePlanningUnit(item, context)
	if err != nil {
		return err
	}
	*units = append(*units, unit)
	return nil
}

// appendCompileSelectorToBucket 按 affinity 与资源档位加入 compile planning bucket。
func appendCompileSelectorToBucket(selector compilePlanningSelector, buckets map[compilePlanningBucketKey]*compilePlanningBucket) error {
	affinityKey, err := compileInputAffinityKey(selector.input)
	if err != nil {
		return err
	}
	bucketKey := compilePlanningBucketKey{affinityKey: affinityKey, resourceTier: selector.resourceTier}
	bucket := buckets[bucketKey]
	if bucket == nil {
		bucket = &compilePlanningBucket{affinityKey: affinityKey, resourceTier: selector.resourceTier, parent: selector.parent, targetKind: selector.targetKind}
		buckets[bucketKey] = bucket
	} else if bucket.parent != selector.parent || bucket.targetKind != selector.targetKind || bucket.resourceTier != selector.resourceTier {
		return fmt.Errorf("compile artifact %q mixes execution semantics", affinityKey)
	}
	bucket.selectors = append(bucket.selectors, selector)
	return nil
}

// compilePlanningSelectorFor 从 exact workload 解析 selector、编译输入和 canonical 估时档位。
func compilePlanningSelectorFor(item PlannedWorkload, index DurationSampleIndex, compileInputs map[GateID]CompileGroupInput, order map[string]int, hintSets ...CompileOwnerHints) (compilePlanningSelector, bool, error) {
	hints, err := compileOwnerHintsFromSet(hintSets...)
	if err != nil {
		return compilePlanningSelector{}, false, err
	}
	parent, kind, target, groupable, err := parseCompilePlanningTarget(item)
	if err != nil {
		return compilePlanningSelector{}, false, err
	}
	if !groupable {
		return compilePlanningSelector{}, false, nil
	}
	input, err := compilePlanningInput(item, compileInputs, target, parent, kind)
	if err != nil {
		return compilePlanningSelector{}, false, err
	}
	bodyEstimate, resourceTier, compileEstimate, err := compilePlanningEstimates(item, parent, kind, target, input, index, hints)
	if err != nil {
		return compilePlanningSelector{}, false, err
	}
	return compilePlanningSelector{
		planned: item, parent: parent, targetKind: kind, target: target, input: input,
		bodyEstimateMS: bodyEstimate, compileEstimateMS: compileEstimate,
		canonicalOrder: order[item.Workload.ID], resourceTier: resourceTier,
	}, true, nil
}

// parseCompilePlanningTarget 解析 workload 的 exact Go selector 目标，非分组项返回 false。
func parseCompilePlanningTarget(item PlannedWorkload) (GateID, WorkloadTargetKind, GoTestTarget, bool, error) {
	parent, kind, payload, targeted, err := ParseWorkloadID(item.Workload.ID)
	if err != nil || !targeted || !compileGroupTargetKind(kind) {
		return GateID(""), WorkloadTargetKind(""), GoTestTarget{}, false, nil
	}
	target, err := parseCompileGroupTarget(kind, payload)
	if err != nil {
		return GateID(""), WorkloadTargetKind(""), GoTestTarget{}, false, fmt.Errorf("parse compile selector %q: %w", item.Workload.ID, err)
	}
	return parent, kind, target, true, nil
}

// compilePlanningInput 读取并校验 selector 对应的冻结 compile input。
func compilePlanningInput(item PlannedWorkload, compileInputs map[GateID]CompileGroupInput, target GoTestTarget, parent GateID, kind WorkloadTargetKind) (CompileGroupInput, error) {
	input, ok := compileInputForSelector(item.Workload.ID, compileInputs)
	if !ok {
		return CompileGroupInput{}, fmt.Errorf("compile input is missing for exact Go selector %q", item.Workload.ID)
	}
	if err := validateCompilePlanningSelectorInput(item.Workload.ID, input, target, parent, kind); err != nil {
		return CompileGroupInput{}, err
	}
	return input, nil
}

// compilePlanningEstimates 读取 selector body、资源档位和共享 compile 估值。
func compilePlanningEstimates(item PlannedWorkload, parent GateID, kind WorkloadTargetKind, target GoTestTarget, input CompileGroupInput, index DurationSampleIndex, hints CompileOwnerHints) (int64, cicontract.WorkloadResourceTier, int64, error) {
	bodyEstimate, err := selectorBodyEstimate(item, parent, kind, target, index)
	if err != nil {
		return 0, 0, 0, err
	}
	resourceTier, err := canonicalCompileResourceTier(item, index.context)
	if err != nil {
		return 0, 0, 0, err
	}
	compileEstimate, err := selectorCompileEstimateFor(item, input, resourceTier, index, hints)
	if err != nil {
		return 0, 0, 0, err
	}
	return bodyEstimate, resourceTier, compileEstimate, nil
}

// validateCompilePlanningSelectorInput 校验 selector 的 package 与 semantic input。
func validateCompilePlanningSelectorInput(workloadID string, input CompileGroupInput, target GoTestTarget, parent GateID, kind WorkloadTargetKind) error {
	if err := input.Validate(); err != nil {
		return fmt.Errorf("compile input for %q: %w", workloadID, err)
	}
	if input.PackageTarget != target.Package {
		return fmt.Errorf("compile input package for %q does not match target package", workloadID)
	}
	expectedSemantic, err := CompileGroupSemanticKey(kind, parent == GateIDBackendTestGuardWithRace)
	if err != nil {
		return fmt.Errorf("compile semantic for %q: %w", workloadID, err)
	}
	if input.SemanticKey != expectedSemantic {
		return fmt.Errorf("compile input semantic for %q does not match canonical selector semantics", workloadID)
	}
	return nil
}

// compileGroupTargetKind 判断 workload target 是否属于可共享 compile 的 Go selector。
func compileGroupTargetKind(kind WorkloadTargetKind) bool {
	return kind == WorkloadTargetGoTest || kind == WorkloadTargetGoBenchmark
}

// parseCompileGroupTarget 解析 go test 或 go benchmark 的目标 payload。
func parseCompileGroupTarget(kind WorkloadTargetKind, payload string) (GoTestTarget, error) {
	if kind == WorkloadTargetGoBenchmark {
		return ParseGoBenchmarkTarget(payload)
	}
	return ParseGoTestTarget(payload)
}

// compileInputForSelector 读取 exact selector 对应的冻结 compile input。
func compileInputForSelector(id string, inputs map[GateID]CompileGroupInput) (CompileGroupInput, bool) {
	input, ok := inputs[GateID(id)]
	return input, ok
}

// canonicalCompileResourceTier 读取 PlannedWorkload 已冻结的 normal 资源档位。
func canonicalCompileResourceTier(item PlannedWorkload, context PlanningContext) (cicontract.WorkloadResourceTier, error) {
	if context.Calibration {
		return 0, nil
	}
	tier, err := plannedWorkloadResourceTier(item)
	if err != nil {
		return 0, fmt.Errorf("read workload %q persisted resource tier: %w", item.Workload.ID, err)
	}
	return tier, nil
}

// normalCompileResourceClass 将 normal 资源档位映射为持久化 class ID。
func normalCompileResourceClass(tier cicontract.WorkloadResourceTier) string {
	switch tier {
	case cicontract.WorkloadResourceTierFast:
		return "small"
	case cicontract.WorkloadResourceTierMedium:
		return "medium"
	case cicontract.WorkloadResourceTierSlow:
		return "maximum"
	default:
		return ""
	}
}

// checkedNormalCompileResourceClass 校验 normal 档位并返回 class ID。
func checkedNormalCompileResourceClass(tier cicontract.WorkloadResourceTier) (string, error) {
	classID := normalCompileResourceClass(tier)
	if classID == "" {
		return "", fmt.Errorf("unsupported normal workload resource tier %d", tier)
	}
	return classID, nil
}

// compileInputAffinityKey 生成 compile input 的稳定 affinity key。
func compileInputAffinityKey(input CompileGroupInput) (string, error) {
	key, err := CompileArtifactKeyForInput(input)
	if err != nil {
		return "", fmt.Errorf("compile artifact affinity: %w", err)
	}
	return key, nil
}

// selectorBodyEstimate 读取 selector body 的权威账本估时，缺失时使用 workload bootstrap。
func selectorBodyEstimate(item PlannedWorkload, parent GateID, kind WorkloadTargetKind, target GoTestTarget, index DurationSampleIndex) (int64, error) {
	parentWorkload, err := NewGoPackageWorkload(parent, target.Package, compileParentBootstrapEstimateMS)
	if err != nil {
		return 0, fmt.Errorf("construct compile parent for %q: %w", item.Workload.ID, err)
	}
	// The synthetic target row is keyed by the exact selector's production
	// input digest. Do not reconstruct that identity from a parent aggregate:
	// normal exact selectors may have no normal parent row, and calibration
	// parent aggregates are intentionally not comparable evidence.
	parentWorkload.InputDigest = item.Workload.InputDigest
	if kind == WorkloadTargetGoTest {
		if !isPrefixedSHA256Digest(item.Workload.InputDigest) {
			return 0, fmt.Errorf("compile selector %q has no exact production input digest", item.Workload.ID)
		}
		if body, ok := index.GoTestDurationMSAtResource(parentWorkload, target.Name, item.ResourceCPU, item.ResourceMemoryGiB); ok && body > 0 {
			return body, nil
		}
	}
	if item.Workload.BootstrapEstimateMS <= 0 {
		return 0, fmt.Errorf("compile selector %q has no positive body bootstrap", item.Workload.ID)
	}
	return item.Workload.BootstrapEstimateMS, nil
}

// selectorCompileEstimateFor 读取 owner 的共享 compile history。历史投影启用
// 时严格按 PlannedWorkload 已固化的资源查询；缺失样本携带统一 compile
// bootstrap，且该值只进入 CompileGroup.CompileEstimateMS。
func selectorCompileEstimateFor(item PlannedWorkload, input CompileGroupInput, tier cicontract.WorkloadResourceTier, index DurationSampleIndex, hints CompileOwnerHints) (int64, error) {
	if hint, ok := hints[compileOwnerHintLookupKey(item.Workload.ID)]; ok {
		if hint.SharedCompileEstimateMS <= 0 {
			return 0, fmt.Errorf("compile owner hint for %q has non-positive shared estimate", item.Workload.ID)
		}
		return hint.SharedCompileEstimateMS, nil
	}
	classID, cpu, memoryGiB, mode, err := compileResourceIdentityForTier(tier, item, index.context)
	if err != nil {
		return 0, err
	}
	identity := CompileTimingIdentity{
		PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
		Platform: index.context.Platform, RunnerIdentityDigest: index.context.Runner,
		ToolchainDigest: index.context.Toolchain, ExecutionMode: mode,
		ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB,
	}
	sample, found, err := index.CompileTimingIndex.EstimateMS(identity)
	if err != nil {
		return 0, fmt.Errorf("compile selector %q history: %w", item.Workload.ID, err)
	}
	if found {
		if sample.DurationMS <= 0 {
			return 0, fmt.Errorf("compile selector %q history has non-positive duration", item.Workload.ID)
		}
		return sample.DurationMS, nil
	}
	return compileParentBootstrapEstimateMS, nil
}

// compileResourceIdentityForTier 读取 calibration 或 normal 的精确资源身份。
func compileResourceIdentityForTier(tier cicontract.WorkloadResourceTier, item PlannedWorkload, context PlanningContext) (string, float64, float64, string, error) {
	if context.Calibration {
		return compileCalibrationResourceIdentity(context)
	}
	return compileNormalResourceIdentity(tier, item)
}

// compileCalibrationResourceIdentity 校验并返回固定的 calibration 资源。
func compileCalibrationResourceIdentity(context PlanningContext) (string, float64, float64, string, error) {
	if err := cicontract.ValidateCalibrationResources(context.CalibrationResourceClassID, context.CalibrationResourceCPU, context.CalibrationResourceMemoryGiB); err != nil {
		return "", 0, 0, "", err
	}
	return context.CalibrationResourceClassID, context.CalibrationResourceCPU, context.CalibrationResourceMemoryGiB, DurationExecutionModeCalibration, nil
}

// compileNormalResourceIdentity 校验 PlannedWorkload 并返回 normal 资源身份。
func compileNormalResourceIdentity(tier cicontract.WorkloadResourceTier, item PlannedWorkload) (string, float64, float64, string, error) {
	if _, err := plannedWorkloadResourceTier(item); err != nil {
		return "", 0, 0, "", err
	}
	classID, err := checkedNormalCompileResourceClass(tier)
	if err != nil {
		return "", 0, 0, "", err
	}
	cpu, memoryGiB, err := normalResourceForTier(tier)
	if err != nil {
		return "", 0, 0, "", err
	}
	return classID, cpu, memoryGiB, DurationExecutionModeNormal, nil
}
