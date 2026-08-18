package multilsp

// 本静态守卫确保公共 multilsp 文件不按运行时 GOOS 选择平台专用系统行为。
// 平台行为必须落在带显式 build constraint 的 helper 文件中，避免交叉编译时
// 公共代码继续携带只在某个平台有意义的分支。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func TestMultilspCommonFilesDoNotSelectPlatformBehaviorAtRuntime(t *testing.T) {
	for _, filename := range []string{"manager.go", "typescript_navigation_fallback.go"} {
		t.Run(filename, func(t *testing.T) {
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			file, err := parser.ParseFile(token.NewFileSet(), filename, data, 0)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok || selector.Sel.Name != "GOOS" {
					return true
				}
				receiver, ok := selector.X.(*ast.Ident)
				if ok && receiver.Name == "runtime" {
					t.Fatalf("%s selects platform behavior through runtime.GOOS; move the helper behind a build-tagged file", filename)
				}
				return true
			})
		})
	}
}
