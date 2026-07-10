package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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
func TestSQLCBoundaryLegacy(t *testing.T) {}
`
	rel := "internal/archtest/sqlc_boundary_test.go"
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
		archtestDir + "/backend_boundary_evaluator.go":          true,
		archtestDir + "/backend_boundary_registry.go":           true,
		archtestDir + "/backend_boundary_single_source_test.go": true,
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
	var violations []string
	for _, declaration := range node.Decls {
		violations = append(violations, backendBoundaryConsumerDeclarationViolations(rel, declaration)...)
	}
	return violations
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
	if fn.Body == nil || strings.HasPrefix(fn.Name.Name, "Test") {
		return nil
	}
	var violations []string
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if ok && hasBackendBoundaryPolicyLiteralShape(literal) {
			violations = append(violations, rel+": function "+fn.Name.Name+" declares local backend boundary policy facts")
			return false
		}
		return true
	})
	return violations
}

func backendBoundarySpecialFunctionViolation(rel string, fn *ast.FuncDecl) string {
	if rel == "internal/archtest/sqlc_boundary_test.go" && strings.HasPrefix(fn.Name.Name, "Test") && !usesCanonicalSQLCBoundaryRule(fn) {
		return rel + ": local SQLC evaluator duplicates the canonical registry"
	}
	if declaresProceduralMCPSidecarBoundary(fn) {
		return rel + ": procedural MCP sidecar evaluator duplicates the canonical registry"
	}
	switch fn.Name.Name {
	case "defaultBackendBoundaryMatrix", "defaultBoundaryRegistry":
		return rel + ": local default boundary registry duplicates the canonical registry"
	case "moduleOwnerForImportCheck", "moduleSiblingImportViolations", "importedModuleName":
		return rel + ": local module sibling evaluator duplicates the canonical registry"
	case "mcpSidecarFilePatterns", "mcpSidecarImportAllowances":
		return rel + ": local MCP sidecar allowlist helper " + fn.Name.Name + " duplicates the canonical registry"
	default:
		return ""
	}
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
	legacy := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		if call, ok := node.(*ast.CallExpr); ok {
			switch backendBoundaryCallName(call.Fun) {
			case "parseImportFiles", "internalPrefix":
				legacy = true
			}
		}
		if value, ok := backendBoundaryStringLiteral(node); ok && value == "internal/store/sqlc" {
			legacy = true
		}
		return !legacy
	})
	return legacy
}

func declaresProceduralMCPSidecarBoundary(fn *ast.FuncDecl) bool {
	if fn.Body == nil {
		return false
	}
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok || backendBoundaryCallName(call.Fun) != "assertNoImportPrefixes" {
			return true
		}
		ast.Inspect(call, func(child ast.Node) bool {
			if value, ok := backendBoundaryStringLiteral(child); ok && strings.HasPrefix(value, "cmd/mcp-") {
				found = true
			}
			return !found
		})
		return !found
	})
	return found
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

func backendBoundaryConsumerValueFactViolations(rel string, item *ast.ValueSpec) []string {
	for _, name := range item.Names {
		if name.Name == "moduleDBImportAllowlist" {
			return []string{rel + ": local moduleDBImportAllowlist duplicates registry exceptions"}
		}
	}
	return nil
}

func backendBoundaryConsumerRuleIDSources(rel string, node *ast.File) map[string][]string {
	ids := map[string][]string{}
	for _, declaration := range node.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if !ok || !isBackendBoundaryDefaultRuleFunction(fn) {
			continue
		}
		collectBackendBoundaryRuleIDs(rel, fn, ids)
	}
	return ids
}

func isBackendBoundaryDefaultRuleFunction(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	return !strings.HasPrefix(name, "Test") && (strings.Contains(name, "Rule") || strings.Contains(name, "Boundary"))
}

func collectBackendBoundaryRuleIDs(rel string, fn *ast.FuncDecl, ids map[string][]string) {
	if fn.Body == nil {
		return
	}
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		id, ok := backendBoundaryRuleIDLiteral(node)
		if ok {
			ids[id] = append(ids[id], rel+":"+fn.Name.Name)
		}
		return true
	})
}

func backendBoundaryRuleIDLiteral(node ast.Node) (string, bool) {
	field, ok := node.(*ast.KeyValueExpr)
	if !ok {
		return "", false
	}
	key, ok := field.Key.(*ast.Ident)
	if !ok || key.Name != "ID" {
		return "", false
	}
	value, ok := field.Value.(*ast.BasicLit)
	if !ok || value.Kind != token.STRING {
		return "", false
	}
	id, err := strconv.Unquote(value.Value)
	return id, err == nil
}
