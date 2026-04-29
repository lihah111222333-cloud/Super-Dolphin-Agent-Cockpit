package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

type interfaceBudget struct {
	relPath     string
	name        string
	maxMethods  int
	maxEmbedded int
}

func TestInterfaceIsolationBudgets(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	budgets := []interfaceBudget{
		{relPath: "cmd/mcp-orch/store/taskdag/contract.go", name: "Store", maxMethods: 0, maxEmbedded: 6},
		{relPath: "cmd/mcp-orch/store/taskdag/contract.go", name: "OrchestrationStore", maxMethods: 0, maxEmbedded: 3},
		{relPath: "cmd/mcp-orch/store/taskdag/contract.go", name: "DAGMutationStore", maxMethods: 2, maxEmbedded: 1},
		{relPath: "cmd/mcp-orch/store/taskdag/contract.go", name: "DAGReadStore", maxMethods: 1, maxEmbedded: 1},
		{relPath: "cmd/mcp-orch/store/taskdag/contract.go", name: "RunningNodeStore", maxMethods: 6, maxEmbedded: 1},
		{relPath: "cmd/mcp-orch/store/taskdag/contract.go", name: "WakeupStore", maxMethods: 10, maxEmbedded: 0},
		{relPath: "cmd/mcp-orch/store/taskdag/contract.go", name: "WorkerLeaseStore", maxMethods: 3, maxEmbedded: 0},
		{relPath: "internal/module/skill/contract.go", name: "Service", maxMethods: 0, maxEmbedded: 13},
		{relPath: "cmd/mcp-lsp/gopls/manager.go", name: "Manager", maxMethods: 0, maxEmbedded: 3},
		{relPath: "cmd/mcp-lsp/manager/manager.go", name: "Manager", maxMethods: 0, maxEmbedded: 8},
	}

	var violations []string
	for _, budget := range budgets {
		methods, embedded, ok := interfaceShape(t, root, budget.relPath, budget.name)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s: interface %s not found", budget.relPath, budget.name))
			continue
		}
		if methods > budget.maxMethods || embedded > budget.maxEmbedded {
			violations = append(violations, fmt.Sprintf(
				"%s:%s has %d direct methods / %d embedded ports, budget is <=%d / <=%d; split consumers before adding methods",
				budget.relPath, budget.name, methods, embedded, budget.maxMethods, budget.maxEmbedded,
			))
		}
	}
	failIfViolations(t, violations)
}

func TestTaskDAGStoreConsumersUseNarrowPort(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	const relPath = "cmd/mcp-orch/orchestration/service.go"
	var violations []string
	for _, field := range []struct {
		structName string
		fieldName  string
	}{
		{structName: "service", fieldName: "dagStore"},
		{structName: "serviceParams", fieldName: "DAGStore"},
	} {
		actual, ok := structFieldType(t, root, relPath, field.structName, field.fieldName)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s: %s.%s not found", relPath, field.structName, field.fieldName))
			continue
		}
		if actual != "taskdag.OrchestrationStore" {
			violations = append(violations, fmt.Sprintf("%s: %s.%s must depend on taskdag.OrchestrationStore, got %s", relPath, field.structName, field.fieldName, actual))
		}
	}
	failIfViolations(t, violations)
}

func TestSkillServiceConsumersUseNarrowPorts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	checks := []struct {
		relPath    string
		structName string
		fieldName  string
		want       string
	}{
		{relPath: "internal/module/dashboard/service.go", structName: "service", fieldName: "skills", want: "skillmodule.SkillLister"},
		{relPath: "internal/module/dashboard/module.go", structName: "serviceParams", fieldName: "Skills", want: "skillmodule.SkillLister"},
		// skillCatalogProviderDeps + registerSkillCatalogDeps removed in skill refactor P2 Task 7;
		// SkillCatalogProvider is gone, Claude uses native discovery via workspace symlink instead.
		{relPath: "internal/platform/toolbridge/host_tools.go", structName: "SkillHostTools", fieldName: "svc", want: "skillpkg.SkillHostToolReader"},
	}
	var violations []string
	for _, check := range checks {
		actual, ok := structFieldType(t, root, check.relPath, check.structName, check.fieldName)
		if !ok {
			violations = append(violations, fmt.Sprintf("%s: %s.%s not found", check.relPath, check.structName, check.fieldName))
			continue
		}
		if actual != check.want {
			violations = append(violations, fmt.Sprintf("%s: %s.%s must depend on %s, got %s", check.relPath, check.structName, check.fieldName, check.want, actual))
		}
	}

	if actual, ok := functionParamType(t, root, "internal/module/turn/service.go", "NewServiceWithPromptAssemblyAndTurnContext", "skillSvc"); !ok {
		violations = append(violations, "internal/module/turn/service.go: NewServiceWithPromptAssemblyAndTurnContext.skillSvc not found")
	} else if actual != "skillpkg.SkillHydrationSource" {
		violations = append(violations, fmt.Sprintf("internal/module/turn/service.go: NewServiceWithPromptAssemblyAndTurnContext.skillSvc must depend on skillpkg.SkillHydrationSource, got %s", actual))
	}
	// provideHostToolRegistry.svc check removed in skill refactor P3 Task 3:
	// provideHostToolRegistry no longer takes a SkillHostToolReader; the Codex tool
	// registry now wraps SkillReadSectionTool via skilllibrary.Config.CacheDir.
	failIfViolations(t, violations)
}

func interfaceShape(t *testing.T, root, relPath, name string) (methods int, embedded int, ok bool) {
	t.Helper()
	file := parseGoFileForInterfaceGuard(t, root, relPath)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != name {
				continue
			}
			iface, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				return 0, 0, false
			}
			for _, field := range iface.Methods.List {
				if len(field.Names) == 0 {
					embedded++
					continue
				}
				methods += len(field.Names)
			}
			return methods, embedded, true
		}
	}
	return 0, 0, false
}

func structFieldType(t *testing.T, root, relPath, structName, fieldName string) (string, bool) {
	t.Helper()
	file := parseGoFileForInterfaceGuard(t, root, relPath)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != structName {
				continue
			}
			st, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				return "", false
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					if name.Name == fieldName {
						return exprTypeString(field.Type), true
					}
				}
			}
		}
	}
	return "", false
}

func functionParamType(t *testing.T, root, relPath, funcName, paramName string) (string, bool) {
	t.Helper()
	file := parseGoFileForInterfaceGuard(t, root, relPath)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Type.Params == nil {
			continue
		}
		for _, field := range fn.Type.Params.List {
			for _, name := range field.Names {
				if name.Name == paramName {
					return exprTypeString(field.Type), true
				}
			}
		}
	}
	return "", false
}

func parseGoFileForInterfaceGuard(t *testing.T, root, relPath string) *ast.File {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relPath))
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	return file
}

func exprTypeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprTypeString(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + exprTypeString(e.X)
	case *ast.ArrayType:
		return "[]" + exprTypeString(e.Elt)
	default:
		return strings.TrimPrefix(fmt.Sprintf("%T", expr), "*ast.")
	}
}
