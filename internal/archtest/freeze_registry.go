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
		Kind:       ViolationPackageCount,
		Limit:      31,
		Reason:     "上下文继承与预警合并 (Phase 1.6 / 2.1) 后 memory 包多出 auto-continue state RPC 套件；临时超 30 默认上限，后续拆 sub-package 后取消。",
		Owner:      "chat",
		RemoveWhen: "memory 包拆出 auto-continue 子包后文件数回落 ≤ 30，删除该 freeze。",
	},
	{
		Path:       "cmd/mcp-orch/orchestration",
		Kind:       ViolationPackageCount,
		Limit:      40,
		Reason:     "dispatcher-wiring batch (5 reviewer P0 #1) 接入 NodeExecutor 抽象后新增 node_router.go + dag_dispatch.go 两个必要单职责文件 (30→32)；closure follow-up 再加 sharedfile_adapter.go 把 store/sharedfile.Store 适配成 nodeexec.SharedFileReader/Writer 端口 (32→33)；ADR-016 v1.2 §2.1-§2.5 C3（spawned agent 自动 stop）按 §2.5 P1 拍板新建 stop_helper.go + stop_metric.go 两个必要单职责文件 (33→35)，与 archive.go 单职责先例对齐；ADR-017 v1.2 A1（DAG turn.completed subscriber + thread.stopped fallback）按 §2.9 + §2.5 拍板新建 5 个必要单职责文件：dag_turn_completed_subscriber.go + dag_subscriber_module.go + dag_subscriber_metric.go + dispatch_agent_running_metric.go + dag_fallback_metric.go (35→40)，同样与 archive / stop_helper 单职责设计一致；后续 lifecycle 子包拆出（含 stop_helper + stop_metric + dag_subscriber + dag_fallback + dispatch_agent metrics）后取消。",
		Owner:      "orchestration",
		RemoveWhen: "orchestration 包拆出 node-dispatch / lifecycle / dag-subscription 子包（含 stop_helper + stop_metric + archive + dag_turn_completed_subscriber + dag_subscriber_module + dag_subscriber_metric + dag_fallback_metric + dispatch_agent_running_metric 等 lifecycle / subscriber / metric helper）后文件数回落 ≤ 30，删除该 freeze。",
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
