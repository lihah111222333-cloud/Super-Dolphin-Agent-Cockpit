package gate

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

// LocalWorkloadPlanningProjection is the read-only gate-owned estimate handed to local producers.
type LocalWorkloadPlanningProjection struct {
	WorkloadID          GateID  `json:"workload_id"`
	PredictedDurationMS int64   `json:"predicted_duration_ms"`
	ResourceClassID     string  `json:"resource_class_id"`
	ResourceCPU         float64 `json:"resource_cpu"`
	ResourceMemoryGiB   float64 `json:"resource_memory_gib"`
}

// ProjectLocalWorkloadPlanning 将选择的 canonical workload 交给 gate 时长 owner 并导出只读资源投影。
func ProjectLocalWorkloadPlanning(index DurationSampleIndex, catalog WorkloadCatalog, workloadIDs []GateID) ([]LocalWorkloadPlanningProjection, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return nil, fmt.Errorf("validate local planning catalog: %w", err)
	}
	selected, err := selectLocalProjectionWorkloads(catalog, workloadIDs)
	if err != nil {
		return nil, err
	}
	projection := make([]LocalWorkloadPlanningProjection, 0, len(selected))
	for _, workload := range selected {
		estimate, resource, err := index.estimateWorkloadDuration(workload)
		if err != nil {
			return nil, fmt.Errorf("estimate local workload %q: %w", workload.ID, err)
		}
		classID, err := localProjectionResourceClass(index.context, resource)
		if err != nil {
			return nil, fmt.Errorf("resource identity for local workload %q: %w", workload.ID, err)
		}
		projection = append(projection, LocalWorkloadPlanningProjection{
			WorkloadID: GateID(workload.ID), PredictedDurationMS: estimate,
			ResourceClassID: classID, ResourceCPU: resource.cpu, ResourceMemoryGiB: resource.memoryGiB,
		})
	}
	return projection, nil
}

// ProjectLocalBootstrapWorkloadPlanning 基于不可变 catalog estimate 生成 local-only schedule。
// 它刻意没有 duration index 输入：
// remote planning history, accepted baselines, runner identity, and ImageCache
// state cannot influence a local PASS lookup plan.
func ProjectLocalBootstrapWorkloadPlanning(catalog WorkloadCatalog, workloadIDs []GateID) ([]LocalWorkloadPlanningProjection, error) {
	if err := ValidateWorkloadCatalog(catalog); err != nil {
		return nil, fmt.Errorf("validate local bootstrap planning catalog: %w", err)
	}
	selected, err := selectLocalProjectionWorkloads(catalog, workloadIDs)
	if err != nil {
		return nil, err
	}
	projection := make([]LocalWorkloadPlanningProjection, 0, len(selected))
	for _, workload := range selected {
		if workload.BootstrapEstimateMS <= 0 {
			return nil, fmt.Errorf("local bootstrap workload %q has invalid estimate %dms", workload.ID, workload.BootstrapEstimateMS)
		}
		tier, err := cicontract.ClassifyWorkloadResourceDuration(workload.BootstrapEstimateMS)
		if err != nil {
			return nil, fmt.Errorf("classify local bootstrap workload %q: %w", workload.ID, err)
		}
		cpu, memoryGiB, err := normalResourceForTier(tier)
		if err != nil {
			return nil, fmt.Errorf("local bootstrap resource for workload %q: %w", workload.ID, err)
		}
		classID := normalCompileResourceClass(tier)
		if classID == "" {
			return nil, fmt.Errorf("local bootstrap workload %q has no resource class for tier %d", workload.ID, tier)
		}
		projection = append(projection, LocalWorkloadPlanningProjection{
			WorkloadID: GateID(workload.ID), PredictedDurationMS: workload.BootstrapEstimateMS,
			ResourceClassID: classID, ResourceCPU: cpu, ResourceMemoryGiB: memoryGiB,
		})
	}
	return projection, nil
}

// selectLocalProjectionWorkloads 校验选择集合并保留 WorkloadCatalog 的 canonical 顺序，拒绝越界或重复条目。
func selectLocalProjectionWorkloads(catalog WorkloadCatalog, workloadIDs []GateID) ([]Workload, error) {
	if len(workloadIDs) == 0 {
		return nil, errors.New("local workload planning selection is empty")
	}
	byID := make(map[GateID]Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		byID[GateID(workload.ID)] = workload
	}
	selected := make([]Workload, 0, len(workloadIDs))
	seen := make(map[GateID]struct{}, len(workloadIDs))
	for index, id := range workloadIDs {
		if strings.TrimSpace(string(id)) == "" {
			return nil, fmt.Errorf("local workload planning selection[%d] is empty", index)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("local workload planning selection contains duplicate %q", id)
		}
		workload, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("local workload planning selection contains unknown %q", id)
		}
		if !workload.Shardable {
			return nil, fmt.Errorf("local workload planning selection contains non-shardable %q", id)
		}
		seen[id] = struct{}{}
	}
	for _, workload := range catalog.Workloads {
		if _, ok := seen[GateID(workload.ID)]; ok {
			selected = append(selected, workload)
		}
	}
	return selected, nil
}

// localProjectionResourceClass 将 owner tuple 严格映射到唯一注册的资源 class。
func localProjectionResourceClass(context PlanningContext, resource durationSampleResource) (string, error) {
	if err := validateLocalProjectionResourceTuple(resource); err != nil {
		return "", err
	}
	if context.Calibration {
		return localProjectionCalibrationClass(context, resource)
	}
	return localProjectionNormalClass(resource)
}

// validateLocalProjectionResourceTuple 拒绝无法作为资源身份的数值。
func validateLocalProjectionResourceTuple(resource durationSampleResource) error {
	if math.IsNaN(resource.cpu) || math.IsInf(resource.cpu, 0) || resource.cpu <= 0 {
		return errors.New("resource CPU must be finite and positive")
	}
	if math.IsNaN(resource.memoryGiB) || math.IsInf(resource.memoryGiB, 0) || resource.memoryGiB <= 0 {
		return errors.New("resource memory must be finite and positive")
	}
	return nil
}

// localProjectionCalibrationClass 校验固定校准资源及其 owner class。
func localProjectionCalibrationClass(context PlanningContext, resource durationSampleResource) (string, error) {
	if err := cicontract.ValidateCalibrationResources(context.CalibrationResourceClassID, context.CalibrationResourceCPU, context.CalibrationResourceMemoryGiB); err != nil {
		return "", fmt.Errorf("calibration resource: %w", err)
	}
	if resource.cpu != context.CalibrationResourceCPU || resource.memoryGiB != context.CalibrationResourceMemoryGiB {
		return "", errors.New("calibration owner resource tuple drifted")
	}
	if resource.classID != "" && resource.classID != context.CalibrationResourceClassID {
		return "", fmt.Errorf("calibration owner class %q does not match %q", resource.classID, context.CalibrationResourceClassID)
	}
	return context.CalibrationResourceClassID, nil
}

// localProjectionNormalClass 校验 normal 三档 tuple 并恢复其 canonical class。
func localProjectionNormalClass(resource durationSampleResource) (string, error) {
	tier, err := normalResourceTierForTuple(resource.cpu, resource.memoryGiB)
	if err != nil {
		return "", err
	}
	classID := normalCompileResourceClass(tier)
	if classID == "" {
		return "", fmt.Errorf("normal resource tier %d has no class identity", tier)
	}
	if resource.classID != "" && resource.classID != classID {
		return "", fmt.Errorf("owner class %q does not match tuple class %q", resource.classID, classID)
	}
	return classID, nil
}
