package directpeercandidate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestDirectPeerCandidateIsNotProductionWired(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate candidate package")
	}
	set := token.NewFileSet()
	for _, name := range []string{"adapter.go"} {
		assertCandidateImports(t, set, filepath.Join(filepath.Dir(file), name), name)
	}
}

func assertCandidateImports(t *testing.T, set *token.FileSet, path, name string) {
	t.Helper()
	parsed, err := parser.ParseFile(set, path, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	for _, spec := range parsed.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote %s import: %v", name, err)
		}
		assertAllowedCandidateImport(t, imported)
	}
	ast.Inspect(parsed, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		for _, forbidden := range []string{"Dispatcher", "Provider", "Registry", "Fx"} {
			if ident.Name == forbidden {
				t.Fatalf("candidate contains production owner identifier %q", forbidden)
			}
		}
		return true
	})
}

func assertAllowedCandidateImport(t *testing.T, imported string) {
	t.Helper()
	for _, forbidden := range []string{"/provider", "/tools", "go.uber.org/fx", "net/http", "os/exec"} {
		if strings.Contains(imported, forbidden) {
			t.Fatalf("candidate imports forbidden production boundary %q", imported)
		}
	}
}
