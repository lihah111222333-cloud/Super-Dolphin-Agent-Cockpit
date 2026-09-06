package orchestrationcommand

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

// TestCommandAttemptGatewayHasNoProviderOrLegacyOutboxDependency keeps the
// durable owner at the provider boundary: adapters submit only the returned
// permit and later write a typed result. The gateway cannot accidentally call
// a provider or reuse direct-peer/event-outbox/command-card storage.
func TestCommandAttemptGatewayHasNoProviderOrLegacyOutboxDependency(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate attempt owner")
	}
	dir := filepath.Dir(file)
	for _, name := range []string{"attempt_contract.go", "attempt_store.go", "attempt_execute.go"} {
		assertAttemptOwnerFileComplies(t, dir, name)
	}
}

func assertAttemptOwnerFileComplies(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	assertNoForbiddenAttemptImports(t, name, parsed)
	assertNoForbiddenAttemptIdentifiers(t, name, parsed)
}

func assertNoForbiddenAttemptImports(t *testing.T, name string, parsed *ast.File) {
	t.Helper()
	for _, spec := range parsed.Imports {
		imported, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote %s import: %v", name, err)
		}
		for _, forbidden := range []string{"/provider", "/directpeer", "/eventstore", "/commandcard", "net/http", "os/exec"} {
			if strings.Contains(imported, forbidden) {
				t.Fatalf("%s imports forbidden boundary %q", name, imported)
			}
		}
	}
}

func assertNoForbiddenAttemptIdentifiers(t *testing.T, name string, parsed *ast.File) {
	t.Helper()
	ast.Inspect(parsed, func(node ast.Node) bool {
		ident, ok := node.(*ast.Ident)
		if !ok {
			return true
		}
		if isForbiddenAttemptIdentifier(ident.Name) {
			t.Fatalf("%s contains forbidden owner identifier %q", name, ident.Name)
		}
		return true
	})
}

func isForbiddenAttemptIdentifier(name string) bool {
	switch name {
	case "Provider", "DirectPeer", "CommandCard", "EventOutbox":
		return true
	default:
		return false
	}
}
