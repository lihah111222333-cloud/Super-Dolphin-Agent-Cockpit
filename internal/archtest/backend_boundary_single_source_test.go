package archtest_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

var backendBoundaryRuleConsumerFiles = []string{
	"internal/archtest/backend_boundary_matrix_test.go",
	"internal/archtest/dependency_direction_test.go",
	"internal/archtest/dependency_direction_mcp_orch_test.go",
}

// TestBackendBoundaryRuleFactsHaveOneSource prevents consumers from reintroducing registry policy facts.
func TestBackendBoundaryRuleFactsHaveOneSource(t *testing.T) {
	t.Helper()
	root := repoRoot(t)
	var violations []string
	ruleIDSources := map[string][]string{}
	for _, rel := range backendBoundaryRuleConsumerFiles {
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
	switch fn.Name.Name {
	case "defaultBackendBoundaryMatrix":
		return []string{rel + ": local defaultBackendBoundaryMatrix duplicates the canonical registry"}
	case "mcpSidecarFilePatterns", "mcpSidecarImportAllowances":
		return []string{rel + ": local MCP sidecar allowlist helper " + fn.Name.Name + " duplicates the canonical registry"}
	default:
		return nil
	}
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
	if _, ok := item.Type.(*ast.StructType); !ok || !strings.HasPrefix(item.Name.Name, "backendBoundary") {
		return nil
	}
	return []string{rel + ": local backend boundary struct " + item.Name.Name + " duplicates registry rule facts"}
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
