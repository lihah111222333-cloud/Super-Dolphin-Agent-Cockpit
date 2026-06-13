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

var explicitFreezeRegistry = []explicitFreeze{}

// freezeRegistryIntegrityViolations 检查 freeze 表本身是否完整且没有重复项。
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

// deadKeyViolations 找出已经失效或可以删除的 freeze 条目。
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

// observedFreezeMetric 读取 freeze 条目当前实际对应的指标值。
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

// freezeAppliesToScanRoots 判断 freeze 条目是否落在本次扫描范围内。
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

// violationKindLabel 输出 freeze 和报告里使用的稳定 kind 名称。
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
	case ViolationFuncComment:
		return "func_comment"
	default:
		return fmt.Sprintf("kind_%d", kind)
	}
}
