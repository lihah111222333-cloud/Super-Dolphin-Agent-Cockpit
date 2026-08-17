package multilsp

// 本静态 AST 守卫故意不加 windows build tag：它只检查源码调用形状，不执行
// Win32 API，因此所有平台都必须运行以防隐藏窗口策略回退。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsChildProcessesUseHiddenExecHelper(t *testing.T) {
	files := []string{
		"transport_conn.go",
		"go_toolchain.go",
		"gomod.go",
		"jsts_dependency_prepare.go",
	}
	for _, file := range files {
		t.Run(file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(".", file))
			if err != nil {
				t.Fatal(err)
			}
			if hasDirectExecCommandCall(t, file, data) {
				t.Fatalf("%s starts production child processes without the Windows hidden-console helper", file)
			}
		})
	}
}

func hasDirectExecCommandCall(t *testing.T, filename string, data []byte) bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), filename, data, 0)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, ok := selector.X.(*ast.Ident)
		if ok && receiver.Name == "exec" && (selector.Sel.Name == "Command" || selector.Sel.Name == "CommandContext") {
			found = true
			return false
		}
		return true
	})
	return found
}
