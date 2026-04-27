//go:build ignore

// check_repofp_delegators 校验：仓内除 internal/platform/repofingerprint 外，
// 任何 `func RepoFingerprint(...)` 都必须是 thin delegator —— 函数体内
// 必须命中 `repofingerprint.MustCompute(` 或 `repofingerprint.Compute(`。
//
// 用途：docs/security/p21-redteam.sh 的 RT-1b 断言。
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
	root := "internal"
	violations := []string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		// 共享实现自身允许有 RepoFingerprint / Compute 的"非 delegator"函数。
		if strings.HasPrefix(filepath.ToSlash(path), "internal/platform/repofingerprint/") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		src, _ := os.ReadFile(path)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "RepoFingerprint" || fn.Body == nil {
				continue
			}
			start := fset.Position(fn.Body.Pos()).Offset
			end := fset.Position(fn.Body.End()).Offset
			body := string(src[start:end])
			if !strings.Contains(body, "repofingerprint.MustCompute(") &&
				!strings.Contains(body, "repofingerprint.Compute(") {
				violations = append(violations,
					fmt.Sprintf("%s:%d func RepoFingerprint not a delegator",
						path, fset.Position(fn.Pos()).Line))
			}
		}
		return nil
	})
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
