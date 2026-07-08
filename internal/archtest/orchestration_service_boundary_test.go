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
	packageAliases := orchestrationServicePackageAliases(t, root)
	var violations []string
	for _, absPath := range walkGoFiles(t, root, "cmd", "internal") {
		relPath, err := filepath.Rel(root, absPath)
		if err != nil {
			t.Fatalf("rel path for %s: %v", absPath, err)
		}
		relPath = filepath.ToSlash(relPath)
		count := countOrchestrationServiceSelectors(t, absPath, packageAliases[filepath.Dir(absPath)])
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
		"cmd/mcp-orch/orchestration/service.go":    compat(2, "production facade may only re-export Service and provide the interface"),
		"cmd/mcp-orch/orchestration/rpc.go":        compat(3, "legacy RPC facade consumes the package Service alias until split"),
		"cmd/mcp-orch/runtime.go":                  compat(2, "mcp-orch registry compatibility adapter fans out to narrower tool handlers"),
		"internal/app/dashboard_adapter.go":        compat(1, "temporary app provider narrows full service to dashboard read port"),
		"internal/app/runtime_reporter_adapter.go": compat(1, "temporary app provider narrows full service to runtime update port"),
	}
}

func countOrchestrationServiceSelectors(t *testing.T, absPath string, packageAliases map[string]bool) int {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, absPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", absPath, err)
	}
	contractAliases := contractImportAliases(t, absPath, file)
	return countOrchestrationServiceSelectorUses(file, contractAliases, packageAliases)
}

func orchestrationServicePackageAliases(t *testing.T, root string) map[string]map[string]bool {
	t.Helper()

	aliasesByDir := map[string]map[string]bool{}
	for _, absPath := range walkGoFiles(t, root, "cmd", "internal") {
		file, err := parser.ParseFile(token.NewFileSet(), absPath, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", absPath, err)
		}
		contractAliases := contractImportAliases(t, absPath, file)
		if len(contractAliases) == 0 {
			continue
		}
		localAliases := orchestrationServiceLocalAliases(file, contractAliases)
		if len(localAliases) == 0 {
			continue
		}
		dir := filepath.Dir(absPath)
		aliases := aliasesByDir[dir]
		if aliases == nil {
			aliases = map[string]bool{}
			aliasesByDir[dir] = aliases
		}
		for name := range localAliases {
			aliases[name] = true
		}
	}
	return aliasesByDir
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

func countOrchestrationServiceSelectorUses(file *ast.File, contractAliases map[string]bool, packageAliases map[string]bool) int {
	if len(contractAliases) == 0 && len(packageAliases) == 0 {
		return 0
	}
	localAliases := orchestrationServiceLocalAliases(file, contractAliases)
	for name := range packageAliases {
		localAliases[name] = true
	}
	count := 0
	ast.Inspect(file, func(n ast.Node) bool {
		switch typed := n.(type) {
		case *ast.TypeSpec:
			count += countOrchestrationServiceTypeExpr(typed.Type, contractAliases, localAliases)
			return false
		case *ast.Field:
			count += countOrchestrationServiceTypeExpr(typed.Type, contractAliases, localAliases)
			return false
		case *ast.ValueSpec:
			if typed.Type != nil {
				count += countOrchestrationServiceTypeExpr(typed.Type, contractAliases, localAliases)
			}
			return false
		}
		return true
	})
	return count
}

func countOrchestrationServiceTypeExpr(expr ast.Expr, contractAliases map[string]bool, localAliases map[string]bool) int {
	count := 0
	ast.Inspect(expr, func(n ast.Node) bool {
		switch typed := n.(type) {
		case *ast.Field:
			count += countOrchestrationServiceTypeExpr(typed.Type, contractAliases, localAliases)
			return false
		case *ast.SelectorExpr:
			if isOrchestrationServiceSelector(typed, contractAliases) {
				count++
			}
		case *ast.Ident:
			if localAliases[typed.Name] {
				count++
			}
		}
		return true
	})
	return count
}

func TestCountOrchestrationServiceSelectorUsesCountsLocalAliases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want int
	}{
		{
			name: "local alias declaration and field parameter return uses",
			src: `package fixture

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type OS = contract.OrchestrationService
type wrapper struct {
	service OS
}

func use(svc OS) OS {
	return svc
}
`,
			want: 4,
		},
		{
			name: "direct selector field parameter return uses",
			src: `package fixture

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type wrapper struct {
	service contract.OrchestrationService
}

func use(svc contract.OrchestrationService) contract.OrchestrationService {
	return svc
}
`,
			want: 3,
		},
		{
			name: "facade allows service alias and interface provider return",
			src: `package fixture

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService

type service struct{}

func ProvideServiceInterface(s *service) Service { return s }
`,
			want: 2,
		},
		{
			name: "facade fixture would fail on extra full service field",
			src: `package fixture

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService

type holder struct {
	service Service
}

type service struct{}

func ProvideServiceInterface(s *service) Service { return s }
`,
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", tt.src, parser.SkipObjectResolution)
			if err != nil {
				t.Fatalf("parse fixture: %v", err)
			}
			contractAliases := contractImportAliases(t, "fixture.go", file)
			got := countOrchestrationServiceSelectorUses(file, contractAliases, nil)
			if got != tt.want {
				t.Fatalf("countOrchestrationServiceSelectorUses() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestCountOrchestrationServiceSelectorUsesCountsPackageAliases(t *testing.T) {
	t.Parallel()

	src := `package fixture

type holder struct {
	service Service
}

func use(svc Service) Service {
	return svc
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "consumer.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	got := countOrchestrationServiceSelectorUses(file, nil, map[string]bool{"Service": true})
	const want = 3
	if got != want {
		t.Fatalf("countOrchestrationServiceSelectorUses() = %d, want %d; package alias use must consume the boundary budget", got, want)
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
