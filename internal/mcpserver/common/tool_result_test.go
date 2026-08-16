package common

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestResolveToolResultTextHasNoGlobalRendererDependency(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "tool_result.go", nil, 0)
	if err != nil {
		t.Fatalf("parse tool_result.go: %v", err)
	}
	forbidden := map[string]struct{}{
		"registeredPlainTextRenderer":         {},
		"plainTextRendererState":              {},
		"RegisterToolResultPlainTextRenderer": {},
		"currentPlainTextRenderer":            {},
	}
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			if _, found := forbidden[declaration.Name.Name]; found {
				t.Fatalf("tool_result.go retains deprecated global renderer declaration %q", declaration.Name.Name)
			}
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				rejectGlobalRendererSpec(t, spec, forbidden)
			}
		}
	}
}

func rejectGlobalRendererSpec(t *testing.T, spec ast.Spec, forbidden map[string]struct{}) {
	t.Helper()
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		if _, found := forbidden[spec.Name.Name]; found {
			t.Fatalf("tool_result.go retains deprecated global renderer declaration %q", spec.Name.Name)
		}
	case *ast.ValueSpec:
		for _, name := range spec.Names {
			if _, found := forbidden[name.Name]; found {
				t.Fatalf("tool_result.go retains deprecated global renderer declaration %q", name.Name)
			}
		}
	}
}
