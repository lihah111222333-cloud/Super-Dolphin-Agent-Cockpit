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
	expectedFields := []string{
		"logger",
		"eventBus",
		"registry",
		"lifecycle",
		"dags",
		"turns",
		"reports",
		"terminalOutcomes",
		"terminalHeadReader",
		"terminalDAG",
	}
	expected := orchestrationInternalBoundarySet(expectedFields)

	failIfViolations(t, orchestrationInternalBoundaryExactServiceFieldViolations(relPath, actualFields, actual, expectedFields, expected))
}

func TestOrchestrationDAGControllerDoesNotOwnServiceOrRuntimeAgentState(t *testing.T) {
	t.Parallel()

	const relPath = "cmd/mcp-orch/orchestration/dag.go"
	root := repoRoot(t)
	file := parseGoFileForInterfaceGuard(t, root, relPath)
	typeSpec, ok := findTypeSpec(file, "dagController")
	if !ok {
		t.Fatalf("%s: type dagController struct not found", relPath)
	}
	controllerStruct, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		t.Fatalf("%s: type dagController is %T, want *ast.StructType", relPath, typeSpec.Type)
	}

	failIfViolations(t, orchestrationInternalBoundaryDAGControllerStateViolations(relPath, controllerStruct))
}

func TestOrchestrationLifecycleControllerStateOwnershipRatchet(t *testing.T) {
	t.Parallel()

	const relPath = "cmd/mcp-orch/orchestration/service.go"
	root := repoRoot(t)
	file := parseGoFileForInterfaceGuard(t, root, relPath)
	typeSpec, ok := findTypeSpec(file, "lifecycleController")
	if !ok {
		t.Fatalf("%s: type lifecycleController struct not found", relPath)
	}
	lifecycleStruct, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		t.Fatalf("%s: type lifecycleController is %T, want *ast.StructType", relPath, typeSpec.Type)
	}

	actualFields := orchestrationInternalBoundaryStructFieldNames(lifecycleStruct)
	actual := orchestrationInternalBoundarySet(actualFields)
	expectedFields := []string{
		"registry",
		"launcher",
		"sessionCleaner",
		"recoveryStore",
		"recovery",
		"agentThreads",
		"agentBindings",
		"machineCfg",
		"processExitWaitTimeout",
		"exitMonitor",
		"asyncCtx",
		"asyncCancel",
		"asyncWg",
	}
	expected := orchestrationInternalBoundarySet(expectedFields)

	failIfViolations(t, orchestrationInternalBoundaryExactStructFieldViolations(relPath, "lifecycleController", actualFields, actual, expectedFields, expected))
}

func TestOrchestrationRecoveryControllerStateOwnershipRatchet(t *testing.T) {
	t.Parallel()

	const relPath = "cmd/mcp-orch/orchestration/recover.go"
	root := repoRoot(t)
	file := parseGoFileForInterfaceGuard(t, root, relPath)
	typeSpec, ok := findTypeSpec(file, "recoveryController")
	if !ok {
		t.Fatalf("%s: type recoveryController struct not found", relPath)
	}
	recoveryStruct, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		t.Fatalf("%s: type recoveryController is %T, want *ast.StructType", relPath, typeSpec.Type)
	}

	actualFields := orchestrationInternalBoundaryStructFieldNames(recoveryStruct)
	actual := orchestrationInternalBoundarySet(actualFields)
	expectedFields := []string{
		"registry",
		"launcher",
		"store",
		"state",
		"local",
		"launchCommit",
		"reports",
		"eventBus",
		"logger",
	}
	expected := orchestrationInternalBoundarySet(expectedFields)

	failIfViolations(t, orchestrationInternalBoundaryExactStructFieldViolations(relPath, "recoveryController", actualFields, actual, expectedFields, expected))
}

func TestOrchestrationRecoveryHelpersDoNotDependOnFullService(t *testing.T) {
	t.Parallel()

	const relPath = "cmd/mcp-orch/orchestration/recover.go"
	root := repoRoot(t)
	fset, file := orchestrationInternalBoundaryParseFile(t, root, relPath)

	failIfViolations(t, orchestrationInternalBoundaryRecoveryServicePointerViolations(fset, file, relPath))
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

func TestOrchestrationAdaptersDoNotHoldFullService(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := orchestrationInternalBoundaryGoFiles(t, root, "cmd/mcp-orch/orchestration")

	var violations []string
	for _, relPath := range files {
		if strings.HasSuffix(relPath, "_test.go") {
			continue
		}
		fset, file := orchestrationInternalBoundaryParseFile(t, root, relPath)
		violations = append(violations, orchestrationInternalBoundaryFullServiceStructFieldViolations(fset, file, relPath)...)
		violations = append(violations, orchestrationInternalBoundaryFullServiceProviderParamViolations(fset, file, relPath)...)
	}
	failIfViolations(t, violations)
}

func TestOrchestrationProviderParamsRejectNestedFuncFullService(t *testing.T) {
	t.Parallel()

	const relPath = "cmd/mcp-orch/orchestration/provider_fixture.go"
	const source = `package orchestration

type service struct{}

func ProvideFixture(callback func(*service)) any {
	return nil
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, relPath, source, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse provider fixture: %v", err)
	}

	violations := orchestrationInternalBoundaryFullServiceProviderParamViolations(fset, file, relPath)
	if len(violations) != 1 {
		t.Fatalf("provider-like nested func(*service) violations = %v, want exactly one", violations)
	}
	if !strings.Contains(violations[0], "ProvideFixture must not take *service") {
		t.Fatalf("provider-like nested func(*service) violation = %q, want provider parameter message", violations[0])
	}
}

func TestOrchestrationDAGFilesDoNotReadRuntimeAgentMap(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	files := orchestrationInternalBoundaryGoFiles(t, root, "cmd/mcp-orch/orchestration")

	var violations []string
	for _, relPath := range files {
		base := filepath.Base(relPath)
		if strings.HasSuffix(relPath, "_test.go") || !strings.HasPrefix(base, "dag") {
			continue
		}
		fset, file := orchestrationInternalBoundaryParseFile(t, root, relPath)
		violations = append(violations, orchestrationInternalBoundaryDAGRuntimeMapAccessViolations(fset, file, relPath)...)
	}
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

func orchestrationInternalBoundaryDAGControllerStateViolations(relPath string, controllerStruct *ast.StructType) []string {
	var violations []string
	for _, field := range controllerStruct.Fields.List {
		typeName := orchestrationInternalBoundaryRuntimeAgentFieldTypeString(field.Type)
		switch typeName {
		case "*service", "agentRegistry", "*agentRegistry", "contextlock.RWMutex", "map[string]*agentRuntime":
			for _, name := range field.Names {
				violations = append(violations, fmt.Sprintf("%s: dagController field %s must not hold %s", relPath, name.Name, typeName))
			}
			if len(field.Names) == 0 {
				violations = append(violations, fmt.Sprintf("%s: dagController embedded field must not hold %s", relPath, typeName))
			}
		}
	}
	return violations
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

func orchestrationInternalBoundaryRecoveryServicePointerViolations(fset *token.FileSet, file *ast.File, relPath string) []string {
	var violations []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		violations = append(violations, orchestrationInternalBoundaryRecoveryFuncServicePointerViolations(fset, fn, relPath)...)
	}
	return violations
}

func orchestrationInternalBoundaryRecoveryFuncServicePointerViolations(fset *token.FileSet, fn *ast.FuncDecl, relPath string) []string {
	violations := orchestrationInternalBoundaryRecoveryReceiverServicePointerViolations(fset, fn, relPath)
	if fn.Type != nil && fn.Type.Params != nil {
		violations = append(violations, orchestrationInternalBoundaryRecoveryParamServicePointerViolations(fset, fn, relPath)...)
	}
	return violations
}

func orchestrationInternalBoundaryRecoveryReceiverServicePointerViolations(fset *token.FileSet, fn *ast.FuncDecl, relPath string) []string {
	if fn.Recv == nil || orchestrationInternalBoundaryAllowedRecoveryServiceReceiver(fn.Name.Name) {
		return nil
	}
	return orchestrationInternalBoundaryRecoveryFieldServicePointerViolations(
		fset,
		fn.Recv.List,
		relPath,
		fn.Name.Name,
		"receiver; delegate through recoveryController or a narrow port",
	)
}

func orchestrationInternalBoundaryRecoveryParamServicePointerViolations(fset *token.FileSet, fn *ast.FuncDecl, relPath string) []string {
	return orchestrationInternalBoundaryRecoveryFieldServicePointerViolations(
		fset,
		fn.Type.Params.List,
		relPath,
		fn.Name.Name,
		"parameter; use an explicit recovery narrow port",
	)
}

func orchestrationInternalBoundaryRecoveryFieldServicePointerViolations(
	fset *token.FileSet,
	fields []*ast.Field,
	relPath string,
	funcName string,
	reason string,
) []string {
	var violations []string
	for _, field := range fields {
		for _, star := range orchestrationInternalBoundaryServiceStarsInExpr(field.Type) {
			violations = append(violations, fmt.Sprintf(
				"%s:%d recovery helper %s must not have *service %s",
				relPath,
				fset.Position(star.Pos()).Line,
				funcName,
				reason,
			))
		}
	}
	return violations
}

func orchestrationInternalBoundaryAllowedRecoveryServiceReceiver(name string) bool {
	return name == "Recover"
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

func orchestrationInternalBoundaryExactServiceFieldViolations(relPath string, actualFields []string, actual map[string]struct{}, expectedFields []string, expected map[string]struct{}) []string {
	return orchestrationInternalBoundaryExactStructFieldViolations(relPath, "service", actualFields, actual, expectedFields, expected)
}

func orchestrationInternalBoundaryExactStructFieldViolations(relPath, typeName string, actualFields []string, actual map[string]struct{}, expectedFields []string, expected map[string]struct{}) []string {
	var violations []string
	for _, field := range expectedFields {
		if _, ok := actual[field]; !ok {
			violations = append(violations, fmt.Sprintf("%s: %s.%s missing from approved field set", relPath, typeName, field))
		}
	}
	for _, field := range actualFields {
		if _, ok := expected[field]; ok {
			continue
		}
		violations = append(violations, fmt.Sprintf("%s: %s.%s must not be added without an explicit owner-boundary update", relPath, typeName, field))
	}
	return violations
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

func orchestrationInternalBoundaryFullServiceStructFieldViolations(fset *token.FileSet, file *ast.File, relPath string) []string {
	var violations []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			violations = append(violations, orchestrationInternalBoundaryFullServiceTypeFieldViolations(fset, spec, relPath)...)
		}
	}
	return violations
}

func orchestrationInternalBoundaryFullServiceTypeFieldViolations(fset *token.FileSet, spec ast.Spec, relPath string) []string {
	typeSpec, ok := spec.(*ast.TypeSpec)
	if !ok {
		return nil
	}
	st, ok := typeSpec.Type.(*ast.StructType)
	if !ok || !orchestrationInternalBoundaryFullServiceFieldOwner(typeSpec.Name.Name, st) {
		return nil
	}
	var violations []string
	for _, field := range st.Fields.List {
		violations = append(violations, orchestrationInternalBoundaryFullServiceFieldViolationsForField(fset, field, relPath, typeSpec.Name.Name)...)
	}
	return violations
}

func orchestrationInternalBoundaryFullServiceFieldViolationsForField(fset *token.FileSet, field *ast.Field, relPath, typeName string) []string {
	fieldName := "<embedded>"
	if len(field.Names) > 0 {
		fieldName = field.Names[0].Name
	}
	var violations []string
	for _, star := range orchestrationInternalBoundaryServiceStarsInExpr(field.Type) {
		violations = append(violations, fmt.Sprintf("%s:%d %s.%s must not reference *service in its field type; use a narrow port/controller owner", relPath, fset.Position(star.Pos()).Line, typeName, fieldName))
	}
	return violations
}

func orchestrationInternalBoundaryFullServiceProviderParamViolations(fset *token.FileSet, file *ast.File, relPath string) []string {
	var violations []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Type == nil || fn.Type.Params == nil || !orchestrationInternalBoundaryProviderLikeFunction(fn.Name.Name) {
			continue
		}
		for _, field := range fn.Type.Params.List {
			for _, star := range orchestrationInternalBoundaryServiceStarsInExpr(field.Type) {
				violations = append(violations, fmt.Sprintf("%s:%d %s must not take *service; expose a narrow port through fx.As or a typed params struct", relPath, fset.Position(star.Pos()).Line, fn.Name.Name))
			}
		}
	}
	return violations
}

func orchestrationInternalBoundaryProviderLikeFunction(name string) bool {
	return strings.HasPrefix(name, "Provide") ||
		strings.HasPrefix(name, "New") ||
		strings.Contains(name, "HookConsumer")
}

func orchestrationInternalBoundaryFullServiceFieldOwner(typeName string, st *ast.StructType) bool {
	lower := strings.ToLower(typeName)
	if strings.Contains(lower, "controller") ||
		strings.Contains(lower, "adapter") ||
		strings.Contains(lower, "actor") ||
		strings.Contains(lower, "consumer") ||
		strings.Contains(lower, "kind") {
		return true
	}
	return orchestrationInternalBoundaryStructEmbedsFxIn(st)
}

func orchestrationInternalBoundaryStructEmbedsFxIn(st *ast.StructType) bool {
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 && exprTypeString(field.Type) == "fx.In" {
			return true
		}
	}
	return false
}

func orchestrationInternalBoundaryDAGRuntimeMapAccessViolations(fset *token.FileSet, file *ast.File, relPath string) []string {
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.Field:
			typeName := orchestrationInternalBoundaryRuntimeAgentFieldTypeString(n.Type)
			if typeName == "map[string]*agentRuntime" || typeName == "contextlock.RWMutex" || typeName == "agentRegistry" || typeName == "*agentRegistry" {
				violations = append(violations, fmt.Sprintf("%s:%d DAG files must not define runtime agent registry state (%s)", relPath, fset.Position(n.Pos()).Line, typeName))
			}
		case *ast.SelectorExpr:
			if n.Sel.Name == "agents" {
				violations = append(violations, fmt.Sprintf("%s:%d DAG files must not directly access agent runtime map selector %s", relPath, fset.Position(n.Pos()).Line, strings.Join(orchestrationInternalBoundarySelectorParts(n), ".")))
			}
		}
		return true
	})
	return violations
}

func orchestrationInternalBoundaryServicePointerDebt(fset *token.FileSet, file *ast.File, relPath string) []string {
	return orchestrationInternalBoundaryUnexpectedServicePointers(fset, file, relPath, nil)
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

func orchestrationInternalBoundaryServiceStarsInExpr(expr ast.Expr) []*ast.StarExpr {
	var stars []*ast.StarExpr
	ast.Inspect(expr, func(node ast.Node) bool {
		star, ok := orchestrationInternalBoundaryServiceStarNode(node)
		if ok {
			stars = append(stars, star)
		}
		return true
	})
	return stars
}

func orchestrationInternalBoundaryServiceStar(expr ast.Expr) (*ast.StarExpr, bool) {
	star, ok := expr.(*ast.StarExpr)
	if !ok || !orchestrationInternalBoundaryIsIdent(star.X, "service") {
		return nil, false
	}
	return star, true
}

func orchestrationInternalBoundarySvcSelectorDebt(fset *token.FileSet, file *ast.File, relPath string) []string {
	var violations []string
	ast.Inspect(file, func(node ast.Node) bool {
		sel, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		parts := orchestrationInternalBoundarySelectorParts(sel)
		if slices.Contains(parts[1:], "svc") {
			violations = append(violations, fmt.Sprintf("%s:%d unexpected %s selector; node_router.go must not grow serviceAgentLauncher full-service coupling", relPath, fset.Position(sel.Pos()).Line, strings.Join(parts, ".")))
		}
		return true
	})
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
			violations = append(violations, fmt.Sprintf("%s:%d StopSpawnedAgent adapter must remain exactly StopSpawnedAgent(ctx, a.lifecycle.threads, a.lifecycle.stopper, threadID)", relPath, fset.Position(call.Pos()).Line))
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
		orchestrationInternalBoundarySelectorChain(call.Args[1], "a", "lifecycle", "threads"),
		orchestrationInternalBoundarySelectorChain(call.Args[2], "a", "lifecycle", "stopper"),
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
		violations = append(violations, orchestrationInternalBoundaryStopAgentServiceNameViolations(file, relPath)...)
	}
	return foundCanonical, violations
}

func orchestrationInternalBoundaryStopAgentServiceNameViolations(file *ast.File, relPath string) []string {
	var violations []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != "StopAgentService" || orchestrationInternalBoundaryIsCanonicalStopper(relPath, typeSpec.Name.Name) {
				continue
			}
			violations = append(violations, fmt.Sprintf("%s: StopAgentService must only be defined in cmd/mcp-orch/orchestration/stop_helper.go", relPath))
		}
	}
	return violations
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
