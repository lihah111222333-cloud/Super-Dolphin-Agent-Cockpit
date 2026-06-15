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
	"turnOutputDelta": {},
}

const (
	maxThinFuncLines = 8
	wrapperWalkRoot  = "internal/provider/codexapp"
	wrapperSkipDir   = "internal/provider/codexapp/testdata"
)

func main() {
	violations := collectP1TurnWrapperViolations()
	if len(violations) == 0 {
		return
	}
	for _, v := range violations {
		fmt.Fprintln(os.Stderr, "FAIL:", v)
	}
	os.Exit(1)
}

func collectP1TurnWrapperViolations() []string {
	if err := requireRepoRootMarker(); err != nil {
		return []string{err.Error()}
	}
	if violation := wrapperRootViolation(); violation != "" {
		return []string{violation}
	}

	fset := token.NewFileSet()
	var violations []string
	foundTarget := false
	if err := filepath.WalkDir(wrapperWalkRoot, visitP1TurnWrapperPath(fset, &violations, &foundTarget)); err != nil {
		violations = append(violations, fmt.Sprintf("walk %s: %v", wrapperWalkRoot, err))
	}
	if !foundTarget {
		violations = append(violations, fmt.Sprintf("no target wrapper functions found under %s", wrapperWalkRoot))
	}
	return violations
}

func requireRepoRootMarker() error {
	if _, err := os.Stat("go.mod"); err != nil {
		return fmt.Errorf("go.mod not found; run check_p1_turn_wrappers.go from repository root: %w", err)
	}
	return nil
}

func wrapperRootViolation() string {
	_, err := os.Stat(wrapperWalkRoot)
	if err == nil {
		return ""
	}
	if os.IsNotExist(err) {
		return fmt.Sprintf("%s not found", wrapperWalkRoot)
	}
	return fmt.Sprintf("stat %s: %v", wrapperWalkRoot, err)
}

func visitP1TurnWrapperPath(fset *token.FileSet, violations *[]string, foundTarget *bool) fs.WalkDirFunc {
	return func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			*violations = append(*violations, fmt.Sprintf("walk %s: %v", path, walkErr))
			return walkErr
		}
		if isLegacySkipDir(path, d) {
			return filepath.SkipDir
		}
		if shouldSkipP1WrapperPath(path, d) {
			return nil
		}
		*violations = append(*violations, parseAndCheckP1WrapperFile(fset, path, foundTarget)...)
		return nil
	}
}

func isLegacySkipDir(path string, d fs.DirEntry) bool {
	return d != nil && d.IsDir() && filepath.Clean(path) == filepath.Clean(wrapperSkipDir)
}

func shouldSkipP1WrapperPath(path string, d fs.DirEntry) bool {
	if d == nil || d.IsDir() {
		return true
	}
	return !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go")
}

func parseAndCheckP1WrapperFile(fset *token.FileSet, path string, foundTarget *bool) []string {
	file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return []string{fmt.Sprintf("%s: parse error: %v", path, err)}
	}
	return checkParsedFileWrappers(fset, path, file, foundTarget)
}

// checkParsedFileWrappers 处理check已解析文件wrappers。
func checkParsedFileWrappers(fset *token.FileSet, path string, file *ast.File, foundTarget *bool) []string {
	var violations []string
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Body == nil {
			continue
		}
		if _, ok := targetFuncs[fd.Name.Name]; !ok {
			continue
		}
		if foundTarget != nil {
			*foundTarget = true
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
