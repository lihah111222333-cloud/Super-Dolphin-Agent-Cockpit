//go:build ignore

package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var targetFuncs = map[string]struct{}{
	"captureAndInjectTurnSummary":       {},
	"mergeTrackedTurnCompletionPayload": {},
	"threadStatusTerminalFromPayload":   {},
	"trackedTurnTerminalFromEvent":      {},
}

const maxThinFuncLines = 8

func main() {
	fset := token.NewFileSet()
	var violations []string
	skipDir := filepath.Clean("internal/apiserver/codexadapter")

	if err := filepath.WalkDir("internal/apiserver", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			violations = append(violations, fmt.Sprintf("walk %s: %v", path, walkErr))
			return nil
		}
		if d.IsDir() {
			if filepath.Clean(path) == skipDir {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s: parse error: %v", path, err))
			return nil
		}
		violations = append(violations, checkParsedFileWrappers(fset, path, file)...)
		return nil
	}); err != nil {
		violations = append(violations, fmt.Sprintf("walk internal/apiserver: %v", err))
	}

	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, "FAIL:", v)
		}
		os.Exit(1)
	}
}

func checkParsedFileWrappers(fset *token.FileSet, path string, file *ast.File) []string {
	var violations []string
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Body == nil {
			continue
		}
		if _, ok := targetFuncs[fd.Name.Name]; !ok {
			continue
		}
		startLine := fset.Position(fd.Pos()).Line
		lineCount := fset.Position(fd.End()).Line - startLine + 1
		if lineCount > maxThinFuncLines {
			violations = append(violations, fmt.Sprintf("%s:%d func %s too large for thin wrapper (%d lines > %d)", path, startLine, fd.Name.Name, lineCount, maxThinFuncLines))
			continue
		}
		if hasHeavyControlFlow(fd.Body) {
			violations = append(violations, fmt.Sprintf("%s:%d func %s contains heavy control flow, not a thin wrapper", path, startLine, fd.Name.Name))
		}
	}
	return violations
}

func hasHeavyControlFlow(body *ast.BlockStmt) bool {
	heavy := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n.(type) {
		case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt, *ast.GoStmt:
			heavy = true
			return false
		default:
			return true
		}
	})
	return heavy
}
