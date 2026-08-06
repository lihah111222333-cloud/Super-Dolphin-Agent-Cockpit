package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestOrchCapabilitiesProtocolValues(t *testing.T) {
	t.Parallel()

	want := []string{
		"tools/orchestration",
		"tools/task",
		"tools/workspace",
		"tools/prompt",
		"tools/command",
		"tools/shared_file",
		"tools/video",
	}
	if got := OrchCapabilities(); !slices.Equal(got, want) {
		t.Fatalf("OrchCapabilities() = %v, want %v", got, want)
	}
}

func TestOrchCapabilitiesReturnsIndependentSnapshot(t *testing.T) {
	t.Parallel()

	first := OrchCapabilities()
	first[0] = "mutated"
	second := OrchCapabilities()
	if second[0] == "mutated" {
		t.Fatal("OrchCapabilities() leaked mutation across calls")
	}
}

func TestOrchCapabilitiesAreNotPackageGlobal(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve constants test file")
	}

	path := filepath.Join(filepath.Dir(thisFile), "constants.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read constants source: %v", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse constants source: %v", err)
	}
	if mcpConstantsHasPackageVar(parsed.Decls, "orchCapabilities") {
		t.Errorf("orchCapabilities must be function-scoped, found package var in %s", path)
	}
}

func mcpConstantsHasPackageVar(declarations []ast.Decl, target string) bool {
	for _, declaration := range declarations {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for _, name := range value.Names {
				if name.Name == target {
					return true
				}
			}
		}
	}
	return false
}
