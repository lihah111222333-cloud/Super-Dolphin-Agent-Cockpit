package nodeexec

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDescriptorStateIsFunctionScoped prevents read-only validation descriptors
// from becoming mutable package globals again.
func TestDescriptorStateIsFunctionScoped(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source directory")
	}

	forbidden := map[string]struct{}{
		"persistedNodeStatusList":          {},
		"persistedNodeStatusSet":           {},
		"reservedOrLegacyNodeStatusSet":    {},
		"agentOutputsForbiddenKeys":        {},
		"automationValidationKeywords":     {},
		"automationNotFoundKeywords":       {},
		"automationTransientKeywords":      {},
		"automationInfrastructureKeywords": {},
		"automationCommandEnvAllowlist":    {},
		"automationOutputsForbiddenKeys":   {},
		"legalTransitions":                 {},
		"unsafeRenderedShellTokens":        {},
		"updateNodeStatusAllowed":          {},
		"removeNodeStatusAllowed":          {},
		"nodePatchBannedDeepKeys":          {},
	}
	files := []string{
		"types.go",
		"executor_agent.go",
		"executor_automation.go",
		"executor_automation_command.go",
		"executor_automation_decode.go",
		"status.go",
		"executor_automation_prompt.go",
		"plan.go",
		"ops.go",
	}
	for _, name := range files {
		path := filepath.Join(filepath.Dir(thisFile), name)
		for _, symbol := range descriptorPackageVars(t, path) {
			if _, blocked := forbidden[symbol]; blocked {
				t.Errorf("%s must be function-scoped, found package var in %s", symbol, path)
			}
		}
	}
}

func descriptorPackageVars(t *testing.T, path string) []string {
	t.Helper()
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filepath.Base(path), err)
	}
	var names []string
	for _, declaration := range parsed.Decls {
		names = append(names, descriptorDeclVarNames(declaration)...)
	}
	return names
}

func descriptorDeclVarNames(declaration ast.Decl) []string {
	gen, ok := declaration.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return nil
	}
	var names []string
	for _, spec := range gen.Specs {
		if value, ok := spec.(*ast.ValueSpec); ok {
			for _, name := range value.Names {
				names = append(names, name.Name)
			}
		}
	}
	return names
}
