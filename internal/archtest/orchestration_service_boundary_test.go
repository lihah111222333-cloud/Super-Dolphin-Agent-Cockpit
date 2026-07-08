package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"testing"
)

type orchestrationServiceAllowance struct {
	max    int
	reason string
}

func TestOrchestrationServiceConsumersUseNarrowPorts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	allowances := allowedOrchestrationServiceConsumers()
	var violations []string
	for _, absPath := range walkGoFiles(t, root, "cmd", "internal") {
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			t.Fatalf("rel path for %s: %v", absPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		count := countOrchestrationServiceSelectors(t, absPath)
		if count == 0 {
			continue
		}
		allowance, ok := allowances[relPath]
		if !ok {
			violations = append(violations, fmt.Sprintf("%s directly consumes contract.OrchestrationService %d time(s); split it behind a narrow contract port", relPath, count))
			continue
		}
		if count > allowance.max {
			violations = append(violations, fmt.Sprintf("%s directly consumes contract.OrchestrationService %d time(s), max %d (%s)", relPath, count, allowance.max, allowance.reason))
		}
	}
	failIfViolations(t, violations)
}

func allowedOrchestrationServiceConsumers() map[string]orchestrationServiceAllowance {
	compat := func(max int, reason string) orchestrationServiceAllowance {
		return orchestrationServiceAllowance{max: max, reason: reason}
	}
	return map[string]orchestrationServiceAllowance{
		"cmd/mcp-orch/orchestration/service.go":                compat(4, "production implementation facade re-exports and exposes the full service"),
		"cmd/mcp-orch/runtime.go":                              compat(1, "mcp-orch registry compatibility adapter fans out to narrower tool handlers"),
		"cmd/mcp-orch/tools/registry.go":                       compat(1, "tool registry compatibility adapter keeps legacy dependency shape"),
		"cmd/mcp-orch/tools/orchestration_tool_definitions.go": compat(1, "orchestration tool definition fanout adapter"),
		"cmd/mcp-orch/tools/task_tool_definitions.go":          compat(3, "task tool definition fanout adapter"),
		"cmd/mcp-orch/tools/orchestration_tools.go":            compat(10, "legacy launch/send/list helpers pending narrow-port migration"),
		"cmd/mcp-orch/tools/orchestration_send_message.go":     compat(6, "send_message spans report, turn submission, and lifecycle lookup until split"),
		"cmd/mcp-orch/tools/task_apply_ops.go":                 compat(1, "DAG ops handler pending DAG runtime split"),
		"cmd/mcp-orch/tools/task_diagnostics.go":               compat(6, "workflow diagnostics spans DAG runtime and run lookup until split"),
		"cmd/mcp-orch/tools/task_lifecycle_helpers.go":         compat(1, "run lifecycle helper pending DAG runtime split"),
		"cmd/mcp-orch/tools/task_tools.go":                     compat(9, "legacy DAG handlers pending DAG runtime split"),
		"cmd/mcp-orch/tools/workflow_workbench.go":             compat(7, "workflow workbench compatibility surface pending DAG runtime split"),
		"internal/app/runtime_reporter_adapter.go":             compat(2, "explicit app adapter narrows full service to RuntimeReporter"),
		"internal/module/dashboard/module.go":                  compat(1, "legacy optional dashboard input pending read-model port split"),
		"internal/module/dashboard/service.go":                 compat(3, "legacy dashboard read-model adapter pending narrow port split"),
		"internal/module/memory/module.go":                     compat(2, "legacy memory runtime bridge pending narrow state port split"),
		"internal/module/uistate/module.go":                    compat(1, "legacy optional uistate input pending read-model port split"),
		"internal/module/uistate/service.go":                   compat(3, "legacy uistate read-model adapter pending narrow port split"),
		"internal/platform/mcpcontrol/handlers.go":             compat(1, "mcpcontrol context resolver compatibility adapter"),
		"internal/platform/mcpcontrol/module.go":               compat(2, "mcpcontrol optional report handler adapter"),
		"internal/platform/mcpcontrol/report_handlers.go":      compat(2, "mcpcontrol completion/report compatibility adapter"),
	}
}

func countOrchestrationServiceSelectors(t *testing.T, absPath string) int {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", absPath, err)
	}
	contractAliases := contractImportAliases(t, absPath, file)
	return countOrchestrationServiceSelectorUses(file, contractAliases)
}

func contractImportAliases(t *testing.T, absPath string, file *ast.File) map[string]bool {
	t.Helper()

	contractAliases := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote import %s: %v", spec.Path.Value, err)
		}
		if path != modulePath+"/internal/contract" {
			continue
		}
		if spec.Name == nil {
			contractAliases["contract"] = true
			continue
		}
		switch spec.Name.Name {
		case ".":
			t.Fatalf("%s dot-imports internal/contract; use explicit contract.<Type> imports", absPath)
		case "_":
			continue
		default:
			contractAliases[spec.Name.Name] = true
		}
	}
	return contractAliases
}

func countOrchestrationServiceSelectorUses(file *ast.File, contractAliases map[string]bool) int {
	if len(contractAliases) == 0 {
		return 0
	}
	localAliases := orchestrationServiceLocalAliases(file, contractAliases)
	count := 0
	ast.Inspect(file, func(n ast.Node) bool {
		selector, ok := n.(*ast.SelectorExpr)
		if ok {
			if selector.Sel.Name != "OrchestrationService" {
				return true
			}
			base, ok := selector.X.(*ast.Ident)
			if ok && contractAliases[base.Name] {
				count++
			}
			return true
		}

		ident, ok := n.(*ast.Ident)
		if ok && localAliases[ident.Name] && !isTypeSpecName(file, ident) {
			count++
		}
		return true
	})
	return count
}

func TestCountOrchestrationServiceSelectorUsesCountsLocalAliases(t *testing.T) {
	t.Parallel()

	src := `package fixture

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type OS = contract.OrchestrationService
type wrapper struct {
	service OS
}

func use(svc OS) OS {
	return svc
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	contractAliases := contractImportAliases(t, "fixture.go", file)
	got := countOrchestrationServiceSelectorUses(file, contractAliases)
	const want = 4
	if got != want {
		t.Fatalf("countOrchestrationServiceSelectorUses() = %d, want %d; local alias use must consume the boundary budget", got, want)
	}
}

func orchestrationServiceLocalAliases(file *ast.File, contractAliases map[string]bool) map[string]bool {
	aliases := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if isOrchestrationServiceSelector(typeSpec.Type, contractAliases) {
				aliases[typeSpec.Name.Name] = true
			}
		}
	}
	return aliases
}

func isOrchestrationServiceSelector(expr ast.Expr, contractAliases map[string]bool) bool {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != "OrchestrationService" {
		return false
	}
	base, ok := selector.X.(*ast.Ident)
	return ok && contractAliases[base.Name]
}

func isTypeSpecName(file *ast.File, ident *ast.Ident) bool {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if ok && typeSpec.Name == ident {
				return true
			}
		}
	}
	return false
}
