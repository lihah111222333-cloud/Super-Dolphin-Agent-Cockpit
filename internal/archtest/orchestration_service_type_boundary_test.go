package archtest_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

const superAgentModulePath = "github.com/lihah111222333-cloud/super-dolphin-agent"

type orchestrationServiceTypeUse struct {
	relPath string
	line    int
	kind    string
	name    string
	expr    string
}

type orchestrationServiceTypeGuardFixture struct {
	name         string
	importPath   string
	files        map[string]string
	wantContains []string
}

func runOrchestrationServiceTypeConsumersCheck(t *testing.T, pkgs []*orchestrationServiceCheckedPackage) {
	t.Helper()

	var violations []string
	for _, pkg := range pkgs {
		if !orchestrationServiceCheckedPackageMayCarryWideType(pkg) {
			continue
		}
		for _, use := range collectOrchestrationServiceTypeUses(pkg, nil) {
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

	for _, tt := range orchestrationServiceTypeGuardFixtures() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pkg := typeCheckWideOrchestrationFixturePackage(t, tt.importPath, tt.files)
			got := orchestrationServiceTypeUseMessages(collectOrchestrationServiceTypeUses(pkg, nil))
			if len(tt.wantContains) == 0 && len(got) > 0 {
				t.Fatalf("unexpected violations:\n%s", strings.Join(got, "\n"))
			}
			for _, want := range tt.wantContains {
				if !containsViolation(got, want) {
					t.Fatalf("missing violation containing %q; got:\n%s", want, strings.Join(got, "\n"))
				}
			}
		})
	}
}

func orchestrationServiceTypeGuardFixtures() []orchestrationServiceTypeGuardFixture {
	fixtures := []orchestrationServiceTypeGuardFixture{
		{
			name:       "public contract wide interface",
			importPath: superAgentModulePath + "/internal/contract",
			files: map[string]string{
				"internal/contract/wide.go": `package contract

type PublicWide interface {
	LaunchAgent()
	GetReport()
}
`,
			},
			wantContains: []string{
				"internal/contract/wide.go:3 type declaration PublicWide uses full orchestration service",
			},
		},
		{
			name:       "public contract wide interface through returned narrow ports",
			importPath: superAgentModulePath + "/internal/contract",
			files: map[string]string{
				"internal/contract/wide.go": `package contract

type LifecyclePort interface { LaunchAgent() }
type ReportPort interface { GetReport() }

type PublicPorts interface {
	Lifecycle() LifecyclePort
	Reports() ReportPort
}
`,
			},
			wantContains: []string{
				"internal/contract/wide.go:6 type declaration PublicPorts uses full orchestration service",
			},
		},
	}
	fixtures = append(fixtures, orchestrationServiceTypeGuardPropagationFixtures()...)
	return append(fixtures, orchestrationServiceTypeGuardAllowedFixtures()...)
}

func orchestrationServiceTypeGuardPropagationFixtures() []orchestrationServiceTypeGuardFixture {
	return []orchestrationServiceTypeGuardFixture{
		{
			name:       "anonymous composite parameter",
			importPath: superAgentModulePath + "/internal/module/dashboard",
			files: map[string]string{
				"internal/module/dashboard/consumer.go": `package dashboard

func use(svc interface {
	interface { LaunchAgent() }
	interface { GetReport() }
}) {}
`,
			},
			wantContains: []string{
				"internal/module/dashboard/consumer.go:3 parameter svc uses full orchestration service",
			},
		},
		{
			name:       "parameter propagation",
			importPath: superAgentModulePath + "/internal/module/dashboard",
			files: map[string]string{
				"internal/module/dashboard/consumer.go": `package dashboard

type wide interface {
	LaunchAgent()
	GetReport()
}

func use(svc wide) {}
`,
			},
			wantContains: []string{
				"internal/module/dashboard/consumer.go:3 type declaration wide uses full orchestration service",
				"internal/module/dashboard/consumer.go:8 parameter svc uses full orchestration service",
			},
		},
		{
			name:       "field propagation",
			importPath: superAgentModulePath + "/internal/provider/codexapp",
			files: map[string]string{
				"internal/provider/codexapp/consumer.go": `package codexapp

type wide interface {
	LaunchAgent()
	GetReport()
}

type holder struct { service wide }
`,
			},
			wantContains: []string{
				"internal/provider/codexapp/consumer.go:3 type declaration wide uses full orchestration service",
				"internal/provider/codexapp/consumer.go:8 field service uses full orchestration service",
			},
		},
		{
			name:       "return propagation",
			importPath: superAgentModulePath + "/internal/platform/toolbridge",
			files: map[string]string{
				"internal/platform/toolbridge/consumer.go": `package toolbridge

type wide interface {
	LaunchAgent()
	GetReport()
}

func use() wide { return nil }
`,
			},
			wantContains: []string{
				"internal/platform/toolbridge/consumer.go:3 type declaration wide uses full orchestration service",
				"internal/platform/toolbridge/consumer.go:8 return value (anonymous) uses full orchestration service",
			},
		},
	}
}

func orchestrationServiceTypeGuardAllowedFixtures() []orchestrationServiceTypeGuardFixture {
	return []orchestrationServiceTypeGuardFixture{
		{
			name:       "legal separated ports struct",
			importPath: superAgentModulePath + "/cmd/mcp-orch/tools",
			files: map[string]string{
				"cmd/mcp-orch/tools/ports.go": `package tools

type lifecycle interface { LaunchAgent() }
type reports interface { GetReport() }

type ToolPorts struct {
	Lifecycle lifecycle
	Reports reports
}
`,
			},
		},
		{
			name:       "legal fx in and runtime ports",
			importPath: superAgentModulePath + "/cmd/mcp-orch",
			files: map[string]string{
				"cmd/mcp-orch/runtime.go": `package main

import "go.uber.org/fx"

type lifecycle interface { LaunchAgent() }
type reports interface { GetReport() }
type turns interface { SubmitTurn() }

type agentMessengerPorts struct {
	Lifecycle lifecycle
	Reports reports
	Turns turns
}

type agentListPorts struct {
	Lifecycle lifecycle
	Reports reports
}

type runtimeParams struct {
	fx.In
	Lifecycle lifecycle
	Reports reports
	Turns turns
}
`,
			},
		},
	}
}

func collectOrchestrationServiceTypeUses(pkg *orchestrationServiceCheckedPackage, target *types.TypeName) []orchestrationServiceTypeUse {
	if pkg == nil || pkg.typesInfo == nil {
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
	for _, parent := range slices.Backward(ctx.parents) {
		if typed, ok := parent.(T); ok {
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
	if typ == nil {
		return false
	}
	if target == nil {
		return wideOrchestrationType(typ)
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

func isAllowedOrchestrationServiceTypeUse(use orchestrationServiceTypeUse) bool {
	return isAllowedWideOrchestrationFacadeUse(use.relPath, use.kind, use.name)
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
