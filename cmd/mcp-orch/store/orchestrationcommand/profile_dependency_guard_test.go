package orchestrationcommand

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestSendProfileStoreHasNoTransportOrAccessOwnerDependency(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate profile dependency guard")
	}
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "profile_*.go"))
	if err != nil {
		t.Fatalf("glob profile sources: %v", err)
	}
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		assertProfileProductionImports(t, path)
	}
}

func assertProfileProductionImports(t *testing.T, path string) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s imports: %v", path, err)
	}
	for _, item := range file.Imports {
		importPath, err := strconv.Unquote(item.Path.Value)
		if err != nil {
			t.Fatalf("decode %s import %s: %v", path, item.Path.Value, err)
		}
		for _, forbidden := range []string{"internal/mcpserver/common", "store/accesswait", "cmd/mcp-orch/tools"} {
			if strings.Contains(importPath, forbidden) {
				t.Fatalf("profile store production file %s imports forbidden owner %s", path, importPath)
			}
		}
	}
}
