package heteroci

import (
	"fmt"
	"strings"
	"unicode"
)

// ExecutionUnit 描述一个带有目标平台、运行时和资源估计的异构 CI 工作单元。
type ExecutionUnit struct {
	// UnitID 是工作单元的稳定标识。
	UnitID string `json:"unit_id"`
	// TargetPlatform 是工作单元要求的规范目标平台。
	TargetPlatform Platform `json:"target_platform"`
	// RequiredRuntime 是工作单元要求的规范执行运行时。
	RequiredRuntime RuntimeKind `json:"required_runtime"`
	// EstimatedDurationMS 是工作单元的正数估计耗时（毫秒）。
	EstimatedDurationMS int64 `json:"estimated_duration_ms"`
	// CPUMilli 是工作单元要求的正数 CPU 毫核数。
	CPUMilli int64 `json:"cpu_milli"`
	// MemoryBytes 是工作单元要求的正数内存字节数。
	MemoryBytes int64 `json:"memory_bytes"`
}

// Validate 校验工作单元的身份、平台、运行时矩阵和正数资源估计。
func (unit ExecutionUnit) Validate() error {
	if !validExecutionUnitID(unit.UnitID) {
		return fmt.Errorf("execution unit id %q must be non-empty, trimmed, and free of control characters", unit.UnitID)
	}
	if !isCanonicalPlatform(unit.TargetPlatform) {
		return fmt.Errorf("execution unit %q target platform %q is not canonical", unit.UnitID, unit.TargetPlatform)
	}
	if !isCanonicalRuntime(unit.RequiredRuntime) {
		return fmt.Errorf("execution unit %q required runtime %q is not canonical", unit.UnitID, unit.RequiredRuntime)
	}
	if !executionUnitRuntimeSupportsPlatform(unit.RequiredRuntime, unit.TargetPlatform) {
		return fmt.Errorf("execution unit %q runtime %q cannot target platform %q", unit.UnitID, unit.RequiredRuntime, unit.TargetPlatform)
	}
	if unit.EstimatedDurationMS <= 0 {
		return fmt.Errorf("execution unit %q estimated duration must be positive", unit.UnitID)
	}
	if unit.CPUMilli <= 0 {
		return fmt.Errorf("execution unit %q cpu milli must be positive", unit.UnitID)
	}
	if unit.MemoryBytes <= 0 {
		return fmt.Errorf("execution unit %q memory bytes must be positive", unit.UnitID)
	}
	return nil
}

// ValidateExecutionUnits 校验非空工作单元集合及其唯一标识；输入顺序不参与校验。
func ValidateExecutionUnits(units []ExecutionUnit) error {
	if units == nil {
		return fmt.Errorf("execution units must be non-nil")
	}
	if len(units) == 0 {
		return fmt.Errorf("execution units must not be empty")
	}
	seenIDs := make(map[string]struct{}, len(units))
	for index, unit := range units {
		if err := unit.Validate(); err != nil {
			return fmt.Errorf("validate execution unit at index %d: %w", index, err)
		}
		if _, exists := seenIDs[unit.UnitID]; exists {
			return fmt.Errorf("execution unit id %q is duplicated", unit.UnitID)
		}
		seenIDs[unit.UnitID] = struct{}{}
	}
	return nil
}

// executionUnitCanRunOnNode 判断工作单元是否能在当前 live 节点的已激活运行时上执行。
// 该判断只检查平台、运行时能力和 live 状态，不做资源装箱、ECI 溢出或 fallback 决策。
func executionUnitCanRunOnNode(unit ExecutionUnit, node NodeSnapshot) bool {
	if unit.Validate() != nil || node.Status != NodeStatusLive || !isCanonicalPlatform(node.Platform) {
		return false
	}
	if !nodeHasActiveRuntime(node, unit.RequiredRuntime) {
		return false
	}
	switch unit.RequiredRuntime {
	case RuntimeUbuntuContainer, RuntimeDarlingCompatRuntime, RuntimeDockerOSXExperimental:
		return node.Platform == PlatformLinuxAMD64
	case RuntimeWindowsNative:
		return node.Platform == PlatformWindowsAMD64
	case RuntimeDarwinNative:
		return node.Platform == unit.TargetPlatform
	default:
		return false
	}
}

func validExecutionUnitID(value string) bool {
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

// executionUnitRuntimeSupportsPlatform 严格校验运行时与目标平台的唯一允许矩阵。
func executionUnitRuntimeSupportsPlatform(runtime RuntimeKind, platform Platform) bool {
	switch runtime {
	case RuntimeUbuntuContainer:
		return platform == PlatformLinuxAMD64
	case RuntimeDarlingCompatRuntime:
		return platform == PlatformDarwinAMD64
	case RuntimeWindowsNative:
		return platform == PlatformWindowsAMD64
	case RuntimeDarwinNative:
		return platform == PlatformDarwinAMD64 || platform == PlatformDarwinARM64
	case RuntimeDockerOSXExperimental:
		return platform == PlatformDarwinAMD64
	default:
		return false
	}
}

func nodeHasActiveRuntime(node NodeSnapshot, required RuntimeKind) bool {
	for _, capability := range node.ActiveCapabilities {
		if capability.Runtime == required {
			return true
		}
	}
	return false
}
