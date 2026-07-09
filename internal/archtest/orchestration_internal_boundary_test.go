package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestOrchestrationServiceStateOwnershipRatchet(t *testing.T) {
	t.Parallel()

	const relPath = "cmd/mcp-orch/orchestration/service.go"
	root := repoRoot(t)
	file := parseGoFileForInterfaceGuard(t, root, relPath)
	typeSpec, ok := findTypeSpec(file, "service")
	if !ok {
		t.Fatalf("%s: type service struct not found", relPath)
	}
	serviceStruct, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		t.Fatalf("%s: type service is %T, want *ast.StructType", relPath, typeSpec.Type)
	}

	actualFields := orchestrationInternalBoundaryStructFieldNames(serviceStruct)
	actual := orchestrationInternalBoundarySet(actualFields)
	nonDebt := map[string]struct{}{
		"logger":   {},
		"eventBus": {},
		"registry": {},
	}
	debtFields := []string{
		"launcher",
		"sessionCleaner",
		"turnStarter",
		"dagStore",
		"runStore",
		"scheduledStartStore",
		"dispatchStore",
		"recoveryStore",
		"agentThreads",
		"agentBindings",
		"machineCfg",
		"processExitWaitTimeout",
		"exitMonitor",
		"asyncCtx",
		"asyncCancel",
		"asyncWg",
	}
	debt := orchestrationInternalBoundarySet(debtFields)

	failIfViolations(t, orchestrationInternalBoundaryServiceFieldViolations(relPath, actualFields, actual, nonDebt, debtFields, debt))
}

func TestOrchestrationAgentRegistryOwnsRuntimeAgentMapAndLock(t *testing.T) {
	t.Parallel()

	const allowedRelPath = "cmd/mcp-orch/orchestration/agent_registry.go"
	root := repoRoot(t)
	files := orchestrationInternalBoundaryGoFiles(t, root, "cmd/mcp-orch/orchestration")

	var violations []string
	for _, relPath := range files {
		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}
		fset, file := orchestrationInternalBoundaryParseFile(t, root, relPath)
		violations = append(violations, orchestrationInternalBoundaryRuntimeAgentRegistryFieldViolations(fset, file, relPath, allowedRelPath)...)
	}
	failIfViolations(t, violations)
}

func TestNodeRouterDoesNotGrowServiceAgentLauncherDebt(t *testing.T) {
	t.Parallel()

	const relPath = "cmd/mcp-orch/orchestration/node_router.go"
	root := repoRoot(t)
	fset, file := orchestrationInternalBoundaryParseFile(t, root, relPath)

	var violations []string
	violations = append(violations, orchestrationInternalBoundaryServicePointerDebt(fset, file, relPath)...)
	violations = append(violations, orchestrationInternalBoundarySvcSelectorDebt(fset, file, relPath)...)
	violations = append(violations, orchestrationInternalBoundaryStopSpawnedAgentDebt(fset, file, relPath)...)
	failIfViolations(t, violations)
}

func TestOrchestrationDoesNotGrowDuplicateStopperInterfaces(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := orchestrationInternalBoundaryGoFiles(t, root, "cmd/mcp-orch/orchestration")
	foundCanonical, violations := orchestrationInternalBoundaryStopperInterfaceViolations(t, root, files)
	if !foundCanonical {
		violations = append(violations, "cmd/mcp-orch/orchestration/stop_helper.go:35 StopAgentService not found; duplicate stopper guard has no canonical reuse target")
	}
	failIfViolations(t, violations)
}

func orchestrationInternalBoundaryRuntimeAgentRegistryFieldViolations(fset *token.FileSet, file *ast.File, relPath, allowedRelPath string) []string {
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		field, ok := node.(*ast.Field)
		if !ok {
			return true
		}
		typeName := orchestrationInternalBoundaryRuntimeAgentFieldTypeString(field.Type)
		if typeName != "map[string]*agentRuntime" && typeName != "contextlock.RWMutex" {
			return true
		}
		if relPath == allowedRelPath {
			return true
		}
		violations = append(violations, fmt.Sprintf("%s:%d runtime agent registry field %s must live in %s", relPath, fset.Position(field.Pos()).Line, typeName, allowedRelPath))
		return true
	})
	return violations
}

func orchestrationInternalBoundaryRuntimeAgentFieldTypeString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.MapType:
		return "map[" + exprTypeString(e.Key) + "]" + exprTypeString(e.Value)
	default:
		return exprTypeString(expr)
	}
}

func orchestrationInternalBoundaryStructFieldNames(st *ast.StructType) []string {
	var fields []string
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			fields = append(fields, exprTypeString(field.Type))
			continue
		}
		for _, name := range field.Names {
			fields = append(fields, name.Name)
		}
	}
	slices.Sort(fields)
	return fields
}

func orchestrationInternalBoundarySet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func orchestrationInternalBoundaryServiceFieldViolations(relPath string, actualFields []string, actual, nonDebt map[string]struct{}, debtFields []string, debt map[string]struct{}) []string {
	var violations []string
	violations = append(violations, orchestrationInternalBoundaryMissingDebtFieldViolations(relPath, actual, debtFields)...)
	violations = append(violations, orchestrationInternalBoundaryMissingNonDebtFieldViolations(relPath, actual, nonDebt)...)
	violations = append(violations, orchestrationInternalBoundaryUnregisteredServiceFieldViolations(relPath, actualFields, nonDebt, debt)...)
	return violations
}

func orchestrationInternalBoundaryMissingDebtFieldViolations(relPath string, actual map[string]struct{}, debtFields []string) []string {
	var violations []string
	for _, field := range debtFields {
		if _, ok := actual[field]; !ok {
			violations = append(violations, fmt.Sprintf("%s: service.%s is registered as current state/container debt but no longer exists; remove the allowance when ownership moves out", relPath, field))
		}
	}
	return violations
}

func orchestrationInternalBoundaryMissingNonDebtFieldViolations(relPath string, actual, nonDebt map[string]struct{}) []string {
	var violations []string
	for field := range nonDebt {
		if _, ok := actual[field]; !ok {
			violations = append(violations, fmt.Sprintf("%s: service.%s is registered as non-debt constructor dependency but no longer exists; update TestOrchestrationServiceStateOwnershipRatchet", relPath, field))
		}
	}
	return violations
}

func orchestrationInternalBoundaryUnregisteredServiceFieldViolations(relPath string, actualFields []string, nonDebt, debt map[string]struct{}) []string {
	var violations []string
	for _, field := range actualFields {
		if orchestrationInternalBoundaryFieldRegistered(field, nonDebt, debt) {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s: service.%s is a new unregistered service container field; move ownership to a narrower owner or explicitly classify the debt in TestOrchestrationServiceStateOwnershipRatchet", relPath, field))
	}
	return violations
}

func orchestrationInternalBoundaryFieldRegistered(field string, nonDebt, debt map[string]struct{}) bool {
	if _, ok := nonDebt[field]; ok {
		return true
	}
	_, ok := debt[field]
	return ok
}

func orchestrationInternalBoundaryParseFile(t *testing.T, root, relPath string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	path := filepath.Join(root, filepath.FromSlash(relPath))
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %s: %v", relPath, err)
	}
	return fset, file
}

func orchestrationInternalBoundaryServicePointerDebt(fset *token.FileSet, file *ast.File, relPath string) []string {
	allowed, fieldAllowed, constructorParamAllowed, violations := orchestrationInternalBoundaryAllowedServicePointers(fset, file, relPath)
	if !fieldAllowed {
		violations = append(violations, fmt.Sprintf("%s: serviceAgentLauncher.svc *service allowance is stale; update TestNodeRouterDoesNotGrowServiceAgentLauncherDebt", relPath))
	}
	if !constructorParamAllowed {
		violations = append(violations, fmt.Sprintf("%s: NewServiceAgentLauncher(svc *service) allowance is stale; update TestNodeRouterDoesNotGrowServiceAgentLauncherDebt", relPath))
	}

	return append(violations, orchestrationInternalBoundaryUnexpectedServicePointers(fset, file, relPath, allowed)...)
}

func orchestrationInternalBoundaryAllowedServicePointers(fset *token.FileSet, file *ast.File, relPath string) (map[*ast.StarExpr]struct{}, bool, bool, []string) {
	allowed := map[*ast.StarExpr]struct{}{}
	var violations []string
	fieldAllowed := orchestrationInternalBoundaryAllowLauncherField(fset, file, relPath, allowed, &violations)
	constructorParamAllowed := orchestrationInternalBoundaryAllowLauncherConstructorParam(fset, file, relPath, allowed, &violations)
	return allowed, fieldAllowed, constructorParamAllowed, violations
}

func orchestrationInternalBoundaryAllowLauncherField(fset *token.FileSet, file *ast.File, relPath string, allowed map[*ast.StarExpr]struct{}, violations *[]string) bool {
	typeSpec, ok := findTypeSpec(file, "serviceAgentLauncher")
	if !ok {
		return false
	}
	st, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		return false
	}
	fieldAllowed := false
	for _, field := range st.Fields.List {
		star, ok := orchestrationInternalBoundaryServiceStar(field.Type)
		if !ok {
			continue
		}
		if len(field.Names) == 1 && field.Names[0].Name == "svc" {
			allowed[star] = struct{}{}
			fieldAllowed = true
			continue
		}
		*violations = append(*violations, fmt.Sprintf("%s:%d serviceAgentLauncher has extra *service field debt; only svc *service is allowed", relPath, fset.Position(field.Pos()).Line))
	}
	return fieldAllowed
}

func orchestrationInternalBoundaryAllowLauncherConstructorParam(fset *token.FileSet, file *ast.File, relPath string, allowed map[*ast.StarExpr]struct{}, violations *[]string) bool {
	fn := orchestrationInternalBoundaryFindFunc(file, "NewServiceAgentLauncher")
	if fn == nil || fn.Type.Params == nil {
		return false
	}
	paramAllowed := false
	for _, field := range fn.Type.Params.List {
		star, ok := orchestrationInternalBoundaryServiceStar(field.Type)
		if !ok {
			continue
		}
		if len(field.Names) == 1 && field.Names[0].Name == "svc" {
			allowed[star] = struct{}{}
			paramAllowed = true
			continue
		}
		*violations = append(*violations, fmt.Sprintf("%s:%d NewServiceAgentLauncher has extra *service parameter debt; only svc *service is allowed", relPath, fset.Position(field.Pos()).Line))
	}
	return paramAllowed
}

func orchestrationInternalBoundaryUnexpectedServicePointers(fset *token.FileSet, file *ast.File, relPath string, allowed map[*ast.StarExpr]struct{}) []string {
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		star, ok := orchestrationInternalBoundaryServiceStarNode(node)
		if !ok {
			return true
		}
		if _, ok := allowed[star]; !ok {
			violations = append(violations, fmt.Sprintf("%s:%d unexpected *service adapter debt; introduce a narrow port instead of growing serviceAgentLauncher full-service coupling", relPath, fset.Position(star.Pos()).Line))
		}
		return true
	})
	return violations
}

func orchestrationInternalBoundaryServiceStarNode(node ast.Node) (*ast.StarExpr, bool) {
	star, ok := node.(*ast.StarExpr)
	if !ok {
		return nil, false
	}
	return orchestrationInternalBoundaryServiceStar(star)
}

func orchestrationInternalBoundaryServiceStar(expr ast.Expr) (*ast.StarExpr, bool) {
	star, ok := expr.(*ast.StarExpr)
	if !ok || !orchestrationInternalBoundaryIsIdent(star.X, "service") {
		return nil, false
	}
	return star, true
}

func orchestrationInternalBoundarySvcSelectorDebt(fset *token.FileSet, file *ast.File, relPath string) []string {
	expected := map[string]int{
		"agentThreads":        1,
		"launchAgentSnapshot": 1,
		"launcher":            2,
	}
	actual := make(map[string]int, len(expected))
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		inner, ok := sel.X.(*ast.SelectorExpr)
		if !ok || inner.Sel.Name != "svc" {
			return true
		}
		actual[sel.Sel.Name]++
		if _, ok := expected[sel.Sel.Name]; !ok {
			violations = append(violations, fmt.Sprintf("%s:%d unexpected .svc.%s selector; serviceAgentLauncher may only touch launchAgentSnapshot, launcher, and agentThreads until the full-service debt is removed", relPath, fset.Position(sel.Pos()).Line, sel.Sel.Name))
		}
		return true
	})
	for name, want := range expected {
		if got := actual[name]; got != want {
			violations = append(violations, fmt.Sprintf("%s: a.svc.%s selector allowance got %d occurrence(s), want %d; update the ratchet when debt shrinks, and do not grow it", relPath, name, got, want))
		}
	}
	return violations
}

func orchestrationInternalBoundaryStopSpawnedAgentDebt(fset *token.FileSet, file *ast.File, relPath string) []string {
	var violations []string
	matches := 0
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || !orchestrationInternalBoundaryIsIdent(call.Fun, "StopSpawnedAgent") {
			return true
		}
		matches++
		if !orchestrationInternalBoundaryIsAllowedStopSpawnedAgentCall(call) {
			violations = append(violations, fmt.Sprintf("%s:%d StopSpawnedAgent adapter debt must remain exactly StopSpawnedAgent(ctx, a.svc.agentThreads, a.svc, threadID)", relPath, fset.Position(call.Pos()).Line))
		}
		return true
	})
	if matches != 1 {
		violations = append(violations, fmt.Sprintf("%s: StopSpawnedAgent adapter allowance got %d call(s), want 1; update the ratchet when debt shrinks, and do not grow it", relPath, matches))
	}
	return violations
}

func orchestrationInternalBoundaryIsAllowedStopSpawnedAgentCall(call *ast.CallExpr) bool {
	if len(call.Args) != 4 {
		return false
	}
	checks := []bool{
		orchestrationInternalBoundaryIsIdent(call.Args[0], "ctx"),
		orchestrationInternalBoundarySelectorChain(call.Args[1], "a", "svc", "agentThreads"),
		orchestrationInternalBoundarySelectorChain(call.Args[2], "a", "svc"),
		orchestrationInternalBoundaryIsIdent(call.Args[3], "threadID"),
	}
	return !slices.Contains(checks, false)
}

func orchestrationInternalBoundaryGoFiles(t *testing.T, root, relRoot string) []string {
	t.Helper()
	absRoot := filepath.Join(root, filepath.FromSlash(relRoot))
	var files []string
	err := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(relPath))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", relRoot, err)
	}
	slices.Sort(files)
	return files
}

func orchestrationInternalBoundaryStopperInterfaceViolations(t *testing.T, root string, files []string) (bool, []string) {
	t.Helper()
	var violations []string
	foundCanonical := false
	for _, relPath := range files {
		_, file := orchestrationInternalBoundaryParseFile(t, root, relPath)
		for _, typeSpec := range orchestrationInternalBoundaryStopAgentOnlyInterfaces(file) {
			if orchestrationInternalBoundaryIsCanonicalStopper(relPath, typeSpec.Name.Name) {
				foundCanonical = true
				continue
			}
			violations = append(violations, fmt.Sprintf("%s: interface %s duplicates StopAgent(ctx context.Context, agentID string) error; reuse cmd/mcp-orch/orchestration/stop_helper.go:35 StopAgentService", relPath, typeSpec.Name.Name))
		}
	}
	return foundCanonical, violations
}

func orchestrationInternalBoundaryStopAgentOnlyInterfaces(file *ast.File) []*ast.TypeSpec {
	var interfaces []*ast.TypeSpec
	for _, decl := range file.Decls {
		interfaces = append(interfaces, orchestrationInternalBoundaryStopAgentOnlyInterfacesInDecl(decl)...)
	}
	return interfaces
}

func orchestrationInternalBoundaryStopAgentOnlyInterfacesInDecl(decl ast.Decl) []*ast.TypeSpec {
	gen, ok := decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.TYPE {
		return nil
	}
	var interfaces []*ast.TypeSpec
	for _, spec := range gen.Specs {
		typeSpec, ok := spec.(*ast.TypeSpec)
		if ok && orchestrationInternalBoundaryIsStopAgentOnlyInterface(typeSpec) {
			interfaces = append(interfaces, typeSpec)
		}
	}
	return interfaces
}

func orchestrationInternalBoundaryIsCanonicalStopper(relPath, name string) bool {
	return relPath == "cmd/mcp-orch/orchestration/stop_helper.go" && name == "StopAgentService"
}

func orchestrationInternalBoundaryIsStopAgentOnlyInterface(typeSpec *ast.TypeSpec) bool {
	iface, ok := typeSpec.Type.(*ast.InterfaceType)
	if !ok || iface.Methods == nil || len(iface.Methods.List) != 1 {
		return false
	}
	method := iface.Methods.List[0]
	if len(method.Names) != 1 || method.Names[0].Name != "StopAgent" {
		return false
	}
	fn, ok := method.Type.(*ast.FuncType)
	if !ok {
		return false
	}
	return orchestrationInternalBoundaryStopAgentParams(fn.Params) && orchestrationInternalBoundaryStopAgentResults(fn.Results)
}

func orchestrationInternalBoundaryStopAgentParams(params *ast.FieldList) bool {
	if params == nil || len(params.List) != 2 {
		return false
	}
	return len(params.List[0].Names) == 1 &&
		params.List[0].Names[0].Name == "ctx" &&
		exprTypeString(params.List[0].Type) == "context.Context" &&
		len(params.List[1].Names) == 1 &&
		params.List[1].Names[0].Name == "agentID" &&
		exprTypeString(params.List[1].Type) == "string"
}

func orchestrationInternalBoundaryStopAgentResults(results *ast.FieldList) bool {
	if results == nil || len(results.List) != 1 {
		return false
	}
	return len(results.List[0].Names) == 0 && exprTypeString(results.List[0].Type) == "error"
}

func orchestrationInternalBoundaryIsIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func orchestrationInternalBoundaryFindFunc(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func orchestrationInternalBoundarySelectorChain(expr ast.Expr, want ...string) bool {
	parts := orchestrationInternalBoundarySelectorParts(expr)
	return slices.Equal(parts, want)
}

func orchestrationInternalBoundarySelectorParts(expr ast.Expr) []string {
	switch e := expr.(type) {
	case *ast.Ident:
		return []string{e.Name}
	case *ast.SelectorExpr:
		return append(orchestrationInternalBoundarySelectorParts(e.X), e.Sel.Name)
	default:
		return []string{strings.TrimPrefix(fmt.Sprintf("%T", expr), "*ast.")}
	}
}
