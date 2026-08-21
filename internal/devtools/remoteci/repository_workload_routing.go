package remoteci

import (
	"errors"
	"fmt"
	"math"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

const bytesPerGiB = int64(1024 * 1024 * 1024)

// ProjectRepositoryWorkloadExecutionUnits 将 target-tree workload 身份与动态规划资源合成为自动路由单元。
// manifest 与 planned 必须是同一精确 MISS 集合；调用方不能补默认平台、运行时、估时或资源。
func ProjectRepositoryWorkloadExecutionUnits(
	manifest RepositoryWorkloadManifest,
	planned []gate.PlannedWorkload,
) ([]heteroci.ExecutionUnit, error) {
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("project repository workload routing manifest: %w", err)
	}
	if planned == nil || len(planned) == 0 {
		return nil, errors.New("project repository workload routing requires a non-empty planned workload set")
	}
	manifestByID, err := repositoryRoutingManifestIndex(manifest.Workloads)
	if err != nil {
		return nil, err
	}
	if len(manifestByID) != len(planned) {
		return nil, errors.New("repository workload routing manifest and planned workload sets differ")
	}
	units := make([]heteroci.ExecutionUnit, 0, len(planned))
	seen := make(map[string]struct{}, len(planned))
	for index, item := range planned {
		unit, err := projectRepositoryWorkloadExecutionUnit(manifestByID, item, seen)
		if err != nil {
			return nil, fmt.Errorf("project planned workload %d: %w", index, err)
		}
		units = append(units, unit)
	}
	return units, nil
}

// repositoryRoutingManifestIndex 构造唯一 workload 索引并拒绝重复身份。
func repositoryRoutingManifestIndex(workloads []RepositoryWorkload) (map[string]RepositoryWorkload, error) {
	indexed := make(map[string]RepositoryWorkload, len(workloads))
	for _, workload := range workloads {
		if _, duplicate := indexed[workload.ID]; duplicate {
			return nil, fmt.Errorf("repository workload routing manifest duplicates %q", workload.ID)
		}
		indexed[workload.ID] = workload
	}
	return indexed, nil
}

// projectRepositoryWorkloadExecutionUnit 逐项绑定 manifest 身份、规划资源并复核领域单元。
func projectRepositoryWorkloadExecutionUnit(
	manifestByID map[string]RepositoryWorkload,
	planned gate.PlannedWorkload,
	seen map[string]struct{},
) (heteroci.ExecutionUnit, error) {
	if _, duplicate := seen[planned.Workload.ID]; duplicate {
		return heteroci.ExecutionUnit{}, fmt.Errorf("planned workload %q is duplicated", planned.Workload.ID)
	}
	seen[planned.Workload.ID] = struct{}{}
	workload, ok := manifestByID[planned.Workload.ID]
	if !ok {
		return heteroci.ExecutionUnit{}, fmt.Errorf("planned workload %q is missing from repository manifest", planned.Workload.ID)
	}
	platform, runtime, err := repositoryRoutingTarget(workload)
	if err != nil {
		return heteroci.ExecutionUnit{}, err
	}
	cpuMilli, memoryBytes, err := repositoryRoutingResources(planned.ResourceCPU, planned.ResourceMemoryGiB)
	if err != nil {
		return heteroci.ExecutionUnit{}, fmt.Errorf("planned workload %q resources: %w", planned.Workload.ID, err)
	}
	unit := heteroci.ExecutionUnit{
		UnitID: planned.Workload.ID, TargetPlatform: platform, RequiredRuntime: runtime,
		EstimatedDurationMS: planned.EstimatedDurationMS, CPUMilli: cpuMilli, MemoryBytes: memoryBytes,
	}
	if err := unit.Validate(); err != nil {
		return heteroci.ExecutionUnit{}, fmt.Errorf("planned workload %q execution unit: %w", planned.Workload.ID, err)
	}
	return unit, nil
}

// repositoryRoutingTarget 将 manifest 的显式平台/运行时矩阵映射到 heteroci 领域类型。
func repositoryRoutingTarget(workload RepositoryWorkload) (heteroci.Platform, heteroci.RuntimeKind, error) {
	switch {
	case workload.Platform == RepositoryWorkloadPlatformLinuxAMD64 && workload.Runtime == RepositoryWorkloadRuntimeUbuntuContainer:
		return heteroci.PlatformLinuxAMD64, heteroci.RuntimeUbuntuContainer, nil
	case workload.Platform == RepositoryWorkloadPlatformWindowsAMD64 && workload.Runtime == RepositoryWorkloadRuntimeWindowsNative:
		return heteroci.PlatformWindowsAMD64, heteroci.RuntimeWindowsNative, nil
	default:
		return "", "", fmt.Errorf("repository workload %q platform/runtime is unsupported", workload.ID)
	}
}

// repositoryRoutingResources 将 planner 浮点规格严格转换为整数毫核与字节。
func repositoryRoutingResources(cpu, memoryGiB float64) (int64, int64, error) {
	if !validRepositoryRoutingResource(cpu, float64(math.MaxInt64)/1000) {
		return 0, 0, errors.New("CPU is invalid")
	}
	if !validRepositoryRoutingResource(memoryGiB, float64(math.MaxInt64)/float64(bytesPerGiB)) {
		return 0, 0, errors.New("memory is invalid")
	}
	cpuMilliFloat := cpu * 1000
	memoryBytesFloat := memoryGiB * float64(bytesPerGiB)
	if math.Trunc(cpuMilliFloat) != cpuMilliFloat || math.Trunc(memoryBytesFloat) != memoryBytesFloat {
		return 0, 0, errors.New("resources cannot be represented exactly")
	}
	return int64(cpuMilliFloat), int64(memoryBytesFloat), nil
}

func validRepositoryRoutingResource(value, maximum float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value > 0 && value <= maximum
}
