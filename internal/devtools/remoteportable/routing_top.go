package remoteportable

import (
	"errors"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/heteroci"
)

// ProjectRepositoryWorkloadBootstrapExecutionUnits 将顶层 manifest workload 投影为首次路由单元。
func ProjectRepositoryWorkloadBootstrapExecutionUnits(manifest PortableManifest) ([]heteroci.ExecutionUnit, error) {
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("project portable workload bootstrap routing manifest: %w", err)
	}
	return ProjectPortableWorkloadBootstrapExecutionUnits(manifest.Workloads)
}

// ProjectPortableWorkloadBootstrapExecutionUnits 将显式 execution workload 投影为路由单元。
func ProjectPortableWorkloadBootstrapExecutionUnits(workloads []PortableManifestWorkload) ([]heteroci.ExecutionUnit, error) {
	if len(workloads) == 0 {
		return nil, errors.New("portable execution workloads are required")
	}
	units := make([]heteroci.ExecutionUnit, 0, len(workloads))
	for index, workload := range workloads {
		unit, err := portableExecutionUnit(workload)
		if err != nil {
			return nil, fmt.Errorf("project portable bootstrap workload %d: %w", index, err)
		}
		units = append(units, unit)
	}
	return units, nil
}

func portableExecutionUnit(workload PortableManifestWorkload) (heteroci.ExecutionUnit, error) {
	platform, runtime, err := portableTopLevelRoutingTarget(workload)
	if err != nil {
		return heteroci.ExecutionUnit{}, err
	}
	unit := heteroci.ExecutionUnit{
		UnitID: workload.UnitID, TargetPlatform: platform, RequiredRuntime: runtime,
		EstimatedDurationMS: workload.HistoricalEstimateMS, CPUMilli: workload.CPUMilli, MemoryBytes: workload.MemoryBytes,
	}
	if err := unit.Validate(); err != nil {
		return heteroci.ExecutionUnit{}, fmt.Errorf("portable workload %q: %w", workload.UnitID, err)
	}
	return unit, nil
}

func portableTopLevelRoutingTarget(workload PortableManifestWorkload) (heteroci.Platform, heteroci.RuntimeKind, error) {
	switch {
	case workload.TargetPlatform == string(RepositoryWorkloadPlatformLinuxAMD64) && workload.RuntimeCapability == string(RepositoryWorkloadRuntimeUbuntuContainer):
		return heteroci.PlatformLinuxAMD64, heteroci.RuntimeUbuntuContainer, nil
	case workload.TargetPlatform == string(RepositoryWorkloadPlatformWindowsAMD64) && workload.RuntimeCapability == string(RepositoryWorkloadRuntimeWindowsNative):
		return heteroci.PlatformWindowsAMD64, heteroci.RuntimeWindowsNative, nil
	default:
		return "", "", fmt.Errorf("portable workload %q platform/runtime is unsupported", workload.UnitID)
	}
}
