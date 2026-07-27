package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBackendBoundaryRuleFactsAutoDiscoverRenamedConsumer(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "archtest", "nested")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const source = `package nested

type localPolicyRecord struct {
	ID           string
	Owner        string
	Reason       string
	FilePatterns []string
	Deny         []string
}

func assemblePolicySnapshot() localPolicyRecord {
	return localPolicyRecord{ID: "synthetic_rule", Owner: "synthetic_owner", Reason: "duplicate", FilePatterns: []string{"internal/**/*.go"}, Deny: []string{"internal/store"}}
}
`
	rel := "internal/archtest/nested/new_consumer.go"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err := discoverBackendBoundaryRuleConsumerFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != rel {
		t.Fatalf("automatic consumer discovery got %v, want [%s]", files, rel)
	}
	node, err := parseBackendBoundaryRuleConsumer(root, rel)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations(rel, node), "\n")
	if !strings.Contains(violations, "local backend boundary struct localPolicyRecord") {
		t.Fatalf("renamed local registry facts must be rejected, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsLegacySQLCEvaluator(t *testing.T) {
	const source = `package archtest_test
func TestSQLCBoundaryLegacy(t *testing.T) {
	_ = internalPrefix("internal/store/sqlc")
}
`
	rel := "internal/archtest/sqlc_legacy_test.go"
	node, err := parser.ParseFile(token.NewFileSet(), rel, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations(rel, node), "\n")
	if !strings.Contains(violations, "local SQLC evaluator duplicates the canonical registry") {
		t.Fatalf("legacy SQLC evaluator must be rejected, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsMixedSQLCEvaluator(t *testing.T) {
	const source = `package archtest_test
func TestSQLCBoundaryMixed(t *testing.T) {
	archtest.EvaluateBackendBoundary(root, registry, "store_sqlc_store_platform_only")
	_ = internalPrefix("internal/store/sqlc")
}
`
	rel := "internal/archtest/sqlc_boundary_test.go"
	node, err := parser.ParseFile(token.NewFileSet(), rel, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations(rel, node), "\n")
	if !strings.Contains(violations, "local SQLC evaluator duplicates the canonical registry") {
		t.Fatalf("mixed canonical and legacy SQLC evaluator must be rejected, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsProceduralSidecarEvaluator(t *testing.T) {
	const source = `package archtest_test
func legacySidecarRule(t *testing.T, root string) {
	assertNoImportPrefixes(t, parseImportFiles(t, root, "cmd/mcp-lsp"), []string{internalPrefix("internal/module")})
}
`
	node, err := parser.ParseFile(token.NewFileSet(), "dependency_direction_test.go", source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations("dependency_direction_test.go", node), "\n")
	if !strings.Contains(violations, "procedural MCP sidecar evaluator duplicates the canonical registry") {
		t.Fatalf("procedural MCP sidecar evaluator must be rejected, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsIndirectSidecarEvaluator(t *testing.T) {
	cases := map[string]string{
		"package const": `package archtest_test
const targetSidecar = "cmd/" + "mcp-lsp"
func legacySidecarRule(t *testing.T, root string) {
	assertNoImportPrefixes(t, parseImportFiles(t, root, targetSidecar), []string{internalPrefix("internal/module")})
}`,
		"local variable": `package archtest_test
func legacySidecarRule(t *testing.T, root string) {
	target := "cmd/" + "mcp-lsp"
	assertNoImportPrefixes(t, parseImportFiles(t, root, target), []string{internalPrefix("internal/module")})
}`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			node, err := parser.ParseFile(token.NewFileSet(), "dependency_direction_test.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			violations := strings.Join(backendBoundaryConsumerFactViolations("dependency_direction_test.go", node), "\n")
			if !strings.Contains(violations, "procedural MCP sidecar evaluator duplicates the canonical registry") {
				t.Fatalf("indirect procedural MCP sidecar evaluator must be rejected, got:\n%s", violations)
			}
		})
	}
}

func TestBackendBoundaryRuleFactsRejectsRenamedProceduralDependencyEvaluators(t *testing.T) {
	cases := map[string]string{
		"store allowlist": `package nested
const storeRoot = "internal/" + "store"
func inspectPersistenceImports(t *testing.T, root string) {
	files := parseImportFiles(t, root, storeRoot)
	allowed := []string{"internal/platform/" + "config", "internal/store/" + "sqlc"}
	_ = files
	_ = allowed
}`,
		"fx scope": `package nested
const framework = "go.uber.org/" + "fx"
func inspectAssemblyImports(t *testing.T, root string) {
	files := parseImportFiles(t, root, "internal", "cmd")
	_ = files
	_ = framework
}`,
		"mcp family": `package nested
func inspectProtocolFamily(t *testing.T, root string) {
	target := "cmd/" + "mcp-orch"
	forbidden := "internal/tool/" + "lsp"
	_ = parseImportFiles(t, root, target)
	_ = forbidden
}`,
		"platform isolation": `package nested
func inspectPlatformImports(t *testing.T, root string) {
	target := "internal/platform/" + "hooks"
	forbidden := "internal/platform/" + "db"
	_ = parseImportFiles(t, root, target)
	_ = forbidden
}`,
	}
	for name, source := range cases {
		t.Run(name, func(t *testing.T) {
			rel := "internal/archtest/nested/renamed_policy_test.go"
			node, err := parser.ParseFile(token.NewFileSet(), rel, source, 0)
			if err != nil {
				t.Fatal(err)
			}
			violations := strings.Join(backendBoundaryConsumerFactViolations(rel, node), "\n")
			if !strings.Contains(violations, "procedural backend dependency evaluator duplicates the canonical registry") {
				t.Fatalf("renamed procedural evaluator must be rejected, got:\n%s", violations)
			}
		})
	}
}

func TestBackendBoundaryRuleFactsAllowsOrdinaryImportFixture(t *testing.T) {
	const source = `package nested
func fixture(t *testing.T, root string) {
	_ = parseImportFiles(t, root, "internal/store")
	_ = "internal/platform/config"
}`
	rel := "internal/archtest/nested/import_fixture_test.go"
	node, err := parser.ParseFile(token.NewFileSet(), rel, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	if violations := backendBoundaryConsumerFactViolations(rel, node); len(violations) != 0 {
		t.Fatalf("ordinary import fixture must not be classified as a procedural evaluator: %v", violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsPolicyTableHiddenInTestFunction(t *testing.T) {
	const source = `package nested
func TestHiddenPolicy(t *testing.T) {
	_ = localPolicyRecord{
		ID: "hidden_rule", Owner: "hidden_owner", Reason: "duplicate",
		FilePatterns: []string{"internal/**/*.go"}, Deny: []string{"internal/store"},
	}
}`
	rel := "internal/archtest/nested/hidden_policy_test.go"
	node, err := parser.ParseFile(token.NewFileSet(), rel, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	violations := strings.Join(backendBoundaryConsumerFactViolations(rel, node), "\n")
	if !strings.Contains(violations, "declares local backend boundary policy facts") {
		t.Fatalf("Test function policy table must be rejected, got:\n%s", violations)
	}
}

func TestBackendBoundaryRuleFactsAllowsExplicitRegistryValidationFixture(t *testing.T) {
	const source = `package archtest_test
func TestRegistryRejectsBrokenRule(t *testing.T) {
	_ = archtest.BackendBoundaryRule{
		ID: "broken_rule", Owner: "fixture_owner", Reason: "fixture",
		FilePatterns: []string{"internal/**/*.go"}, Deny: []archtest.BoundaryImportPolicy{},
	}
}`
	rel := "internal/archtest/boundary_registry_test.go"
	node, err := parser.ParseFile(token.NewFileSet(), rel, source, 0)
	if err != nil {
		t.Fatal(err)
	}
	if violations := backendBoundaryConsumerFactViolations(rel, node); len(violations) != 0 {
		t.Fatalf("explicit registry validation fixture must remain allowed: %v", violations)
	}
}

func TestBackendBoundaryRuleFactsDoNotJoinUnrelatedFunctions(t *testing.T) {
	result := loadBackendBoundarySSAFixture(t, "unrelated_helpers")
	if len(result.violations) != 0 {
		t.Fatalf("unrelated functions must not be joined into one evaluator: %v", result.violations)
	}
}

func TestBackendBoundaryRuleFactsRejectsConnectedHelperSplit(t *testing.T) {
	result := loadBackendBoundarySSAFixture(t, "connected_helper_split")
	violations := strings.Join(result.violations, "\n")
	if !strings.Contains(violations, "procedural backend dependency evaluator duplicates the canonical registry") {
		t.Fatalf("connected helper split must be rejected, got:\n%s", violations)
	}
}

// TestBackendBoundaryRuleFactsHaveOneSource prevents consumers from reintroducing registry policy facts.
func TestBackendBoundaryRuleFactsHaveOneSource(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	consumerFiles, err := discoverBackendBoundaryRuleConsumerFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	var violations []string
	ruleIDSources := map[string][]string{}
	for _, rel := range consumerFiles {
		node, err := parseBackendBoundaryRuleConsumer(root, rel)
		if err != nil {
			t.Fatal(err)
		}
		violations = append(violations, backendBoundaryConsumerFactViolations(rel, node)...)
		for id, sources := range backendBoundaryConsumerRuleIDSources(rel, node) {
			ruleIDSources[id] = append(ruleIDSources[id], sources...)
		}
	}
	violations = append(violations, backendBoundaryProductionSSAConnectivityViolations(t, root, consumerFiles)...)
	for id, sources := range ruleIDSources {
		if len(sources) > 1 {
			violations = append(violations, fmt.Sprintf("rule %q appears in multiple local default collections: %s", id, strings.Join(sources, ", ")))
		}
	}
	if len(violations) > 0 {
		t.Fatalf("backend boundary policy facts must come only from DefaultBackendBoundaryRegistry:\n%s", strings.Join(violations, "\n"))
	}
}

func discoverBackendBoundaryRuleConsumerFiles(root string) ([]string, error) {
	const archtestDir = "internal/archtest"
	excluded := map[string]bool{
		archtestDir + "/backend_boundary_evaluator.go":            true,
		archtestDir + "/backend_boundary_registry.go":             true,
		archtestDir + "/backend_boundary_registry_app_adapter.go": true,
		archtestDir + "/backend_boundary_single_source_test.go":   true,
	}
	var files []string
	err := filepath.WalkDir(filepath.Join(root, archtestDir), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !excluded[rel] {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover backend boundary consumers under %s: %w", archtestDir, err)
	}
	return files, nil
}

func parseBackendBoundaryRuleConsumer(root, rel string) (*ast.File, error) {
	path := filepath.Join(root, rel)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	node, err := parser.ParseFile(token.NewFileSet(), path, data, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	return node, nil
}

func backendBoundaryConsumerFactViolations(rel string, node *ast.File) []string {
	violations := backendBoundaryFileSemanticViolations(rel, node)
	for _, declaration := range node.Decls {
		violations = append(violations, backendBoundaryConsumerDeclarationViolations(rel, declaration)...)
	}
	return violations
}

func backendBoundaryFileSemanticViolations(rel string, node *ast.File) []string {
	packageStrings := backendBoundaryPackageStringConstants(node)
	var violations []string
	if declaresProceduralBackendDependencyBoundary(node, packageStrings) {
		violations = append(violations, rel+": procedural backend dependency evaluator duplicates the canonical registry")
	}
	for _, declaration := range node.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if rel != "internal/archtest/sqlc_boundary_test.go" && strings.HasPrefix(fn.Name.Name, "Test") && containsLegacySQLCBoundaryFacts(fn) {
			violations = append(violations, rel+": local SQLC evaluator duplicates the canonical registry")
		}
		if declaresProceduralMCPSidecarBoundary(fn, packageStrings) {
			violations = append(violations, rel+": procedural MCP sidecar evaluator duplicates the canonical registry")
		}
	}
	return violations
}

func declaresProceduralBackendDependencyBoundary(node *ast.File, packageStrings map[string]string) bool {
	for _, facts := range collectProceduralBackendDependencyFacts(node, packageStrings) {
		if declaresProceduralStoreBoundary(facts.parseTargets, facts.facts) ||
			declaresProceduralFXBoundary(facts.parseTargets, facts.facts) ||
			declaresProceduralMCPServerFamilyBoundary(facts.parseTargets, facts.facts) ||
			declaresProceduralPlatformControlBoundary(facts.parseTargets, facts.facts) ||
			declaresProceduralModuleOuterBoundary(facts.parseTargets, facts.facts) {
			return true
		}
	}
	return false
}

type backendBoundaryDependencyFacts struct {
	parseTargets map[string]bool
	facts        map[string]bool
}

func newBackendBoundaryDependencyFacts() backendBoundaryDependencyFacts {
	return backendBoundaryDependencyFacts{
		parseTargets: make(map[string]bool),
		facts:        make(map[string]bool),
	}
}

func collectProceduralBackendDependencyFacts(node *ast.File, packageStrings map[string]string) []backendBoundaryDependencyFacts {
	var functions []backendBoundaryDependencyFacts
	for _, declaration := range node.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		collected := newBackendBoundaryDependencyFacts()
		collectProceduralBackendFunctionFacts(fn, packageStrings, &collected)
		functions = append(functions, collected)
	}
	return functions
}

func collectProceduralBackendFunctionFacts(fn *ast.FuncDecl, packageStrings map[string]string, collected *backendBoundaryDependencyFacts) {
	values := backendBoundaryFunctionStringConstants(fn, packageStrings)
	ast.Inspect(fn.Body, func(item ast.Node) bool {
		if expr, ok := item.(ast.Expr); ok {
			if value, ok := backendBoundaryStringValue(expr, values); ok {
				collected.facts[value] = true
			}
		}
		call, ok := item.(*ast.CallExpr)
		if !ok {
			return true
		}
		if backendBoundaryCallName(call.Fun) != "parseImportFiles" {
			return true
		}
		for _, arg := range call.Args {
			if value, ok := backendBoundaryStringValue(arg, values); ok {
				collected.parseTargets[value] = true
			}
		}
		return true
	})
}

func mergeBackendBoundaryFactSet(target, source map[string]bool) {
	for fact := range source {
		target[fact] = true
	}
}

func declaresProceduralStoreBoundary(parseTargets, facts map[string]bool) bool {
	return parseTargets["internal/store"] && facts["internal/platform/config"] && facts["internal/store/sqlc"]
}

func declaresProceduralFXBoundary(parseTargets, facts map[string]bool) bool {
	return parseTargets["internal"] && parseTargets["cmd"] && facts["go.uber.org/fx"]
}

func declaresProceduralMCPServerFamilyBoundary(parseTargets, facts map[string]bool) bool {
	orchFacts := facts["internal/tool/lsp"] || facts["internal/tool/ida"]
	idaFacts := facts["internal/tool/lsp"] || facts["internal/tool/orchestration"]
	orchTarget := parseTargets["internal/mcpserver/orch"] || parseTargets["cmd/mcp-orch"]
	idaTarget := parseTargets["internal/mcpserver/ida"] || parseTargets["cmd/mcp-ida"]
	return orchTarget && orchFacts || idaTarget && idaFacts
}

func declaresProceduralPlatformControlBoundary(parseTargets, facts map[string]bool) bool {
	hooksFacts := facts["internal/platform/mcpcontrol"] || facts["internal/platform/db"]
	return parseTargets["internal/platform/hooks"] && hooksFacts ||
		parseTargets["internal/platform/mcpcontrol"] && facts["internal/platform/hooks"]
}

func backendBoundaryConsumerDeclarationViolations(rel string, declaration ast.Decl) []string {
	if fn, ok := declaration.(*ast.FuncDecl); ok {
		return backendBoundaryConsumerFunctionFactViolations(rel, fn)
	}
	if group, ok := declaration.(*ast.GenDecl); ok {
		return backendBoundaryConsumerGroupFactViolations(rel, group)
	}
	return nil
}

func backendBoundaryConsumerFunctionFactViolations(rel string, fn *ast.FuncDecl) []string {
	if violation := backendBoundarySpecialFunctionViolation(rel, fn); violation != "" {
		return []string{violation}
	}
	if fn.Body == nil {
		return nil
	}
	var violations []string
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if ok && hasBackendBoundaryPolicyLiteralShape(literal) {
			if isExplicitBackendBoundaryValidationLiteral(rel, literal) {
				return false
			}
			violations = append(violations, rel+": function "+fn.Name.Name+" declares local backend boundary policy facts")
			return false
		}
		return true
	})
	if declaresClosedContractAllowlist(fn) {
		violations = append(violations, rel+": function "+fn.Name.Name+" declares a local contract repository allowlist")
	}
	return violations
}

func backendBoundaryAllowsTestPolicyFixture(rel string) bool {
	return filepath.ToSlash(rel) == "internal/archtest/boundary_registry_test.go"
}

func usesCanonicalSQLCBoundaryRule(fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	var callsEvaluator, namesRule bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok && backendBoundaryCallName(call.Fun) == "EvaluateBackendBoundary" {
			callsEvaluator = true
		}
		if value, ok := backendBoundaryStringLiteral(node); ok && value == "store_sqlc_store_platform_only" {
			namesRule = true
		}
		return true
	})
	return callsEvaluator && namesRule && !containsLegacySQLCBoundaryFacts(fn)
}

func containsLegacySQLCBoundaryFacts(fn *ast.FuncDecl) bool {
	var namesSQLCPath, scansImports bool
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			switch backendBoundaryCallName(call.Fun) {
			case "internalPrefix", "parseImportFiles":
				scansImports = true
			}
		}
		if value, ok := backendBoundaryStringLiteral(node); ok && value == "internal/store/sqlc" {
			namesSQLCPath = true
		}
		return !(namesSQLCPath && scansImports)
	})
	return namesSQLCPath && scansImports
}

func declaresProceduralMCPSidecarBoundary(fn *ast.FuncDecl, packageStrings map[string]string) bool {
	if fn.Body == nil {
		return false
	}
	stringsByName := backendBoundaryFunctionStringConstants(fn, packageStrings)
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || backendBoundaryCallName(call.Fun) != "assertNoImportPrefixes" {
			return true
		}
		ast.Inspect(call, func(child ast.Node) bool {
			if parseCall, ok := child.(*ast.CallExpr); ok && backendBoundaryParseImportCallTargetsSidecar(parseCall, stringsByName) {
				found = true
			}
			return !found
		})
		return !found
	})
	return found
}

func backendBoundaryPackageStringConstants(node *ast.File) map[string]string {
	values := make(map[string]string)
	for _, declaration := range node.Decls {
		group, ok := declaration.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range group.Specs {
			if item, ok := spec.(*ast.ValueSpec); ok {
				backendBoundaryCollectValueSpecStrings(item, values)
			}
		}
	}
	return values
}

func backendBoundaryFunctionStringConstants(fn *ast.FuncDecl, packageStrings map[string]string) map[string]string {
	values := make(map[string]string, len(packageStrings))
	maps.Copy(values, packageStrings)
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch item := node.(type) {
		case *ast.AssignStmt:
			backendBoundaryCollectAssignmentStrings(item, values)
		case *ast.ValueSpec:
			backendBoundaryCollectValueSpecStrings(item, values)
		}
		return true
	})
	return values
}

func backendBoundaryCollectAssignmentStrings(item *ast.AssignStmt, values map[string]string) {
	for i, left := range item.Lhs {
		name, ok := left.(*ast.Ident)
		if !ok || i >= len(item.Rhs) {
			continue
		}
		if value, ok := backendBoundaryStringValue(item.Rhs[i], values); ok {
			values[name.Name] = value
		}
	}
}

func backendBoundaryCollectValueSpecStrings(item *ast.ValueSpec, values map[string]string) {
	for i, name := range item.Names {
		if i >= len(item.Values) {
			continue
		}
		if value, ok := backendBoundaryStringValue(item.Values[i], values); ok {
			values[name.Name] = value
		}
	}
}

func backendBoundaryParseImportCallTargetsSidecar(call *ast.CallExpr, values map[string]string) bool {
	if backendBoundaryCallName(call.Fun) != "parseImportFiles" {
		return false
	}
	for _, arg := range call.Args {
		if value, ok := backendBoundaryStringValue(arg, values); ok && strings.HasPrefix(value, "cmd/mcp-") {
			return true
		}
	}
	return false
}

func backendBoundaryStringValue(expr ast.Expr, values map[string]string) (string, bool) {
	switch item := expr.(type) {
	case *ast.BasicLit:
		return backendBoundaryStringLiteral(item)
	case *ast.Ident:
		value, ok := values[item.Name]
		return value, ok
	case *ast.ParenExpr:
		return backendBoundaryStringValue(item.X, values)
	case *ast.BinaryExpr:
		if item.Op != token.ADD {
			return "", false
		}
		left, leftOK := backendBoundaryStringValue(item.X, values)
		right, rightOK := backendBoundaryStringValue(item.Y, values)
		return left + right, leftOK && rightOK
	default:
		return "", false
	}
}

func backendBoundaryCallName(expr ast.Expr) string {
	switch item := expr.(type) {
	case *ast.Ident:
		return item.Name
	case *ast.SelectorExpr:
		return item.Sel.Name
	default:
		return ""
	}
}

func backendBoundaryStringLiteral(node ast.Node) (string, bool) {
	literal, ok := node.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(literal.Value)
	return value, err == nil
}

func backendBoundaryConsumerGroupFactViolations(rel string, group *ast.GenDecl) []string {
	var violations []string
	for _, spec := range group.Specs {
		violations = append(violations, backendBoundaryConsumerSpecFactViolations(rel, spec)...)
	}
	return violations
}

func backendBoundaryConsumerSpecFactViolations(rel string, spec ast.Spec) []string {
	if item, ok := spec.(*ast.TypeSpec); ok {
		return backendBoundaryConsumerTypeFactViolations(rel, item)
	}
	if item, ok := spec.(*ast.ValueSpec); ok {
		return backendBoundaryConsumerValueFactViolations(rel, item)
	}
	return nil
}

func backendBoundaryConsumerTypeFactViolations(rel string, item *ast.TypeSpec) []string {
	structType, ok := item.Type.(*ast.StructType)
	if !ok || (!isLocalBoundaryPolicyType(item.Name.Name) && !hasBackendBoundaryPolicyStructShape(structType)) {
		return nil
	}
	return []string{rel + ": local backend boundary struct " + item.Name.Name + " duplicates registry rule facts"}
}

func hasBackendBoundaryPolicyStructShape(structType *ast.StructType) bool {
	fields := make(map[string]bool)
	for _, field := range structType.Fields.List {
		for _, name := range field.Names {
			fields[name.Name] = true
		}
	}
	if fields["Owners"] && fields["Rules"] {
		return true
	}
	return fields["ID"] && fields["Owner"] && fields["Reason"] && hasAnyBoundaryPolicyField(fields)
}

func hasBackendBoundaryPolicyLiteralShape(literal *ast.CompositeLit) bool {
	fields := make(map[string]bool)
	for _, element := range literal.Elts {
		item, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := item.Key.(*ast.Ident)
		if ok {
			fields[key.Name] = true
		}
	}
	if fields["Owners"] && fields["Rules"] {
		return true
	}
	return fields["ID"] && fields["Owner"] && fields["Reason"] && hasAnyBoundaryPolicyField(fields)
}

func hasAnyBoundaryPolicyField(fields map[string]bool) bool {
	for _, name := range []string{"FilePatterns", "Allow", "Deny", "ScopeAllow", "Exceptions", "DependencyPackages", "ImportPrefix"} {
		if fields[name] {
			return true
		}
	}
	return false
}

func isLocalBoundaryPolicyType(name string) bool {
	if strings.HasPrefix(name, "backendBoundary") {
		return true
	}
	switch name {
	case "boundaryRegistry", "boundaryOwner", "boundaryImportRule", "boundaryException":
		return true
	default:
		return false
	}
}

func backendBoundaryConsumerRuleIDSources(rel string, node *ast.File) map[string][]string {
	ids := map[string][]string{}
	for _, declaration := range node.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || !isBackendBoundaryDefaultRuleFunction(rel, fn) {
			continue
		}
		collectBackendBoundaryRuleIDs(rel, fn, ids)
	}
	return ids
}

func isBackendBoundaryDefaultRuleFunction(rel string, fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	if strings.HasPrefix(name, "Test") {
		return !backendBoundaryAllowsTestPolicyFixture(rel)
	}
	return strings.Contains(name, "Rule") || strings.Contains(name, "Boundary")
}

func collectBackendBoundaryRuleIDs(rel string, fn *ast.FuncDecl, ids map[string][]string) {
	if fn.Body == nil {
		return
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || !hasBackendBoundaryPolicyLiteralShape(literal) {
			return true
		}
		id, ok := backendBoundaryRuleIDLiteral(literal)
		if ok {
			ids[id] = append(ids[id], rel+":"+fn.Name.Name)
		}
		return false
	})
}

func backendBoundaryRuleIDLiteral(literal *ast.CompositeLit) (string, bool) {
	for _, element := range literal.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := field.Key.(*ast.Ident)
		if !ok || key.Name != "ID" {
			continue
		}
		value, ok := field.Value.(*ast.BasicLit)
		if !ok || value.Kind != token.STRING {
			return "", false
		}
		id, err := strconv.Unquote(value.Value)
		return id, err == nil
	}
	return "", false
}
