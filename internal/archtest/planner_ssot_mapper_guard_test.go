package archtest

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

const plannerSSOTCanonicalImport = "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"

type plannerSSOTMapperSpec struct {
	sourceFile       string
	functionName     string
	canonicalCall    string
	expectedArgs     []string
	fixtureSignature string
	directReturn     bool
}

func plannerSSOTMapperSpecs() []plannerSSOTMapperSpec {
	bindings := cicontract.PlannerSSOTConsumerBindings()
	specs := make([]plannerSSOTMapperSpec, 0, len(bindings))
	for _, binding := range bindings {
		independentArgs := plannerSSOTIndependentArguments(binding.SourceFile, binding.FunctionName, binding.CanonicalCall)
		specs = append(specs, plannerSSOTMapperSpec{
			sourceFile: binding.SourceFile, functionName: binding.FunctionName, canonicalCall: binding.CanonicalCall,
			expectedArgs: independentArgs, fixtureSignature: binding.FixtureSignature,
			directReturn: binding.DirectReturn,
		})
	}
	return specs
}

func plannerSSOTRejectConstantDeadBaselineMutations(function *ast.FuncDecl) error {
	var violation error
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if violation != nil {
			return false
		}
		branch, ok := node.(*ast.IfStmt)
		if !ok {
			return true
		}
		value, constant := plannerSSOTConstantBool(branch.Cond)
		if !constant || value {
			return true
		}
		ast.Inspect(branch.Body, func(dead ast.Node) bool {
			if assignment, ok := dead.(*ast.AssignStmt); ok {
				if slices.ContainsFunc(assignment.Lhs, plannerSSOTBaselineStateExpression) {
					violation = fmt.Errorf("%s mutates baseline state in a constant-dead branch", function.Name.Name)
					return false
				}
			}
			return violation == nil
		})
		return violation == nil
	})
	return violation
}

func plannerSSOTConstantBool(expression ast.Expr) (bool, bool) {
	switch value := expression.(type) {
	case *ast.Ident:
		if value.Name == "true" {
			return true, true
		}
		if value.Name == "false" {
			return false, true
		}
	case *ast.UnaryExpr:
		if value.Op == token.NOT {
			result, ok := plannerSSOTConstantBool(value.X)
			return !result, ok
		}
	case *ast.BinaryExpr:
		return plannerSSOTConstantComparison(value)
	}
	return false, false
}

func plannerSSOTConstantComparison(expression *ast.BinaryExpr) (bool, bool) {
	left, leftOK := plannerSSOTConstantLiteral(expression.X)
	right, rightOK := plannerSSOTConstantLiteral(expression.Y)
	if !leftOK || !rightOK || (expression.Op != token.EQL && expression.Op != token.NEQ) {
		return false, false
	}
	result := left == right
	if expression.Op == token.NEQ {
		result = !result
	}
	return result, true
}

func plannerSSOTConstantLiteral(expression ast.Expr) (string, bool) {
	literal, ok := expression.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	return literal.Kind.String() + ":" + literal.Value, true
}

func plannerSSOTBaselineStateExpression(expression ast.Expr) bool {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}
	switch identifier.Name {
	case "baselineIndex", "memoryFloor", "targetCPU", "fastIdentity", "bootstrapIndex", "requiredIndex", "candidateIndex":
		return true
	default:
		return strings.Contains(strings.ToLower(identifier.Name), "baseline")
	}
}

func plannerSSOTIndependentArguments(sourceFile, functionName, canonicalCall string) []string {
	key := plannerSSOTConsumerKey(sourceFile, functionName, canonicalCall)
	arguments := map[string][]string{
		"internal/devtools/gate/workload_duration_index.go:normalResourceForTier->NormalResourceTupleForTier":                    {"tier"},
		"internal/devtools/gate/workload_compile_selector_planning.go:normalCompileResourceClass->NormalResourceClassForTier":    {"tier"},
		"internal/devtools/gate/local_workload_planning_projection.go:localProjectionNormalClass->NormalResourceClassProjection": {"resource.classID", "resource.cpu", "resource.memoryGiB"},
		"internal/devtools/gate/workload_resource_planning.go:plannedWorkloadResourceTier->NormalResourceTierForResources":       {"workload.ResourceCPU", "workload.ResourceMemoryGiB"},
		"internal/devtools/gate/compile_group.go:validateCompileGroupResourceClass->ValidateResourceClassID":                     {"value"},
		"internal/devtools/gate/workload_duration_index.go:normalResourceTierForTuple->NormalResourceTierForResources":           {"cpu", "memoryGiB"},
		"internal/devtools/gate/workload_resource_reserve.go:resourceTierForReserve->NormalResourceTierForIdentity":              {"resources.ClassID", "resources.CPU", "resources.MemoryGiB"},
	}
	return append([]string(nil), arguments[key]...)
}

func TestPlannerSSOTBaselineStateMutationsAreLive(t *testing.T) {
	for _, binding := range cicontract.PlannerSSOTConsumerBindings() {
		if strings.Contains(binding.SourceFile, "shardresource") {
			t.Fatalf("planner SSOT registry retains deleted shardresource consumer: %#v", binding)
		}
	}
}

func TestPlannerSSOTMappersUseCanonicalDirectCalls(t *testing.T) {
	root := findRepoRoot(t)
	for _, spec := range plannerSSOTMapperSpecs() {
		t.Run(spec.functionName, func(t *testing.T) {
			path := filepath.Join(root, spec.sourceFile)
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read mapper source %s: %v", path, err)
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
			if err != nil {
				t.Fatalf("parse mapper source %s: %v", path, err)
			}
			if err := validatePlannerSSOTMapper(file, spec); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPlannerSSOTRegistryHasNoRetiredShardResourceEntries(t *testing.T) {
	for _, binding := range cicontract.PlannerSSOTConsumerBindings() {
		if strings.Contains(binding.SourceFile, "shardresource") {
			t.Fatalf("planner SSOT registry retains deleted shardresource consumer: %#v", binding)
		}
	}
}

func TestPlannerSSOTRegistryMatchesIndependentArgumentSource(t *testing.T) {
	for _, binding := range cicontract.PlannerSSOTConsumerBindings() {
		want := plannerSSOTIndependentArguments(binding.SourceFile, binding.FunctionName, binding.CanonicalCall)
		if !reflect.DeepEqual(binding.ExpectedArguments, want) {
			t.Fatalf("registry arguments for %s/%s differ from independent source: got=%v want=%v", binding.SourceFile, binding.FunctionName, binding.ExpectedArguments, want)
		}
	}
}

func TestPlannerSSOTMapperRegistryCoversProductionConsumers(t *testing.T) {
	root := findRepoRoot(t)
	discovered := discoverPlannerSSOTProductionConsumers(t, root)
	for _, duplicate := range plannerSSOTRegistryDuplicateKeys(plannerSSOTMapperSpecs()) {
		t.Errorf("planner SSOT registry duplicate consumer: %s", duplicate)
	}
	for _, violation := range plannerSSOTCoverageViolations(discovered, plannerSSOTRegisteredConsumers(plannerSSOTMapperSpecs())) {
		t.Error(violation)
	}
}

func TestPlannerSSOTMapperRegistryRejectsDuplicateEntry(t *testing.T) {
	specs := plannerSSOTMapperSpecs()
	specs = append(specs, specs[0])
	if duplicates := plannerSSOTRegistryDuplicateKeys(specs); len(duplicates) != 1 {
		t.Fatalf("planner SSOT registry duplicate count = %d, want 1 (%v)", len(duplicates), duplicates)
	}
}

func TestPlannerSSOTMapperCoverageRejectsDeletedRegistryEntry(t *testing.T) {
	discovered := discoverPlannerSSOTProductionConsumers(t, findRepoRoot(t))
	specs := plannerSSOTMapperSpecs()
	registered := plannerSSOTRegisteredConsumers(specs[:len(specs)-1])
	if len(plannerSSOTCoverageViolations(discovered, registered)) == 0 {
		t.Fatal("planner SSOT coverage accepted a deleted production registry entry")
	}
}

func plannerSSOTRegisteredConsumers(specs []plannerSSOTMapperSpec) map[string]struct{} {
	registered := make(map[string]struct{}, len(specs))
	for _, spec := range specs {
		registered[plannerSSOTConsumerKey(spec.sourceFile, spec.functionName, spec.canonicalCall)] = struct{}{}
	}
	return registered
}

func plannerSSOTRegistryDuplicateKeys(specs []plannerSSOTMapperSpec) []string {
	counts := make(map[string]int, len(specs))
	for _, spec := range specs {
		counts[plannerSSOTConsumerKey(spec.sourceFile, spec.functionName, spec.canonicalCall)]++
	}
	duplicates := make([]string, 0)
	for key, count := range counts {
		if count > 1 {
			duplicates = append(duplicates, key)
		}
	}
	return duplicates
}

func plannerSSOTCoverageViolations(discovered, registered map[string]struct{}) []string {
	violations := make([]string, 0)
	for consumer := range discovered {
		if _, ok := registered[consumer]; !ok {
			violations = append(violations, "planner SSOT production consumer is unregistered: "+consumer)
		}
	}
	for consumer := range registered {
		if _, ok := discovered[consumer]; !ok {
			violations = append(violations, "planner SSOT registry has stale consumer: "+consumer)
		}
	}
	return violations
}

func discoverPlannerSSOTProductionConsumers(t *testing.T, root string) map[string]struct{} {
	t.Helper()
	discovered := make(map[string]struct{})
	scanGoFiles(t, root, func(relPath, absPath string) {
		if strings.HasSuffix(relPath, "_test.go") || !plannerSSOTProductionPath(relPath) {
			return
		}
		file := parsePlannerSSOTProductionFile(t, absPath)
		aliases, err := plannerSSOTCanonicalImportAliases(file)
		if err != nil {
			t.Fatalf("discover canonical import alias in %s: %v", relPath, err)
		}
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			if call := plannerSSOTProductionCanonicalCall(function, aliases); call != "" {
				discovered[plannerSSOTConsumerKey(relPath, function.Name.Name, call)] = struct{}{}
			}
		}
	})
	return discovered
}

func plannerSSOTProductionPath(path string) bool {
	return strings.HasPrefix(path, "internal/devtools/gate/")
}

func parsePlannerSSOTProductionFile(t *testing.T, path string) *ast.File {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read planner SSOT production source %s: %v", path, err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse planner SSOT production source %s: %v", path, err)
	}
	return file
}

func plannerSSOTProductionCanonicalCall(function *ast.FuncDecl, aliases map[string]struct{}) string {
	if len(function.Body.List) == 1 {
		if returned, ok := function.Body.List[0].(*ast.ReturnStmt); ok && len(returned.Results) == 1 {
			if call, ok := returned.Results[0].(*ast.CallExpr); ok {
				return plannerSSOTResourceCallName(call, aliases)
			}
		}
	}
	if function.Name.Name == "validateCanonicalPolicyClass" && len(function.Body.List) > 0 {
		if assignment, ok := function.Body.List[0].(*ast.AssignStmt); ok && len(assignment.Rhs) == 1 {
			if call, ok := assignment.Rhs[0].(*ast.CallExpr); ok {
				return plannerSSOTResourceCallName(call, aliases)
			}
		}
	}
	return ""
}

func plannerSSOTResourceCallName(call *ast.CallExpr, aliases map[string]struct{}) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || !plannerSSOTCanonicalPackageIdent(selector.X, aliases) || !strings.Contains(selector.Sel.Name, "Resource") {
		return ""
	}
	return selector.Sel.Name
}

func plannerSSOTConsumerKey(path, function, call string) string {
	return path + ":" + function + "->" + call
}

func TestPlannerSSOTMapperGuardRejectsNonCanonicalArgumentBindings(t *testing.T) {
	for _, spec := range plannerSSOTMapperSpecs() {
		plannerSSOTRunArgumentBindingCases(t, spec)
		if spec.functionName == "validateCanonicalPolicyClass" {
			plannerSSOTRunArgumentBindingCase(t, spec, 0, "off-by-one tier", "cicontract.WorkloadResourceTier(index + 2)")
		}
	}
	shadowSpec := plannerSSOTMapperSpecs()[0]
	plannerSSOTRunShadowBindingCase(t, shadowSpec)
	plannerSSOTRunWrapperBindingCase(t, shadowSpec)
	plannerSSOTRunImportAliasCase(t, shadowSpec)
}

func plannerSSOTRunArgumentBindingCases(t *testing.T, spec plannerSSOTMapperSpec) {
	t.Helper()
	for index := range spec.expectedArgs {
		for _, replacement := range []struct {
			name string
			path string
		}{
			{name: "constant", path: "2"},
			{name: "unrelated variable", path: "other"},
		} {
			plannerSSOTRunArgumentBindingCase(t, spec, index, replacement.name, replacement.path)
		}
	}
}

func plannerSSOTRunArgumentBindingCase(t *testing.T, spec plannerSSOTMapperSpec, index int, name, replacement string) {
	t.Helper()
	t.Run(fmt.Sprintf("%s/%s/argument-%d", spec.functionName, name, index), func(t *testing.T) {
		source := plannerSSOTMapperFixtureWithArgument(spec, index, replacement)
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
		if err != nil {
			t.Fatalf("parse fixture: %v", err)
		}
		want := fmt.Sprintf("argument %d", index)
		if err := validatePlannerSSOTMapper(file, spec); err == nil || !containsPlannerSSOTError(err, want) {
			t.Fatalf("validatePlannerSSOTMapper() error = %v, want %q", err, want)
		}
	})
}

func plannerSSOTRunShadowBindingCase(t *testing.T, spec plannerSSOTMapperSpec) {
	t.Helper()
	t.Run("shadowed canonical import", func(t *testing.T) {
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", plannerSSOTMapperFixtureWithShadow(), 0)
		if err != nil {
			t.Fatalf("parse fixture: %v", err)
		}
		if err := validatePlannerSSOTMapper(file, spec); err == nil || !containsPlannerSSOTError(err, "shadows canonical package") {
			t.Fatalf("validatePlannerSSOTMapper() error = %v, want shadowed canonical package", err)
		}
	})
}

func plannerSSOTRunWrapperBindingCase(t *testing.T, spec plannerSSOTMapperSpec) {
	t.Helper()
	t.Run("wrapper argument", func(t *testing.T) {
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", plannerSSOTMapperFixture(spec, "identity(tier)"), 0)
		if err != nil {
			t.Fatalf("parse fixture: %v", err)
		}
		if err := validatePlannerSSOTMapper(file, spec); err == nil || !containsPlannerSSOTError(err, "argument 0") {
			t.Fatalf("validatePlannerSSOTMapper() error = %v, want argument 0", err)
		}
	})
}

func plannerSSOTRunImportAliasCase(t *testing.T, spec plannerSSOTMapperSpec) {
	t.Helper()
	t.Run("canonical import alias", func(t *testing.T) {
		source := plannerSSOTMapperFixture(spec, strings.Join(spec.expectedArgs, ", "))
		source = strings.Replace(source, "import cicontract ", "import contract ", 1)
		source = strings.ReplaceAll(source, "cicontract.", "contract.")
		file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", source, 0)
		if err != nil {
			t.Fatalf("parse alias fixture: %v", err)
		}
		if err := validatePlannerSSOTMapper(file, spec); err != nil {
			t.Fatalf("validatePlannerSSOTMapper() alias error = %v", err)
		}
	})
}

func TestRetiredValidateCompileResourceShapeAbsentFromProduction(t *testing.T) {
	root := findRepoRoot(t)
	const retiredName = "validateCompileResourceShape"
	definitions, references := plannerSSOTRetiredProductionCounts(t, root, retiredName)
	if definitions != 0 || references != 0 {
		t.Fatalf("retired %s remains in production AST: definitions=%d references=%d", retiredName, definitions, references)
	}
}

func plannerSSOTRetiredProductionCounts(t *testing.T, root, retiredName string) (int, int) {
	t.Helper()
	definitions := 0
	references := 0
	scanGoFiles(t, root, func(relPath, absPath string) {
		if strings.HasSuffix(relPath, "_test.go") {
			return
		}
		source, err := os.ReadFile(absPath)
		if err != nil {
			t.Fatalf("read production source %s: %v", relPath, err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), absPath, source, 0)
		if err != nil {
			t.Fatalf("parse production source %s: %v", relPath, err)
		}
		fileDefinitions, fileReferences := plannerSSOTRetiredASTCounts(file, retiredName)
		definitions += fileDefinitions
		references += fileReferences
	})
	return definitions, references
}

func plannerSSOTRetiredASTCounts(file *ast.File, retiredName string) (int, int) {
	definitions := 0
	references := 0
	for _, declaration := range file.Decls {
		function, isFunction := declaration.(*ast.FuncDecl)
		if isFunction && function.Name.Name == retiredName {
			definitions++
		}
		ast.Inspect(declaration, func(node ast.Node) bool {
			ident, isIdent := node.(*ast.Ident)
			if !isIdent || ident.Name != retiredName || (isFunction && function.Name == ident) {
				return true
			}
			references++
			return true
		})
	}
	return definitions, references
}

func containsPlannerSSOTError(err error, want string) bool {
	return err != nil && strings.Contains(err.Error(), want)
}

func plannerSSOTMapperFixture(spec plannerSSOTMapperSpec, arguments string) string {
	if !spec.directReturn {
		return "package gate\n\nimport cicontract \"" + plannerSSOTCanonicalImport + "\"\n\n" + spec.fixtureSignature + " {\n\texpected, err := cicontract." + spec.canonicalCall + "(" + arguments + ")\n\t_ = expected\n\treturn err\n}\n"
	}
	return "package gate\n\nimport cicontract \"" + plannerSSOTCanonicalImport + "\"\n\n" + spec.fixtureSignature + " {\n\treturn cicontract." + spec.canonicalCall + "(" + arguments + ")\n}\n"
}

func plannerSSOTMapperFixtureWithArgument(spec plannerSSOTMapperSpec, index int, replacement string) string {
	arguments := append([]string(nil), spec.expectedArgs...)
	arguments[index] = replacement
	return plannerSSOTMapperFixture(spec, strings.Join(arguments, ", "))
}

func plannerSSOTMapperFixtureWithShadow() string {
	return "package gate\n\nimport cicontract \"" + plannerSSOTCanonicalImport + "\"\n\nfunc normalResourceForTier(tier int) (float64, float64, error) {\n\tcicontract := tier\n\treturn cicontract.NormalResourceTupleForTier(tier)\n}\n"
}

func validatePlannerSSOTMapper(file *ast.File, spec plannerSSOTMapperSpec) error {
	aliases, err := plannerSSOTCanonicalImportAliases(file)
	if err != nil {
		return fmt.Errorf("%s canonical cicontract import: %w", spec.functionName, err)
	}
	if len(aliases) != 1 {
		return fmt.Errorf("%s has no canonical cicontract import", spec.functionName)
	}
	function, err := plannerSSOTMapperFunction(file, spec.functionName)
	if err != nil {
		return err
	}
	parameters := plannerSSOTParameterNames(function)
	if plannerSSOTShadowedCanonicalPackage(function, parameters, aliases) {
		return fmt.Errorf("%s shadows canonical package", spec.functionName)
	}
	if !spec.directReturn {
		return plannerSSOTAssignedCanonicalCall(function, spec, aliases)
	}
	return plannerSSOTDirectReturn(function, spec, aliases)
}

func plannerSSOTAssignedCanonicalCall(function *ast.FuncDecl, spec plannerSSOTMapperSpec, aliases map[string]struct{}) error {
	if len(function.Body.List) == 0 {
		return fmt.Errorf("%s must start with its canonical call", spec.functionName)
	}
	assignment, ok := function.Body.List[0].(*ast.AssignStmt)
	if !ok || len(assignment.Rhs) != 1 {
		return fmt.Errorf("%s must assign its canonical call first", spec.functionName)
	}
	call, ok := assignment.Rhs[0].(*ast.CallExpr)
	if !ok || !plannerSSOTCanonicalCall(call, spec.canonicalCall, aliases) {
		return fmt.Errorf("%s must call cicontract.%s first", spec.functionName, spec.canonicalCall)
	}
	if err := plannerSSOTCallArguments(call, spec); err != nil {
		return err
	}
	if spec.functionName == "validateCanonicalPolicyClass" {
		if err := plannerSSOTValidateCanonicalPolicyDataflow(function, call, aliases); err != nil {
			return err
		}
	}
	return nil
}

func plannerSSOTValidateCanonicalPolicyDataflow(function *ast.FuncDecl, canonicalCall *ast.CallExpr, aliases map[string]struct{}) error {
	valueName, errorName, canonicalName, err := plannerSSOTCanonicalPolicyBindings(function, canonicalCall)
	if err != nil {
		return err
	}
	canonicalCalls, valueUses, errorUses := plannerSSOTCanonicalPolicyUsage(function, canonicalName, aliases, valueName, errorName)
	if canonicalCalls != 1 || valueUses == 0 || errorUses == 0 {
		return fmt.Errorf("%s canonical call must have one live value/error dataflow", function.Name.Name)
	}
	return nil
}

func plannerSSOTCanonicalPolicyBindings(function *ast.FuncDecl, canonicalCall *ast.CallExpr) (*ast.Ident, *ast.Ident, string, error) {
	if len(function.Body.List) == 0 {
		return nil, nil, "", fmt.Errorf("%s has no live canonical dataflow", function.Name.Name)
	}
	assignment, ok := function.Body.List[0].(*ast.AssignStmt)
	if !ok || len(assignment.Lhs) != 2 {
		return nil, nil, "", fmt.Errorf("%s canonical call must bind value and error", function.Name.Name)
	}
	valueName, valueOK := assignment.Lhs[0].(*ast.Ident)
	errorName, errorOK := assignment.Lhs[1].(*ast.Ident)
	if !valueOK || !errorOK || valueName.Name == "_" || errorName.Name == "_" {
		return nil, nil, "", fmt.Errorf("%s canonical call bindings must remain live", function.Name.Name)
	}
	canonicalSelector, ok := canonicalCall.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, nil, "", fmt.Errorf("%s canonical call must use a package selector", function.Name.Name)
	}
	return valueName, errorName, canonicalSelector.Sel.Name, nil
}

func plannerSSOTCanonicalPolicyUsage(function *ast.FuncDecl, canonicalName string, aliases map[string]struct{}, valueName, errorName *ast.Ident) (int, int, int) {
	canonicalCalls := 0
	valueUses, errorUses := 0, 0
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && plannerSSOTCanonicalCall(call, canonicalName, aliases) {
			canonicalCalls++
		}
		ident, ok := node.(*ast.Ident)
		if !ok || ident == valueName || ident == errorName {
			return true
		}
		if ident.Name == valueName.Name {
			valueUses++
		}
		if ident.Name == errorName.Name {
			errorUses++
		}
		return true
	})
	return canonicalCalls, valueUses, errorUses
}

func plannerSSOTMapperFunction(file *ast.File, functionName string) (*ast.FuncDecl, error) {
	var function *ast.FuncDecl
	for _, declaration := range file.Decls {
		candidate, ok := declaration.(*ast.FuncDecl)
		if ok && candidate.Name.Name == functionName {
			if function != nil {
				return nil, fmt.Errorf("%s is declared more than once", functionName)
			}
			function = candidate
		}
	}
	if function == nil || function.Body == nil {
		return nil, fmt.Errorf("%s definition is missing", functionName)
	}
	return function, nil
}

func plannerSSOTDirectReturn(function *ast.FuncDecl, spec plannerSSOTMapperSpec, aliases map[string]struct{}) error {
	if len(function.Body.List) != 1 {
		return fmt.Errorf("%s must contain one direct return statement", spec.functionName)
	}
	returnStatement, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returnStatement.Results) != 1 {
		return fmt.Errorf("%s must return one canonical call directly", spec.functionName)
	}
	call, ok := returnStatement.Results[0].(*ast.CallExpr)
	if !ok || !plannerSSOTCanonicalCall(call, spec.canonicalCall, aliases) {
		return fmt.Errorf("%s must directly return cicontract.%s", spec.functionName, spec.canonicalCall)
	}
	return plannerSSOTCallArguments(call, spec)
}

func plannerSSOTCallArguments(call *ast.CallExpr, spec plannerSSOTMapperSpec) error {
	if len(call.Args) != len(spec.expectedArgs) {
		return fmt.Errorf("%s canonical call argument count = %d, want %d", spec.functionName, len(call.Args), len(spec.expectedArgs))
	}
	for index, expected := range spec.expectedArgs {
		actual, err := plannerSSOTExpressionString(call.Args[index])
		if err != nil || actual != expected {
			return fmt.Errorf("%s canonical call argument %d must be direct function input/field %q", spec.functionName, index, expected)
		}
	}
	return nil
}

func plannerSSOTExpressionString(expression ast.Expr) (string, error) {
	var output bytes.Buffer
	if err := printer.Fprint(&output, token.NewFileSet(), expression); err != nil {
		return "", err
	}
	return output.String(), nil
}

func plannerSSOTCanonicalImportAliases(file *ast.File) (map[string]struct{}, error) {
	aliases := make(map[string]struct{})
	for _, importSpec := range file.Imports {
		alias, matched, err := plannerSSOTCanonicalImportAlias(importSpec)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		if _, duplicate := aliases[alias]; duplicate {
			return nil, fmt.Errorf("canonical cicontract import alias %q is duplicated", alias)
		}
		aliases[alias] = struct{}{}
	}
	if len(aliases) == 0 {
		return aliases, nil
	}
	if len(aliases) != 1 {
		return nil, fmt.Errorf("canonical cicontract import count = %d, want 1", len(aliases))
	}
	return aliases, nil
}

func plannerSSOTCanonicalImportAlias(importSpec *ast.ImportSpec) (string, bool, error) {
	if importSpec.Path == nil || importSpec.Path.Value != fmt.Sprintf("%q", plannerSSOTCanonicalImport) {
		return "", false, nil
	}
	alias := "cicontract"
	if importSpec.Name != nil {
		alias = importSpec.Name.Name
	}
	if alias == "_" || alias == "." || alias == "" {
		return "", false, fmt.Errorf("canonical cicontract import alias %q is invalid", alias)
	}
	return alias, true, nil
}

func plannerSSOTCanonicalPackageIdent(expression ast.Expr, aliases map[string]struct{}) bool {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return false
	}
	_, ok = aliases[identifier.Name]
	return ok
}

func plannerSSOTCanonicalCall(call *ast.CallExpr, functionName string, aliases map[string]struct{}) bool {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || selector.Sel.Name != functionName {
		return false
	}
	return plannerSSOTCanonicalPackageIdent(selector.X, aliases)
}

func plannerSSOTParameterNames(function *ast.FuncDecl) map[string]struct{} {
	parameters := make(map[string]struct{})
	if function.Type.Params == nil {
		return parameters
	}
	for _, field := range function.Type.Params.List {
		for _, name := range field.Names {
			parameters[name.Name] = struct{}{}
		}
	}
	return parameters
}

func plannerSSOTShadowedCanonicalPackage(function *ast.FuncDecl, parameters map[string]struct{}, aliases map[string]struct{}) bool {
	for alias := range aliases {
		if _, shadowed := parameters[alias]; shadowed {
			return true
		}
	}
	shadowed := false
	stack := make([]ast.Node, 0, 8)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if node == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		var parent ast.Node
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		if ident, ok := node.(*ast.Ident); ok {
			if _, isCanonicalAlias := aliases[ident.Name]; !isCanonicalAlias {
				stack = append(stack, node)
				return true
			}
			selector, packageIdent := parent.(*ast.SelectorExpr)
			if !packageIdent || selector.X != ident {
				shadowed = true
				stack = append(stack, node)
				return false
			}
		}
		stack = append(stack, node)
		return true
	})
	return shadowed
}
