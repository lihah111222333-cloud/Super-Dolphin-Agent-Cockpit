//go:build ignore

// check_repofp_delegators verifies that RepoFingerprint helpers outside
// internal/util/repofingerprint remain thin delegators.
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

func main() {
	violations, err := findViolations("internal")
	if err != nil {
		fmt.Fprintln(os.Stderr, "walk:", err)
		os.Exit(2)
	}
	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(os.Stderr, v)
		}
		os.Exit(1)
	}
	fmt.Println("ok: all RepoFingerprint outside repofingerprint package are delegators")
}

func findViolations(root string) ([]string, error) {
	violations := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || shouldSkip(path, d) {
			return err
		}
		fileViolations, err := scanFile(path)
		if err != nil {
			return err
		}
		violations = append(violations, fileViolations...)
		return nil
	})
	return violations, err
}

func shouldSkip(path string, d fs.DirEntry) bool {
	if d.IsDir() {
		return true
	}
	if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
		return true
	}
	return strings.HasPrefix(filepath.ToSlash(path), "internal/util/repofingerprint/")
}

func scanFile(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	violations := []string{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !isRepoFingerprint(fn) {
			continue
		}
		if !isDelegatorBody(src, fset, fn) {
			violations = append(violations,
				fmt.Sprintf("%s:%d func RepoFingerprint not a delegator",
					path, fset.Position(fn.Pos()).Line))
		}
	}
	return violations, nil
}

func isRepoFingerprint(fn *ast.FuncDecl) bool {
	return fn.Name.Name == "RepoFingerprint" && fn.Body != nil
}

func isDelegatorBody(src []byte, fset *token.FileSet, fn *ast.FuncDecl) bool {
	start := fset.Position(fn.Body.Pos()).Offset
	end := fset.Position(fn.Body.End()).Offset
	body := string(src[start:end])
	return strings.Contains(body, "repofingerprint.MustCompute(") ||
		strings.Contains(body, "repofingerprint.Compute(")
}
