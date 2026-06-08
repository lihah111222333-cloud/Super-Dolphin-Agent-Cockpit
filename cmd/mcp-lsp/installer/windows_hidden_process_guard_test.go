package installer

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
