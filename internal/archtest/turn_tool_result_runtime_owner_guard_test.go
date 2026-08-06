package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/archtest"
)

// TestTurnToolResultRuntimeOwnerDoesNotRegressToPackageGlobals 防止 ToolResult 预算和生命周期状态退回 package singleton。
func TestTurnToolResultRuntimeOwnerDoesNotRegressToPackageGlobals(t *testing.T) {
	root := repoRoot(t)
	files := map[string]*ast.File{}
	for _, relative := range turnToolResultRuntimeFiles() {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if got := archtest.MeasureFileMetrics(path).GlobalVars; got != 0 {
			t.Fatalf("%s has %d mutable package globals, want 0", relative, got)
		}
		files[relative] = parseTurnToolResultRuntimeFile(t, path)
	}
	if violations := turnToolResultRuntimeViolations(files); len(violations) > 0 {
		t.Fatalf("ToolResult runtime owner violations:\n%s", strings.Join(violations, "\n"))
	}
}

// TestTurnToolResultRuntimeOwnerGuardRejectsSyntheticRegression 证明守卫能拒绝 global 回流和不完整 owner。
func TestTurnToolResultRuntimeOwnerGuardRejectsSyntheticRegression(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "tool_result_storage.go", `package turn
var defaultToolResultBudgetRegistry = &toolResultBudgetRegistry{}
type ToolResultRuntime struct { budget *toolResultBudgetRegistry }
`, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse synthetic ToolResult regression: %v", err)
	}
	violations := turnToolResultRuntimeViolations(map[string]*ast.File{
		"internal/module/turn/tool_result_storage.go": file,
	})
	for _, want := range []string{"package mutable global", "must own lifecycle *toolResultLifecycleRegistry"} {
		if !containsTurnToolResultRuntimeViolation(violations, want) {
			t.Fatalf("synthetic regression missing %q; got %v", want, violations)
		}
	}
}

// turnToolResultRuntimeFiles 返回必须保持无 package mutable global 的 ToolResult runtime 文件。
func turnToolResultRuntimeFiles() []string {
	return []string{
		"internal/module/turn/tool_result_budget.go",
		"internal/module/turn/tool_result_lifecycle.go",
		"internal/module/turn/tool_result_storage.go",
	}
}

// parseTurnToolResultRuntimeFile 解析守卫目标源码，解析失败即阻断检查。
func parseTurnToolResultRuntimeFile(t *testing.T, path string) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

// turnToolResultRuntimeViolations 收集 package global 和 owner 字段缺失的违规。
func turnToolResultRuntimeViolations(files map[string]*ast.File) []string {
	violations := []string{}
	for relative, file := range files {
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if ok && gen.Tok == token.VAR {
				violations = append(violations, fmt.Sprintf("%s: package mutable global is forbidden", relative))
			}
		}
	}
	storage := files["internal/module/turn/tool_result_storage.go"]
	if !turnToolResultRuntimeHasField(storage, "budget", "toolResultBudgetRegistry") {
		violations = append(violations, "ToolResultRuntime must own budget *toolResultBudgetRegistry")
	}
	if !turnToolResultRuntimeHasField(storage, "lifecycle", "toolResultLifecycleRegistry") {
		violations = append(violations, "ToolResultRuntime must own lifecycle *toolResultLifecycleRegistry")
	}
	return violations
}

// turnToolResultRuntimeHasField 确认 ToolResultRuntime 持有指定私有 registry 指针字段。
func turnToolResultRuntimeHasField(file *ast.File, wantField, wantType string) bool {
	if file == nil {
		return false
	}
	for _, decl := range file.Decls {
		if turnToolResultDeclHasField(decl, wantField, wantType) {
			return true
		}
	}
	return false
}

func turnToolResultDeclHasField(decl ast.Decl, wantField, wantType string) bool {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.TYPE {
		return false
	}
	for _, spec := range gen.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if !ok || typeSpec.Name.Name != "ToolResultRuntime" {
			continue
		}
		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range structType.Fields.List {
			if turnToolResultRuntimeFieldMatches(field, wantField, wantType) {
				return true
			}
		}
	}
	return false
}

// turnToolResultRuntimeFieldMatches 匹配指定名称和私有 registry 指针类型。
func turnToolResultRuntimeFieldMatches(field *ast.Field, wantField, wantType string) bool {
	if len(field.Names) != 1 || field.Names[0].Name != wantField {
		return false
	}
	pointer, ok := field.Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	ident, ok := pointer.X.(*ast.Ident)
	return ok && ident.Name == wantType
}

// containsTurnToolResultRuntimeViolation 返回违规列表是否包含指定文本。
func containsTurnToolResultRuntimeViolation(violations []string, want string) bool {
	for _, violation := range violations {
		if strings.Contains(violation, want) {
			return true
		}
	}
	return false
}
