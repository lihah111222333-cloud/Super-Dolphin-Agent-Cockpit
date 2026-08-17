package installer

// 本静态 AST 守卫故意不加 windows build tag：它不启动 Windows 进程，所有
// 平台都应阻止公共安装路径绕过 hiddenexec。

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

func TestWindowsInstallerProcessesUseHiddenExecHelper(t *testing.T) {
	data, err := os.ReadFile("installer.go")
	if err != nil {
		t.Fatal(err)
	}
	if hasDirectExecCommandCall(t, "installer.go", data) {
		t.Fatal("installer.go starts production installer processes without the Windows hidden-console helper")
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
