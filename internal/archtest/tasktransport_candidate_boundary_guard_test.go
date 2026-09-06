package archtest_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

const (
	taskTransportCandidatePackage = "cmd/mcp-orch/orchestration/tasktransport"
	taskTransportCandidateImport  = modulePath + "/" + taskTransportCandidatePackage
	taskTransportCommonImport     = modulePath + "/internal/mcpserver/common"
)

// TestTaskTransportCandidateProductionImportsStayTransportNeutral 锁定候选实现
// 的依赖面：它只能使用标准库与 common attribution/scope seam，不能偷偷获得
// runtime、registry、Fx、provider、EventStore 或 unified_v2 的生产能力。
func TestTaskTransportCandidateProductionImportsStayTransportNeutral(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := parseImportFiles(t, root, taskTransportCandidatePackage)
	if len(files) == 0 {
		t.Fatalf("candidate package %q has no production Go files", taskTransportCandidatePackage)
	}

	var violations []string
	for _, file := range files {
		for _, imported := range file.Imports {
			if imported == taskTransportCommonImport || isTaskTransportStdlibImport(imported) {
				continue
			}
			violations = append(violations, file.RelPath+": candidate import "+imported+" is outside common attribution/scope and the standard library")
		}
	}
	failIfViolations(t, violations)
}

// TestTaskTransportCandidateIsNotWiredIntoProduction 扫描所有生产 Go 文件，防止
// 候选被 runtime、registry、Fx、provider、EventStore 或 unified_v2（及其他
// 未审计生产组件）导入。候选行为测试不属于生产 wiring，因此不会被阻断。
func TestTaskTransportCandidateIsNotWiredIntoProduction(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := parseImportFiles(t, root, "internal", "cmd", "pkg", "scripts")
	if len(files) == 0 {
		t.Fatal("production Go scan found no files")
	}

	var violations []string
	for _, file := range files {
		if file.RelPath == taskTransportCandidatePackage || strings.HasPrefix(file.RelPath, taskTransportCandidatePackage+"/") {
			continue
		}
		for _, imported := range file.Imports {
			if imported == taskTransportCandidateImport || strings.HasPrefix(imported, taskTransportCandidateImport+"/") {
				violations = append(violations, file.RelPath+": production code imports candidate "+imported)
			}
		}
	}
	failIfViolations(t, violations)
}

// TestTaskTransportCandidateBoundaryFixturesRejectUnapprovedImports keeps the
// guard itself fail-closed: a future edit that adds an EventStore/Fx/provider
// dependency must be rejected even before it reaches the real candidate tree.
func TestTaskTransportCandidateBoundaryFixturesRejectUnapprovedImports(t *testing.T) {
	t.Parallel()

	for _, fixture := range []struct {
		name    string
		imports []string
		want    bool
	}{
		{name: "common attribution scope", imports: []string{taskTransportCommonImport}},
		{name: "standard library", imports: []string{"context", "strings"}},
		{name: "runtime", imports: []string{modulePath + "/cmd/mcp-orch"}, want: true},
		{name: "registry", imports: []string{modulePath + "/cmd/mcp-orch/tools"}, want: true},
		{name: "event store", imports: []string{modulePath + "/cmd/mcp-orch/store/eventstore"}, want: true},
		{name: "fx", imports: []string{"go.uber.org/fx"}, want: true},
		{name: "provider", imports: []string{modulePath + "/internal/provider/unified"}, want: true},
		{name: "unified v2", imports: []string{modulePath + "/cmd/mcp-orch/tools/unifiedv2"}, want: true},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root := t.TempDir()
			relPath := filepath.ToSlash(filepath.Join(taskTransportCandidatePackage, "fixture.go"))
			path := writeBackendBoundaryFixtureAt(t, root, relPath, taskTransportFixtureSource(fixture.imports))
			imports := parseTaskTransportFixtureImports(t, path)
			violations := taskTransportCandidateImportViolations(relPath, imports)
			if fixture.want && len(violations) == 0 {
				t.Fatalf("unapproved imports %v were accepted", fixture.imports)
			}
			if !fixture.want && len(violations) != 0 {
				t.Fatalf("approved imports %v were rejected: %v", fixture.imports, violations)
			}
		})
	}
}

func taskTransportCandidateImportViolations(relPath string, imports []string) []string {
	var violations []string
	for _, imported := range imports {
		if imported == taskTransportCommonImport || isTaskTransportStdlibImport(imported) {
			continue
		}
		violations = append(violations, relPath+": candidate import "+imported+" is outside common attribution/scope and the standard library")
	}
	return violations
}

func taskTransportFixtureSource(imports []string) string {
	var source strings.Builder
	source.WriteString("package tasktransport\n\nimport (\n")
	for index, imported := range imports {
		source.WriteString("\tfixture")
		source.WriteString(string(rune('A' + index)))
		source.WriteString(" \"" + imported + "\"\n")
	}
	source.WriteString(")\n")
	return source.String()
}

func parseTaskTransportFixtureImports(t *testing.T, path string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse tasktransport fixture %s: %v", path, err)
	}
	imports := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		imports = append(imports, strings.Trim(spec.Path.Value, `\"`))
	}
	return imports
}

func isTaskTransportStdlibImport(importPath string) bool {
	first, _, _ := strings.Cut(importPath, "/")
	return first != "" && !strings.Contains(first, ".")
}
