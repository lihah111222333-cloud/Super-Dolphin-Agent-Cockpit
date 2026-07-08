package archtest_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const superAgentModulePath = "github.com/anthropic-ai/super-agent-v3"
const orchestrationServiceContractPackagePath = superAgentModulePath + "/internal/contract"

type orchestrationServiceTypeUse struct {
	relPath string
	line    int
	kind    string
	name    string
	expr    string
}

type orchestrationServiceTypeGuardFixture struct {
	name         string
	files        map[string]string
	wantContains []string
}

func TestOrchestrationServiceTypeConsumersUseNarrowPorts(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	target := mustLoadOrchestrationServiceTypeObject(t, root)
	pkgs := loadOrchestrationServiceTypeGuardPackages(t, root, target.Pkg())
	var violations []string
	for _, pkg := range pkgs {
		if !isOrchestrationServiceTypeGuardProductionPackage(pkg) {
			continue
		}
		for _, use := range collectOrchestrationServiceTypeUses(pkg, target) {
			if isAllowedOrchestrationServiceTypeUse(use) {
				continue
			}
			violations = append(violations, use.violationMessage())
		}
	}
	sort.Strings(violations)
	failIfViolations(t, violations)
}

func TestOrchestrationServiceTypeGuardFixtures(t *testing.T) {
	t.Parallel()

	target := mustLoadOrchestrationServiceTypeObject(t, repoRoot(t))
	for _, tt := range orchestrationServiceTypeGuardFixtures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := typeCheckOrchestrationServiceFixturePackage(t, target.Pkg(), tt.files)
			got := orchestrationServiceTypeUseMessages(collectOrchestrationServiceTypeUses(pkg, target))
			for _, want := range tt.wantContains {
				if !containsViolation(got, want) {
					t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

func orchestrationServiceTypeGuardFixtures() []orchestrationServiceTypeGuardFixture {
	return []orchestrationServiceTypeGuardFixture{
		{
			name: "cross-file alias parameter",
			files: map[string]string{
				"cmd/mcp-orch/orchestration/alias.go": `package orchestration

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService
`,
				"cmd/mcp-orch/orchestration/consumer.go": `package orchestration

func use(svc Service) {}
`,
			},
			wantContains: []string{
				"cmd/mcp-orch/orchestration/alias.go:5 type alias Service uses full orchestration service",
				"cmd/mcp-orch/orchestration/consumer.go:3 parameter svc uses full orchestration service",
			},
		},
		{
			name: "cross-file alias generic constraint",
			files: map[string]string{
				"internal/module/dashboard/alias.go": `package dashboard

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService
`,
				"internal/module/dashboard/consumer.go": `package dashboard

type holder[T Service] struct{}
`,
			},
			wantContains: []string{
				"internal/module/dashboard/consumer.go:3 type parameter T uses full orchestration service",
			},
		},
		{
			name: "cross-file alias method expression initializer",
			files: map[string]string{
				"internal/platform/mcpcontrol/alias.go": `package mcpcontrol

import contract "github.com/anthropic-ai/super-agent-v3/internal/contract"

type Service = contract.OrchestrationService
`,
				"internal/platform/mcpcontrol/consumer.go": `package mcpcontrol

var submit = Service.SubmitTurn
`,
			},
			wantContains: []string{
				"internal/platform/mcpcontrol/consumer.go:3 method expression submit uses full orchestration service",
			},
		},
	}
}

func collectOrchestrationServiceTypeUses(pkg *orchestrationServiceCheckedPackage, target *types.TypeName) []orchestrationServiceTypeUse {
	if pkg == nil || pkg.typesInfo == nil || target == nil {
		return nil
	}

	contexts := orchestrationServiceIdentContexts(pkg.syntax)
	seen := map[string]bool{}
	var uses []orchestrationServiceTypeUse
	for ident, obj := range pkg.typesInfo.Defs {
		if !orchestrationServiceObjectUsesTarget(obj, target) {
			continue
		}
		addOrchestrationServiceTypeUse(&uses, seen, pkg.fset, ident, obj, contexts[ident])
	}
	for ident, obj := range pkg.typesInfo.Uses {
		if !orchestrationServiceObjectUsesTarget(obj, target) {
			continue
		}
		addOrchestrationServiceTypeUse(&uses, seen, pkg.fset, ident, obj, contexts[ident])
	}
	sortOrchestrationServiceTypeUses(uses)
	return uses
}

func sortOrchestrationServiceTypeUses(uses []orchestrationServiceTypeUse) {
	sort.Slice(uses, func(i, j int) bool {
		return orchestrationServiceTypeUseSortKey(uses[i]) < orchestrationServiceTypeUseSortKey(uses[j])
	})
}

func orchestrationServiceTypeUseSortKey(use orchestrationServiceTypeUse) string {
	return fmt.Sprintf("%s\x00%09d\x00%s\x00%s\x00%s", use.relPath, use.line, use.kind, use.name, use.expr)
}

type orchestrationServiceIdentContext struct {
	parents []ast.Node
}

func orchestrationServiceIdentContexts(files []*ast.File) map[*ast.Ident]orchestrationServiceIdentContext {
	contexts := map[*ast.Ident]orchestrationServiceIdentContext{}
	for _, file := range files {
		var stack []ast.Node
		ast.Inspect(file, func(node ast.Node) bool {
			if node == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if ident, ok := node.(*ast.Ident); ok {
				parents := make([]ast.Node, len(stack))
				copy(parents, stack)
				contexts[ident] = orchestrationServiceIdentContext{parents: parents}
			}
			stack = append(stack, node)
			return true
		})
	}
	return contexts
}

func addOrchestrationServiceTypeUse(
	uses *[]orchestrationServiceTypeUse,
	seen map[string]bool,
	fset *token.FileSet,
	ident *ast.Ident,
	obj types.Object,
	ctx orchestrationServiceIdentContext,
) {
	if ident == nil || obj == nil {
		return
	}
	use := classifyOrchestrationServiceTypeUse(fset, ident, obj, ctx)
	if use.kind == "" {
		return
	}
	key := strings.Join([]string{use.relPath, fmt.Sprint(use.line), use.kind, use.name, use.expr}, "\x00")
	if seen[key] {
		return
	}
	seen[key] = true
	*uses = append(*uses, use)
}

func classifyOrchestrationServiceTypeUse(
	fset *token.FileSet,
	ident *ast.Ident,
	obj types.Object,
	ctx orchestrationServiceIdentContext,
) orchestrationServiceTypeUse {
	position := fset.Position(ident.Pos())
	use := orchestrationServiceTypeUse{
		relPath: orchestrationServiceTypeRelPath(position.Filename),
		line:    position.Line,
		expr:    orchestrationServiceTypeExprString(fset, ident, ctx),
	}
	if kind, name, ok := orchestrationServiceMethodExpressionContext(ident, obj, ctx); ok {
		use.kind = kind
		use.name = name
		return use
	}
	if kind, name, ok := orchestrationServiceFieldContext(ctx); ok {
		use.kind = kind
		use.name = name
		return use
	}
	if kind, name, ok := orchestrationServiceTypeSpecContext(ident, ctx); ok {
		use.kind = kind
		use.name = name
		return use
	}
	if spec := nearestOrchestrationServiceValueSpec(ctx); spec != nil {
		use.kind = "variable"
		use.name = valueSpecNames(spec)
		return use
	}
	use.kind = orchestrationServiceFallbackTypeUseKind(ctx)
	use.name = ident.Name
	return use
}

func orchestrationServiceTypeSpecContext(ident *ast.Ident, ctx orchestrationServiceIdentContext) (string, string, bool) {
	spec := nearestOrchestrationServiceTypeSpec(ctx)
	if spec == nil {
		return "", "", false
	}
	if spec.Name != ident {
		return "type declaration", spec.Name.Name, true
	}
	if spec.Assign.IsValid() {
		return "type alias", ident.Name, true
	}
	return "type declaration", ident.Name, true
}

func orchestrationServiceFallbackTypeUseKind(ctx orchestrationServiceIdentContext) string {
	switch {
	case hasOrchestrationServiceParent[*ast.TypeAssertExpr](ctx):
		return "type assertion"
	case hasOrchestrationServiceParent[*ast.TypeSwitchStmt](ctx):
		return "type switch case"
	case hasOrchestrationServiceParent[*ast.CompositeLit](ctx):
		return "composite literal"
	case hasOrchestrationServiceParent[*ast.CallExpr](ctx):
		return "type conversion"
	default:
		return "type reference"
	}
}

func orchestrationServiceMethodExpressionContext(
	ident *ast.Ident,
	obj types.Object,
	ctx orchestrationServiceIdentContext,
) (string, string, bool) {
	selector, ok := nearestOrchestrationServiceParent[*ast.SelectorExpr](ctx)
	if !ok || selector.X != ident {
		return "", "", false
	}
	if _, ok := obj.(*types.TypeName); !ok {
		return "", "", false
	}
	if spec := nearestOrchestrationServiceValueSpec(ctx); spec != nil {
		return "method expression", valueSpecNames(spec), true
	}
	return "method expression", selector.Sel.Name, true
}

func orchestrationServiceFieldContext(ctx orchestrationServiceIdentContext) (string, string, bool) {
	field, ok := nearestOrchestrationServiceParent[*ast.Field](ctx)
	if !ok {
		return "", "", false
	}
	fieldList, ok := nearestOrchestrationServiceParent[*ast.FieldList](ctx)
	if !ok {
		return "", "", false
	}
	if spec := nearestOrchestrationServiceTypeSpec(ctx); spec != nil && spec.TypeParams == fieldList {
		return "type parameter", fieldName(field), true
	}
	if fn, ok := nearestOrchestrationServiceParent[*ast.FuncType](ctx); ok {
		switch fieldList {
		case fn.TypeParams:
			return "type parameter", fieldName(field), true
		case fn.Params:
			return "parameter", fieldName(field), true
		case fn.Results:
			return "return value", fieldName(field), true
		}
	}
	if _, ok := nearestOrchestrationServiceParent[*ast.StructType](ctx); ok {
		return "field", fieldName(field), true
	}
	return "", "", false
}

func nearestOrchestrationServiceTypeSpec(ctx orchestrationServiceIdentContext) *ast.TypeSpec {
	spec, _ := nearestOrchestrationServiceParent[*ast.TypeSpec](ctx)
	return spec
}

func nearestOrchestrationServiceValueSpec(ctx orchestrationServiceIdentContext) *ast.ValueSpec {
	spec, _ := nearestOrchestrationServiceParent[*ast.ValueSpec](ctx)
	return spec
}

func nearestOrchestrationServiceParent[T ast.Node](ctx orchestrationServiceIdentContext) (T, bool) {
	var zero T
	for i := len(ctx.parents) - 1; i >= 0; i-- {
		if typed, ok := ctx.parents[i].(T); ok {
			return typed, true
		}
	}
	return zero, false
}

func hasOrchestrationServiceParent[T ast.Node](ctx orchestrationServiceIdentContext) bool {
	_, ok := nearestOrchestrationServiceParent[T](ctx)
	return ok
}

func orchestrationServiceObjectUsesTarget(obj types.Object, target *types.TypeName) bool {
	switch typed := obj.(type) {
	case *types.TypeName:
		return orchestrationServiceTypeUsesTarget(typed.Type(), target)
	case *types.Var:
		return orchestrationServiceTypeUsesTarget(typed.Type(), target)
	default:
		return false
	}
}

func orchestrationServiceTypeUsesTarget(typ types.Type, target *types.TypeName) bool {
	if typ == nil || target == nil {
		return false
	}
	unaliased := types.Unalias(typ)
	switch typed := unaliased.(type) {
	case *types.Named:
		return typed.Obj() == target
	case *types.TypeParam:
		return orchestrationServiceTypeUsesTarget(typed.Constraint(), target)
	case *types.Interface:
		return orchestrationServiceInterfaceUsesTarget(typed, target)
	case *types.Signature:
		return orchestrationServiceTupleUsesTarget(typed.Params(), target) ||
			orchestrationServiceTupleUsesTarget(typed.Results(), target)
	case *types.Tuple:
		return orchestrationServiceTupleUsesTarget(typed, target)
	default:
		return orchestrationServiceContainerTypeUsesTarget(unaliased, target)
	}
}

func orchestrationServiceInterfaceUsesTarget(iface *types.Interface, target *types.TypeName) bool {
	for embedded := range iface.EmbeddedTypes() {
		if orchestrationServiceTypeUsesTarget(embedded, target) {
			return true
		}
	}
	return false
}

func orchestrationServiceContainerTypeUsesTarget(typ types.Type, target *types.TypeName) bool {
	switch typed := typ.(type) {
	case *types.Pointer:
		return orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	case *types.Slice:
		return orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	case *types.Array:
		return orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	case *types.Map:
		return orchestrationServiceTypeUsesTarget(typed.Key(), target) ||
			orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	case *types.Chan:
		return orchestrationServiceTypeUsesTarget(typed.Elem(), target)
	}
	return false
}

func orchestrationServiceTupleUsesTarget(tuple *types.Tuple, target *types.TypeName) bool {
	if tuple == nil {
		return false
	}
	for variable := range tuple.Variables() {
		if orchestrationServiceTypeUsesTarget(variable.Type(), target) {
			return true
		}
	}
	return false
}

func orchestrationServiceTypeRelPath(filename string) string {
	normalized := filepath.ToSlash(filename)
	for _, marker := range []string{"/cmd/", "/internal/"} {
		if index := strings.Index(normalized, marker); index >= 0 {
			return normalized[index+1:]
		}
	}
	return normalized
}

func orchestrationServiceTypeExprString(fset *token.FileSet, ident *ast.Ident, ctx orchestrationServiceIdentContext) string {
	if selector, ok := nearestOrchestrationServiceParent[*ast.SelectorExpr](ctx); ok {
		if selector.X == ident || selector.Sel == ident {
			return orchestrationServiceNodeString(fset, selector)
		}
	}
	return ident.Name
}

func orchestrationServiceNodeString(fset *token.FileSet, node any) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return fmt.Sprint(node)
	}
	return buf.String()
}

func isAllowedOrchestrationServiceTypeUse(_ orchestrationServiceTypeUse) bool {
	return false
}

func orchestrationServiceTypeUseMessages(uses []orchestrationServiceTypeUse) []string {
	messages := make([]string, 0, len(uses))
	for _, use := range uses {
		messages = append(messages, use.violationMessage())
	}
	return messages
}

func (use orchestrationServiceTypeUse) violationMessage() string {
	return fmt.Sprintf("%s:%d %s %s uses full orchestration service via %s; split it behind a narrow contract port", use.relPath, use.line, use.kind, use.name, use.expr)
}
