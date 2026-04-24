package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionPrivateAllowlistIntegrity(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	for _, entry := range sessionPrivateRuntimeAllowlist {
		t.Run(entry.Symbol, func(t *testing.T) {
			t.Parallel()
			if !sessionPrivateEntryComplete(entry) {
				t.Fatalf("incomplete session-private allowlist entry: %+v", entry)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(entry.DefinitionPath))); err != nil {
				t.Fatalf("definition path missing for %s: %v", entry.Symbol, err)
			}
			if !goFileDefinesSymbol(t, root, entry.DefinitionPath, entry.Symbol) {
				t.Fatalf("symbol %s not found in %s", entry.Symbol, entry.DefinitionPath)
			}
		})
	}
}

func TestSessionPrivateRuntimeAllowlist(t *testing.T) {
	t.Parallel()
	seenTODO := map[string]struct{}{}
	for _, item := range runtimeOwnershipTODOs {
		key := item.Finding + "|" + item.Path + "|" + item.Symbol
		if _, dup := seenTODO[key]; dup {
			t.Fatalf("duplicate runtime ownership TODO key: %s", key)
		}
		seenTODO[key] = struct{}{}
	}
	root := repoRootForGuardTests(t)
	for _, entry := range sessionPrivateRuntimeAllowlist {
		line := symbolLine(t, root, entry.DefinitionPath, entry.Symbol)
		t.Logf("[P22.1 WARN] session-private %s:%d %s", entry.DefinitionPath, line, entry.Symbol)
	}
}

func sessionPrivateEntryComplete(entry sessionPrivateRuntimeException) bool {
	return entry.DefinitionPath != "" && entry.CallSitePath != "" && entry.Symbol != "" &&
		entry.BridgeShape != "" && entry.ExceptionClass != "" && entry.Reason != "" &&
		entry.RemoveWhen != "" && entry.RollbackWhen != "" && entry.RollbackAction != ""
}

func goFileDefinesSymbol(t *testing.T, root, rel, symbol string) bool {
	t.Helper()
	return symbolLine(t, root, rel, symbol) > 0
}

func symbolLine(t *testing.T, root, rel, symbol string) int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	name := shortSymbolName(symbol)
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fset.Position(fn.Pos()).Line
		}
	}
	return 0
}

func shortSymbolName(symbol string) string {
	if idx := strings.LastIndex(symbol, ")."); idx >= 0 {
		return symbol[idx+2:]
	}
	if idx := strings.LastIndex(symbol, "."); idx >= 0 {
		return symbol[idx+1:]
	}
	return symbol
}
