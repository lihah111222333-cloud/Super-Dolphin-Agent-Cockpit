package gate

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestRetiredNarrowContainerShardAPIStaysAbsent 防止已由 miss-only 规划替代的旧导出 API 复活。
func TestRetiredNarrowContainerShardAPIStaysAbsent(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	productionFiles, err := filepath.Glob(filepath.Join(filepath.Dir(currentFile), "*.go"))
	if err != nil {
		t.Fatalf("discover gate production sources: %v", err)
	}
	for _, productionFile := range productionFiles {
		if strings.HasSuffix(productionFile, "_test.go") {
			continue
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), productionFile, nil, 0)
		if parseErr != nil {
			t.Fatalf("parse gate production source %s: %v", filepath.Base(productionFile), parseErr)
		}
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if isFunction && function.Recv == nil && function.Name.Name == "NarrowContainerShard" {
				t.Fatalf("retired NarrowContainerShard API was reintroduced in %s", filepath.Base(productionFile))
			}
		}
	}
}
