package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestIsClosureAction(t *testing.T) {
	t.Parallel()

	cases := map[string]bool{
		"check":                true,
		"refresh":              true,
		"refresh-dependencies": true,
		"provenance":           true,
		"":                     false,
		"run":                  false,
	}
	for action, want := range cases {
		if got := isClosureAction(action); got != want {
			t.Errorf("isClosureAction(%q) = %t, want %t", action, got, want)
		}
	}
}

func TestRunClosureCheckRejectsUnknownActionBeforeSideEffects(t *testing.T) {
	err := runClosureCheck([]string{"unknown", "--tree", "not-a-real-tree"})
	if err == nil {
		t.Fatal("runClosureCheck() error = nil, want protocol error")
	}
	if got := gatecontract.ExitCodeOf(err); got != gatecontract.ExitProtocol {
		t.Fatalf("runClosureCheck() exit code = %d, want protocol %d: %v", got, gatecontract.ExitProtocol, err)
	}
}

func TestClosureActionsAreNotPackageGlobal(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve closure test file")
	}

	path := filepath.Join(filepath.Dir(thisFile), "closure_cli.go")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read closure source: %v", err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		t.Fatalf("parse closure source: %v", err)
	}
	for _, declaration := range parsed.Decls {
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
				if name.Name == "closureActions" {
					t.Errorf("closureActions must be function-scoped, found package var in %s", path)
				}
			}
		}
	}
}
