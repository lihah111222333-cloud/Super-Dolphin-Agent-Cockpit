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

func TestStatelessGuard(t *testing.T) {
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

			isStatelessLayer := isMatchedLayer(rel, "/domain") ||
				isMatchedLayer(rel, "/service") ||
				isMatchedLayer(rel, "/handler") ||
				isMatchedLayer(rel, "/controller")

			if !isStatelessLayer {
				return nil
			}

			fset := token.NewFileSet()
			fileNode, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return nil
			}

			globalVarCount := countGlobalVars(fileNode)
			if globalVarCount > 0 {
				violations = append(violations, fmt.Sprintf("%s: 核心业务层违规: 必须保持无状态，禁止任何包级全局变量 var（当前发现 %d 个）", rel, globalVarCount))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", sr, err)
		}
	}

	if len(violations) > 0 {
		t.Fatalf("Stateless violations (%d):\n  %s", len(violations), strings.Join(violations, "\n  "))
	}
}

func countGlobalVars(node *ast.File) int {
	count := 0
	for _, decl := range node.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if ok {
				count += countValidNames(vs.Names)
			}
		}
	}
	return count
}

func countValidNames(names []*ast.Ident) int {
	valid := 0
	for _, name := range names {
		if name.Name != "_" {
			valid++
		}
	}
	return valid
}
