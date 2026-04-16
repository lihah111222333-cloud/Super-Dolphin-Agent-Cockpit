package archtest

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type explicitFreeze struct {
	Path       string
	Kind       ViolationKind
	Limit      int
	Reason     string
	Owner      string
	RemoveWhen string
}

var explicitFreezeRegistry = []explicitFreeze{
	{
		Path:       "internal/module/memory",
		Kind:       ViolationFile,
		Limit:      527,
		Reason:     "memory 迁移期仍存在高密度文件，单文件上限冻结到当前裁决值",
		Owner:      "P19-Phase-F",
		RemoveWhen: "memory 拆包后恢复到默认单文件预算 400 行",
	},
	{
		Path:       "internal/module/memory",
		Kind:       ViolationPackageCount,
		Limit:      44,
		Reason:     "memory 子包拆分尚未完成，包文件数冻结到当前值",
		Owner:      "P19-Phase-F",
		RemoveWhen: "memory 子包拆分完成并回落到默认包文件预算 15 个",
	},
	{
		Path:       "internal/module/memory",
		Kind:       ViolationPackageLines,
		Limit:      11922,
		Reason:     "memory 迁移期仍承载聚合逻辑，包有效行数冻结到已裁决上限",
		Owner:      "P19-Phase-F",
		RemoveWhen: "memory 主链拆分完成并回落到默认包行数预算 4500 行",
	},
	{
		Path:       "internal/module/prompt",
		Kind:       ViolationFile,
		Limit:      492,
		Reason:     "prompt 动态装配文件仍高于默认单文件预算，冻结到当前裁决值",
		Owner:      "P19-Phase-F",
		RemoveWhen: "prompt 装配逻辑拆分后恢复到默认单文件预算 400 行",
	},
	{
		Path:       "internal/module/prompt",
		Kind:       ViolationPackageCount,
		Limit:      26,
		Reason:     "prompt 迁移期文件数高于默认预算，冻结到当前裁决上限",
		Owner:      "P19-Phase-F",
		RemoveWhen: "prompt 子包梳理完成并回落到默认包文件预算 15 个",
	},
	{
		Path:       "internal/module/thread",
		Kind:       ViolationPackageCount,
		Limit:      24,
		Reason:     "thread 迁移期文件数高于默认预算，冻结到当前裁决上限",
		Owner:      "P19-Phase-F",
		RemoveWhen: "thread 子包梳理完成并回落到默认包文件预算 15 个",
	},
	{
		Path:       "internal/module/thread",
		Kind:       ViolationPackageLines,
		Limit:      5319,
		Reason:     "thread 主链仍高于默认包行数预算，冻结到当前裁决上限",
		Owner:      "P19-Phase-F",
		RemoveWhen: "thread 拆分完成并回落到默认包行数预算 4500 行",
	},
	{
		Path:       "internal/module/turn",
		Kind:       ViolationPackageCount,
		Limit:      21,
		Reason:     "turn 迁移期文件数高于默认预算，冻结到当前裁决上限",
		Owner:      "P19-Phase-F",
		RemoveWhen: "turn 子包梳理完成并回落到默认包文件预算 15 个",
	},
	{
		Path:       "internal/provider/claudecli",
		Kind:       ViolationPackageCount,
		Limit:      23,
		Reason:     "claudecli provider 迁移期文件数高于默认预算，冻结到当前裁决上限",
		Owner:      "P19-Phase-F",
		RemoveWhen: "claudecli provider 收敛后回落到默认包文件预算 15 个",
	},
	{
		Path:       "internal/provider/codexapp",
		Kind:       ViolationPackageCount,
		Limit:      17,
		Reason:     "codexapp provider 迁移期文件数高于默认预算，冻结到当前裁决上限",
		Owner:      "P19-Phase-F",
		RemoveWhen: "codexapp provider 收敛后回落到默认包文件预算 15 个",
	},
}

func freezeRegistryIntegrityViolations() []Violation {
	seen := make(map[string]struct{}, len(explicitFreezeRegistry))
	violations := make([]Violation, 0)
	for _, entry := range explicitFreezeRegistry {
		key := freezeRegistryKey(entry.Path, entry.Kind)
		if _, ok := seen[key]; ok {
			violations = append(violations, Violation{
				Kind:    ViolationDeadKey,
				File:    entry.Path,
				Limit:   entry.Limit,
				Message: fmt.Sprintf("freeze registry 重复键 %s (%s)", entry.Path, violationKindLabel(entry.Kind)),
			})
			continue
		}
		seen[key] = struct{}{}
		if entry.Path == "" || entry.Reason == "" || entry.Owner == "" || entry.RemoveWhen == "" {
			violations = append(violations, Violation{
				Kind:    ViolationDeadKey,
				File:    entry.Path,
				Limit:   entry.Limit,
				Message: fmt.Sprintf("freeze registry 元数据不完整 %s (%s)", entry.Path, violationKindLabel(entry.Kind)),
			})
			continue
		}
		defaultLimit, ok := defaultFreezeLimit(entry.Kind)
		if !ok {
			violations = append(violations, Violation{
				Kind:    ViolationDeadKey,
				File:    entry.Path,
				Limit:   entry.Limit,
				Message: fmt.Sprintf("freeze registry 使用了不支持的 kind %d (%s)", entry.Kind, entry.Path),
			})
			continue
		}
		if entry.Limit <= defaultLimit {
			violations = append(violations, Violation{
				Kind:    ViolationDeadKey,
				File:    entry.Path,
				Got:     entry.Limit,
				Limit:   defaultLimit,
				Message: fmt.Sprintf("freeze registry 无效 %s (%s): %d <= 默认预算 %d", entry.Path, violationKindLabel(entry.Kind), entry.Limit, defaultLimit),
			})
		}
	}
	return violations
}

func deadKeyViolations(repoRoot string, scanRoots []string, stats map[string]*packageStat) []Violation {
	violations := make([]Violation, 0)
	for _, entry := range explicitFreezeRegistry {
		if !freezeAppliesToScanRoots(entry.Path, scanRoots) {
			continue
		}
		observed, exists := observedFreezeMetric(repoRoot, entry, stats)
		if !exists {
			violations = append(violations, Violation{
				Kind:    ViolationDeadKey,
				File:    entry.Path,
				Limit:   entry.Limit,
				Message: fmt.Sprintf("freeze registry 死键 %s (%s): 目标路径不存在；owner=%s remove_when=%s", entry.Path, violationKindLabel(entry.Kind), entry.Owner, entry.RemoveWhen),
			})
			continue
		}
		defaultLimit, ok := defaultFreezeLimit(entry.Kind)
		if !ok {
			continue
		}
		if observed <= defaultLimit {
			violations = append(violations, Violation{
				Kind:    ViolationDeadKey,
				File:    entry.Path,
				Got:     observed,
				Limit:   entry.Limit,
				Message: fmt.Sprintf("freeze registry 死键 %s (%s): 当前值 %d 已回落到默认预算 %d，删除该 freeze；owner=%s", entry.Path, violationKindLabel(entry.Kind), observed, defaultLimit, entry.Owner),
			})
		}
	}
	return violations
}

func frozenLimit(path string, kind ViolationKind) (int, bool) {
	for _, entry := range explicitFreezeRegistry {
		if entry.Path == path && entry.Kind == kind {
			return entry.Limit, true
		}
	}
	return 0, false
}

func observedFreezeMetric(repoRoot string, entry explicitFreeze, stats map[string]*packageStat) (int, bool) {
	if _, err := os.Stat(filepath.Join(repoRoot, filepath.FromSlash(entry.Path))); err != nil {
		return 0, false
	}
	stat := stats[entry.Path]
	if stat == nil {
		return 0, true
	}
	switch entry.Kind {
	case ViolationFile:
		return stat.MaxFileLines, true
	case ViolationPackageCount:
		return stat.Files, true
	case ViolationPackageLines:
		return stat.Lines, true
	default:
		return 0, true
	}
}

func freezeAppliesToScanRoots(path string, scanRoots []string) bool {
	if len(scanRoots) == 0 {
		return true
	}
	for _, root := range scanRoots {
		root = filepath.ToSlash(root)
		if root == "" {
			continue
		}
		if strings.HasSuffix(root, ".go") {
			if root == path || strings.HasPrefix(root, path+"/") {
				return true
			}
			continue
		}
		if root == path || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func defaultFreezeLimit(kind ViolationKind) (int, bool) {
	switch kind {
	case ViolationFile:
		return MaxFileLines, true
	case ViolationPackageCount:
		return MaxPackageFiles, true
	case ViolationPackageLines:
		return MaxPackageLines, true
	default:
		return 0, false
	}
}

func freezeRegistryKey(path string, kind ViolationKind) string {
	return fmt.Sprintf("%s#%d", path, kind)
}

func violationKindLabel(kind ViolationKind) string {
	switch kind {
	case ViolationFile:
		return "file"
	case ViolationFunc:
		return "func"
	case ViolationNesting:
		return "nesting"
	case ViolationCC:
		return "cc"
	case ViolationIdentifier:
		return "identifier"
	case ViolationPackageCount:
		return "package_count"
	case ViolationPackageLines:
		return "package_lines"
	case ViolationDeadKey:
		return "dead_key"
	default:
		return fmt.Sprintf("kind_%d", kind)
	}
}
