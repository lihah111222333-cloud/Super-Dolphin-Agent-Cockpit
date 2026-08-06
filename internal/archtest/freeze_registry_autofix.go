package archtest

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
)

// FreezeRegistryAutoFix 描述一次 freeze registry 自动收缩或删除计划。
// 这些字段只用于守卫报告和源码重写，不承载生产运行时配置。
type FreezeRegistryAutoFix struct {
	Path         string
	Kind         string
	Action       string
	OldLimit     int
	NewLimit     int
	Observed     int
	DefaultLimit int
}

// String 输出 freeze 自动修复的操作说明。
func (f FreezeRegistryAutoFix) String() string {
	switch f.Action {
	case "delete":
		return fmt.Sprintf("删除 freeze %s (%s): 当前值 %d 已回落到默认预算 %d", f.Path, f.Kind, f.Observed, f.DefaultLimit)
	case "shrink":
		return fmt.Sprintf("收缩 freeze %s (%s): %d -> %d", f.Path, f.Kind, f.OldLimit, f.NewLimit)
	default:
		return fmt.Sprintf("%s freeze %s (%s)", f.Action, f.Path, f.Kind)
	}
}

// AutoRepairFreezeRegistry 收缩或删除已经不再需要的 freeze 条目。
func AutoRepairFreezeRegistry(opts CheckOptions) ([]FreezeRegistryAutoFix, error) {
	repoRoot := opts.RepoRoot
	if repoRoot == "" {
		repoRoot = "."
	}
	scanRoots := opts.scanRoots()
	stats := make(map[string]*packageStat)
	for _, root := range scanRoots {
		scanRoot(repoRoot, root, opts.skipDirs(), stats, false)
	}
	planned, entries := planFreezeRegistryAutoFixes(repoRoot, scanRoots, stats)
	if len(planned) == 0 {
		return nil, nil
	}
	if err := rewriteFreezeRegistrySource(repoRoot, entries); err != nil {
		return nil, err
	}
	return planned, nil
}

func planFreezeRegistryAutoFixes(repoRoot string, scanRoots []string, stats map[string]*packageStat) ([]FreezeRegistryAutoFix, []explicitFreeze) {
	return planFreezeRegistryAutoFixesForEntries(repoRoot, scanRoots, stats, explicitFreezeRegistry())
}

func planFreezeRegistryAutoFixesForEntries(repoRoot string, scanRoots []string, stats map[string]*packageStat, entries []explicitFreeze) ([]FreezeRegistryAutoFix, []explicitFreeze) {
	fixes := make([]FreezeRegistryAutoFix, 0)
	next := make([]explicitFreeze, 0, len(entries))
	for _, entry := range entries {
		fix, keep, updated := planFreezeRegistryAutoFix(repoRoot, scanRoots, stats, entry)
		if fix.Action != "" {
			fixes = append(fixes, fix)
		}
		if keep {
			next = append(next, updated)
		}
	}
	return fixes, next
}

// planFreezeRegistryAutoFix 决定单个 freeze 条目应该保留、收缩还是删除。
func planFreezeRegistryAutoFix(repoRoot string, scanRoots []string, stats map[string]*packageStat, entry explicitFreeze) (FreezeRegistryAutoFix, bool, explicitFreeze) {
	if !freezeAppliesToScanRoots(entry.Path, scanRoots) {
		return FreezeRegistryAutoFix{}, true, entry
	}
	defaultLimit, ok := defaultFreezeLimit(entry.Kind)
	if !ok {
		return FreezeRegistryAutoFix{}, true, entry
	}
	kind := violationKindLabel(entry.Kind)
	observed, exists := observedFreezeMetric(repoRoot, entry, stats)
	if !exists || entry.Limit <= defaultLimit || observed <= defaultLimit {
		return FreezeRegistryAutoFix{
			Path:         entry.Path,
			Kind:         kind,
			Action:       "delete",
			OldLimit:     entry.Limit,
			Observed:     observed,
			DefaultLimit: defaultLimit,
		}, false, explicitFreeze{}
	}
	if observed < entry.Limit {
		oldLimit := entry.Limit
		entry.Limit = observed
		return FreezeRegistryAutoFix{
			Path:         entry.Path,
			Kind:         kind,
			Action:       "shrink",
			OldLimit:     oldLimit,
			NewLimit:     observed,
			Observed:     observed,
			DefaultLimit: defaultLimit,
		}, true, entry
	}
	return FreezeRegistryAutoFix{}, true, entry
}

func rewriteFreezeRegistrySource(repoRoot string, entries []explicitFreeze) error {
	path := filepath.Join(repoRoot, "internal/archtest/freeze_registry.go")
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	start, end, err := findExplicitFreezeRegistryOffsets(path, src)
	if err != nil {
		return err
	}
	replacement := renderExplicitFreezeRegistry(entries)
	updated := append([]byte(nil), src[:start]...)
	updated = append(updated, replacement...)
	updated = append(updated, src[end:]...)
	formatted, err := format.Source(updated)
	if err != nil {
		return err
	}
	return os.WriteFile(path, formatted, 0o644)
}

// findExplicitFreezeRegistryOffsets 定位 freeze registry 字面量在源码里的范围。
func findExplicitFreezeRegistryOffsets(path string, src []byte) (int, int, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return 0, 0, err
	}
	for _, decl := range file.Decls {
		function, ok := decl.(*ast.FuncDecl)
		if !ok || function.Name.Name != "explicitFreezeRegistry" {
			continue
		}
		return explicitFreezeRegistryReturnOffsets(fset, function)
	}
	return 0, 0, fmt.Errorf("explicitFreezeRegistry snapshot function not found in %s", path)
}

// explicitFreezeRegistryReturnOffsets 定位快照函数中唯一的返回字面量范围。
func explicitFreezeRegistryReturnOffsets(fset *token.FileSet, function *ast.FuncDecl) (int, int, error) {
	for _, statement := range function.Body.List {
		result, ok := statement.(*ast.ReturnStmt)
		if !ok || len(result.Results) != 1 {
			continue
		}
		value := result.Results[0]
		return fset.Position(value.Pos()).Offset, fset.Position(value.End()).Offset, nil
	}
	return 0, 0, fmt.Errorf("explicitFreezeRegistry snapshot literal not found")
}

func renderExplicitFreezeRegistry(entries []explicitFreeze) []byte {
	var buf bytes.Buffer
	buf.WriteString("[]explicitFreeze{\n")
	for _, entry := range entries {
		fmt.Fprintf(&buf, "\t{\n")
		fmt.Fprintf(&buf, "\t\tPath:       %q,\n", entry.Path)
		fmt.Fprintf(&buf, "\t\tKind:       %s,\n", violationKindConst(entry.Kind))
		fmt.Fprintf(&buf, "\t\tLimit:      %d,\n", entry.Limit)
		fmt.Fprintf(&buf, "\t\tReason:     %q,\n", entry.Reason)
		fmt.Fprintf(&buf, "\t\tOwner:      %q,\n", entry.Owner)
		fmt.Fprintf(&buf, "\t\tRemoveWhen: %q,\n", entry.RemoveWhen)
		fmt.Fprintf(&buf, "\t},\n")
	}
	buf.WriteString("}")
	return buf.Bytes()
}

// violationKindConst 输出重写 freeze registry 时需要写回源码的常量名。
func violationKindConst(kind ViolationKind) string {
	switch kind {
	case ViolationFile:
		return "ViolationFile"
	case ViolationFunc:
		return "ViolationFunc"
	case ViolationNesting:
		return "ViolationNesting"
	case ViolationCC:
		return "ViolationCC"
	case ViolationIdentifier:
		return "ViolationIdentifier"
	case ViolationPackageCount:
		return "ViolationPackageCount"
	case ViolationDeadKey:
		return "ViolationDeadKey"
	case ViolationFuncComment:
		return "ViolationFuncComment"
	default:
		return fmt.Sprintf("ViolationKind(%d)", kind)
	}
}
