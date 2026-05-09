package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScatteredDecimalGuard(t *testing.T) {
	t.Parallel()
	root := repoRootForGuardTests(t)
	scanRoots := []string{"cmd", "internal", "pkg"}
	skipDirs := DefaultSkipDirs()

	var violations []string

	for _, sr := range scanRoots {
		abs := filepath.Join(root, sr)
		err := filepath.Walk(abs, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if info.IsDir() {
				if _, skip := skipDirs[info.Name()]; skip {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)

			fset := token.NewFileSet()
			fileNode, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return nil
			}

			for _, decl := range fileNode.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok || gd.Tok != token.VAR {
					continue
				}
				for _, spec := range gd.Specs {
					vspec, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}

					isDecimal := false

					// 检查是否显式声明为 decimal.Decimal
					if sel, ok := vspec.Type.(*ast.SelectorExpr); ok {
						if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "decimal" && sel.Sel.Name == "Decimal" {
							isDecimal = true
						}
					}

					// 检查是否由 decimal 包初始化推导类型
					if !isDecimal && len(vspec.Values) > 0 {
						for _, val := range vspec.Values {
							if call, ok := val.(*ast.CallExpr); ok {
								if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
									if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "decimal" {
										isDecimal = true
										break
									}
								}
							} else if sel, ok := val.(*ast.SelectorExpr); ok {
								if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "decimal" {
									isDecimal = true
									break
								}
							}
						}
					}

					if isDecimal {
						for _, name := range vspec.Names {
							if name.Name != "_" {
								violations = append(violations, fmt.Sprintf("%s:%d: 禁止散乱的全局 Decimal 变量 %q，请合并到单一 struct 容器或彻底消除状态", rel, fset.Position(name.Pos()).Line, name.Name))
							}
						}
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("Scattered Decimal violations (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}
