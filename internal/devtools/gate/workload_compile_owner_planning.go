package gate

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// shardableWorkloadEstimate 是创建 PlannedWorkload 之前的中间估值。
// 资源 tuple 在此阶段仍可由 compile owner fixed point 覆盖；一旦转成
// PlannedWorkload，资源身份即被冻结，后续 compileGroup 只读该身份。
type shardableWorkloadEstimate struct {
	workload   Workload
	estimateMS int64
	resource   durationSampleResource
}

// CompileOwnerHint 固化同一 package+semantic owner 的共享编译成本和资源。
// SharedCompileEstimateMS 只用于 compile group 成本闭包，绝不写入 selector
// body 或 PlannedWorkload.EstimatedDurationMS。
type CompileOwnerHint struct {
	OwnerKey                string
	SharedCompileEstimateMS int64
	ResourceTier            cicontract.WorkloadResourceTier
	ResourceClassID         string
	ResourceCPU             float64
	ResourceMemoryGiB       float64
}

// CompileOwnerHints 将 canonical owner key 映射到固定的共享编译估值。
type CompileOwnerHints map[string]CompileOwnerHint

// CompileOwnerKey 返回跨 source generation 稳定的 package+semantic owner。
func CompileOwnerKey(packageTarget, semanticKey string) string {
	return strings.TrimSpace(packageTarget) + "\x00" + strings.TrimSpace(semanticKey)
}

// estimateShardableWorkloads 只完成 workload body 的第一阶段估值，不创建
// PlannedWorkload；compile owner 资源因此能在计划对象落地前覆盖资源 tuple。
func estimateShardableWorkloads(catalog WorkloadCatalog, index DurationSampleIndex) ([]shardableWorkloadEstimate, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return nil, err
	}
	base := make([]shardableWorkloadEstimate, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		if !workload.Shardable {
			continue
		}
		estimate, resource, err := index.estimateWorkloadDuration(workload)
		if err != nil {
			return nil, err
		}
		base = append(base, shardableWorkloadEstimate{workload: workload, estimateMS: estimate, resource: resource})
	}
	if len(base) == 0 {
		return nil, errors.New("workload catalog contains no shardable workload")
	}
	return base, nil
}

// plannedWorkloadsFromEstimates 将第一阶段估值转为带持久化资源身份的
// PlannedWorkload。compile owner hint 只覆盖资源 CPU/memory，不改 body 时长。
func plannedWorkloadsFromEstimates(base []shardableWorkloadEstimate, hints CompileOwnerHints) ([]PlannedWorkload, error) {
	planned := make([]PlannedWorkload, 0, len(base))
	for _, item := range base {
		resource := item.resource
		if hint, ok := hints[compileOwnerHintLookupKey(item.workload.ID)]; ok {
			if hint.ResourceCPU <= 0 || hint.ResourceMemoryGiB <= 0 {
				return nil, fmt.Errorf("compile owner hint for %q has invalid resource", item.workload.ID)
			}
			resource = durationSampleResource{
				classID: hint.ResourceClassID, cpu: hint.ResourceCPU, memoryGiB: hint.ResourceMemoryGiB,
			}
		}
		if resource.cpu <= 0 || resource.memoryGiB <= 0 {
			return nil, fmt.Errorf("workload %q has invalid planned resource", item.workload.ID)
		}
		planned = append(planned, PlannedWorkload{
			Workload: item.workload, EstimatedDurationMS: item.estimateMS,
			ResourceCPU: resource.cpu, ResourceMemoryGiB: resource.memoryGiB,
		})
	}
	return planned, nil
}

// BuildCompileOwnerHints 为每个 selector 只解析一次 package+semantic owner。
// 返回 map 以 selector ID 为键，确保资源覆盖发生在 PlannedWorkload 创建前，
// 而 hint 本身仍是 owner 共享的。
func BuildCompileOwnerHints(base []shardableWorkloadEstimate, compileInputs map[GateID]CompileGroupInput, index CompileTimingIndex, contexts ...PlanningContext) (CompileOwnerHints, error) {
	context := compileOwnerPlanningContext(index, contexts...)
	ownerInputs, err := collectCompileOwnerInputs(base, compileInputs)
	if err != nil {
		return nil, err
	}
	return resolveCompileOwnerHints(base, compileInputs, ownerInputs, index, context)
}

// collectCompileOwnerInputs 校验每个 exact selector，并按 package+semantic 收集共享输入。
func collectCompileOwnerInputs(base []shardableWorkloadEstimate, compileInputs map[GateID]CompileGroupInput) (map[string]CompileGroupInput, error) {
	ownerInputs := make(map[string]CompileGroupInput)
	for _, estimate := range base {
		parent, kind, payload, targeted, err := ParseWorkloadID(estimate.workload.ID)
		if err != nil || !targeted || !compileGroupTargetKind(kind) {
			continue
		}
		input, ok := compileInputs[GateID(estimate.workload.ID)]
		if !ok {
			return nil, fmt.Errorf("compile input is missing for exact Go selector %q", estimate.workload.ID)
		}
		target, err := parseCompileGroupTarget(kind, payload)
		if err != nil {
			return nil, fmt.Errorf("parse compile owner selector %q: %w", estimate.workload.ID, err)
		}
		if err := validateCompilePlanningSelectorInput(estimate.workload.ID, input, target, parent, kind); err != nil {
			return nil, err
		}
		ownerKey := CompileOwnerKey(input.PackageTarget, input.SemanticKey)
		if previous, exists := ownerInputs[ownerKey]; exists {
			// 共享 compile 成本刻意忽略 source-sensitive input/profile digest；
			// 同 owner 的冲突输入仍在后续按 affinity 分组。
			_ = previous
		} else {
			ownerInputs[ownerKey] = input
		}
	}
	return ownerInputs, nil
}

// resolveCompileOwnerHints 为已收集的 owner 解析 fixed-point，并覆盖每个 selector。
func resolveCompileOwnerHints(base []shardableWorkloadEstimate, compileInputs map[GateID]CompileGroupInput, ownerInputs map[string]CompileGroupInput, index CompileTimingIndex, context PlanningContext) (CompileOwnerHints, error) {
	hints := make(CompileOwnerHints)
	keys := make([]string, 0, len(ownerInputs))
	for key := range ownerInputs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		hint, err := resolveCompileOwnerHint(key, ownerInputs[key], index, context)
		if err != nil {
			return nil, err
		}
		for _, estimate := range base {
			input, ok := compileInputs[GateID(estimate.workload.ID)]
			if !ok || CompileOwnerKey(input.PackageTarget, input.SemanticKey) != key {
				continue
			}
			hints[compileOwnerHintLookupKey(estimate.workload.ID)] = hint
		}
	}
	return hints, nil
}

// compileOwnerPlanningContext 保持三参数 owner-hints API 可用；生产 planner
// 会显式传入 index.context，测试或独立调用在有样本时从样本环境推导。
func compileOwnerPlanningContext(index CompileTimingIndex, contexts ...PlanningContext) PlanningContext {
	if len(contexts) > 1 {
		return PlanningContext{}
	}
	if len(contexts) == 1 {
		return contexts[0]
	}
	if len(index.Samples) == 0 {
		return PlanningContext{}
	}
	context := PlanningContext{
		Platform:  index.Samples[0].Identity.Platform,
		Runner:    index.Samples[0].Identity.RunnerIdentityDigest,
		Toolchain: index.Samples[0].Identity.ToolchainDigest,
	}
	if index.Samples[0].Identity.ExecutionMode == DurationExecutionModeCalibration {
		context.Calibration = true
		context.CalibrationResourceClassID = index.Samples[0].Identity.ResourceClassID
		context.CalibrationResourceCPU = index.Samples[0].Identity.ResourceCPU
		context.CalibrationResourceMemoryGiB = index.Samples[0].Identity.ResourceMemoryGiB
	}
	return context
}

// compileOwnerHintLookupKey 保持 selector 覆盖键与 owner key 分离；同一
// owner 下的 hint 值仍完全相同。
func compileOwnerHintLookupKey(workloadID string) string { return workloadID }

func resolveCompileOwnerHint(ownerKey string, input CompileGroupInput, index CompileTimingIndex, context PlanningContext) (CompileOwnerHint, error) {
	if err := validateCompileOwnerRequest(ownerKey, input, context); err != nil {
		return CompileOwnerHint{}, err
	}
	if context.Calibration {
		return resolveCalibrationCompileOwnerHint(ownerKey, input, index, context)
	}
	return resolveNormalCompileOwnerHint(ownerKey, input, index, context)
}

// validateCompileOwnerRequest 校验 owner 的 package、semantic 与运行上下文完整性。
func validateCompileOwnerRequest(ownerKey string, input CompileGroupInput, context PlanningContext) error {
	if strings.TrimSpace(input.PackageTarget) == "" || strings.TrimSpace(input.SemanticKey) == "" {
		return fmt.Errorf("compile owner %q has incomplete package/semantic identity", ownerKey)
	}
	if strings.TrimSpace(context.Platform) == "" || strings.TrimSpace(context.Runner) == "" || strings.TrimSpace(context.Toolchain) == "" {
		return errors.New("compile owner planning context is incomplete")
	}
	return nil
}

func resolveCalibrationCompileOwnerHint(ownerKey string, input CompileGroupInput, index CompileTimingIndex, context PlanningContext) (CompileOwnerHint, error) {
	classID := context.CalibrationResourceClassID
	cpu, memoryGiB := context.CalibrationResourceCPU, context.CalibrationResourceMemoryGiB
	if err := cicontract.ValidateCalibrationResources(classID, cpu, memoryGiB); err != nil {
		return CompileOwnerHint{}, fmt.Errorf("compile owner %q calibration resource: %w", ownerKey, err)
	}
	identity := CompileTimingIdentity{
		PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
		Platform: context.Platform, RunnerIdentityDigest: context.Runner,
		ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeCalibration,
		ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB,
	}
	sample, found, err := index.EstimateMS(identity)
	if err != nil {
		return CompileOwnerHint{}, fmt.Errorf("compile owner %q calibration history: %w", ownerKey, err)
	}
	estimate := compileParentBootstrapEstimateMS
	if found {
		estimate = sample.DurationMS
	}
	return CompileOwnerHint{OwnerKey: ownerKey, SharedCompileEstimateMS: estimate, ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB}, nil
}

func resolveNormalCompileOwnerHint(ownerKey string, input CompileGroupInput, index CompileTimingIndex, context PlanningContext) (CompileOwnerHint, error) {

	tier := cicontract.WorkloadResourceTierFast
	carried := compileParentBootstrapEstimateMS
	visited := map[cicontract.WorkloadResourceTier]struct{}{}
	for range 3 {
		if _, exists := visited[tier]; exists {
			return CompileOwnerHint{}, fmt.Errorf("compile owner %q resource fixed point oscillates", ownerKey)
		}
		visited[tier] = struct{}{}
		result, err := resolveNormalCompileTier(ownerKey, input, index, context, tier, carried)
		if err != nil {
			return CompileOwnerHint{}, err
		}
		if result.done {
			return result.hint, nil
		}
		carried, tier = result.estimate, result.nextTier
	}
	return CompileOwnerHint{}, fmt.Errorf("compile owner %q resource fixed point did not converge", ownerKey)
}

type compileOwnerTierResult struct {
	hint     CompileOwnerHint
	estimate int64
	nextTier cicontract.WorkloadResourceTier
	done     bool
}

// resolveNormalCompileTier 查询一个 normal 资源档并返回 fixed-point 的下一步。
func resolveNormalCompileTier(ownerKey string, input CompileGroupInput, index CompileTimingIndex, context PlanningContext, tier cicontract.WorkloadResourceTier, carried int64) (compileOwnerTierResult, error) {
	cpu, memoryGiB, err := normalResourceForTier(tier)
	if err != nil {
		return compileOwnerTierResult{}, err
	}
	classID, err := checkedNormalCompileResourceClass(tier)
	if err != nil {
		return compileOwnerTierResult{}, err
	}
	identity := CompileTimingIdentity{
		PackageTarget: input.PackageTarget, SemanticKey: input.SemanticKey,
		Platform: context.Platform, RunnerIdentityDigest: context.Runner,
		ToolchainDigest: context.Toolchain, ExecutionMode: DurationExecutionModeNormal,
		ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB,
	}
	sample, found, err := index.EstimateMS(identity)
	if err != nil {
		return compileOwnerTierResult{}, fmt.Errorf("compile owner %q history: %w", ownerKey, err)
	}
	estimate := carried
	if found {
		if sample.DurationMS <= 0 {
			return compileOwnerTierResult{}, fmt.Errorf("compile owner %q history has non-positive duration", ownerKey)
		}
		estimate = sample.DurationMS
	}
	if !found {
		// 首次 MISS 没有小档 compile history 时，固定使用 small 资源；
		// bootstrap 只是一次共享 compile 成本，不据此将资源升档。
		return compileOwnerTierResult{hint: CompileOwnerHint{OwnerKey: ownerKey, SharedCompileEstimateMS: carried, ResourceTier: tier, ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB}, done: true}, nil
	}
	nextTier, done, err := normalResourceTierTransition(tier, estimate)
	if err != nil {
		return compileOwnerTierResult{}, fmt.Errorf("compile owner %q resource transition at %dms: %w", ownerKey, estimate, err)
	}
	if done {
		// 资源规划只允许向更高档位收敛；一次 fixed-point 中不得因更高档样本变快而降档。
		// 独立 downsizing 策略负责经过最小样本数验证后的降档，避免规划在档位间振荡。
		return compileOwnerTierResult{hint: CompileOwnerHint{OwnerKey: ownerKey, SharedCompileEstimateMS: estimate, ResourceTier: tier, ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB}, done: true}, nil
	}
	return compileOwnerTierResult{estimate: estimate, nextTier: nextTier}, nil
}
