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
		Path:       "internal/provider/codexapp",
		Kind:       ViolationPackageCount,
		Limit:      31,
		Reason:     "P25 skill progressive-disclosure keeps codexapp adapter split across driver/session/recovery/dynamic-tool tests until Phase 3 policy consolidation",
		Owner:      "P25 skill rollout",
		RemoveWhen: "Phase 3 provider default policy removes temporary override/evidence scaffolding or codexapp package is split below default package file budget",
	},
	{
		Path:       "internal/module/memory",
		Kind:       ViolationPackageCount,
		Limit:      32,
		Reason:     "Phase 1.6 上下文继承与预警：新增 auto-continue state RPC 与 sharedfile RPC 因 600 行/文件守卫被迫各占独立文件（ui_rpc_sharedfile.go + ui_rpc_auto_continue_state.go），把 internal/module/memory 推到 32 个非测试文件",
		Owner:      "Phase 1.6 上下文继承与预警",
		RemoveWhen: "Phase 2.x promote-task 引入后把 sharedfile RPC 与 promote 路由合并到一个文件，让 internal/module/memory 包非测试文件回落 ≤30；或主文件 ui_rpc_mutations.go 拆分使新增 RPC 可直接 inline 于主文件",
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
