package archtest_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDesktopDependenciesStayOutOfHeadlessRuntime 防止桌面宿主依赖进入可 headless 运行的后端与 MCP sidecar。
// internal/app 是允许把 Wails 与 ui/wails 接到根 runtime 的装配边界，业务、provider、platform 和 sidecar 不应直接碰桌面层。
func TestDesktopDependenciesStayOutOfHeadlessRuntime(t *testing.T) {
	root := repoRoot(t)

	rules := []desktopDependencyRule{
		{
			Name:     "core runtime",
			RelRoots: []string{"internal/module", "internal/provider", "internal/platform"},
		},
		{
			Name:     "MCP sidecar",
			RelRoots: mcpSidecarRoots(t, root),
		},
	}
	for _, rule := range rules {
		t.Run(rule.Name, func(t *testing.T) {
			var violations []string
			for _, file := range parseImportFiles(t, root, rule.RelRoots...) {
				violations = append(violations, desktopDependencyViolations(rule.Name, file)...)
			}
			failIfViolations(t, violations)
		})
	}
}

type desktopDependencyRule struct {
	Name     string
	RelRoots []string
}

type desktopOnlyDependency struct {
	Name   string
	Prefix string
}

func desktopOnlyDependencies() []desktopOnlyDependency {
	return []desktopOnlyDependency{
		{Name: "Wails runtime", Prefix: "github.com/wailsapp/wails"},
		{Name: "Wails UI adapter", Prefix: internalPrefix("internal/ui/wails")},
		{Name: "legacy frontend embed tree", Prefix: internalPrefix("cmd/agent-terminal/frontend")},
	}
}

func desktopDependencyViolations(surface string, file parsedFile) []string {
	if desktopDependencyPathAllowed(file.RelPath) {
		return nil
	}

	var violations []string
	for _, imp := range file.Imports {
		for _, dependency := range desktopOnlyDependencies() {
			if importHasPrefix(imp, dependency.Prefix) {
				violations = append(violations, fmt.Sprintf(
					"%s imports desktop-only dependency %s (%s) in %s; keep desktop wiring in internal/app and Wails code in internal/ui/wails",
					file.RelPath,
					imp,
					dependency.Name,
					surface,
				))
				break
			}
		}
	}
	return violations
}

func desktopDependencyPathAllowed(relPath string) bool {
	return strings.HasPrefix(relPath, "internal/platform/ui/")
}

func importHasPrefix(importPath, prefix string) bool {
	trimmed := strings.TrimRight(prefix, "/")
	return importPath == trimmed || strings.HasPrefix(importPath, trimmed+"/")
}

func mcpSidecarRoots(t *testing.T, root string) []string {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(root, "cmd", "mcp-*"))
	if err != nil {
		t.Fatalf("glob cmd/mcp-* sidecar roots: %v", err)
	}

	var roots []string
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			t.Fatalf("stat MCP sidecar root %s: %v", match, err)
		}
		if !info.IsDir() {
			continue
		}
		rel, err := filepath.Rel(root, match)
		if err != nil {
			t.Fatalf("rel MCP sidecar root %s: %v", match, err)
		}
		roots = append(roots, filepath.ToSlash(rel))
	}
	if len(roots) == 0 {
		t.Fatalf("expected at least one cmd/mcp-* sidecar root")
	}
	return roots
}
