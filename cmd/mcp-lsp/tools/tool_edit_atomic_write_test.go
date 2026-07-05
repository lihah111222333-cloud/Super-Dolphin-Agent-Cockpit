package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestReplaceRangePreservesOriginalWhenInitialWriteFails locks replace_range onto atomic disk replacement.
func TestReplaceRangePreservesOriginalWhenInitialWriteFails(t *testing.T) {
	assertEditFunctionUsesAtomicWrite(t, "tool_edit_replace_update.go", "applyReplaceRangeUpdate")
}

// TestTextEditActionPreservesOriginalWhenWriteFails locks LSP text edits onto atomic disk replacement.
func TestTextEditActionPreservesOriginalWhenWriteFails(t *testing.T) {
	assertEditFunctionUsesAtomicWrite(t, "tool_edit_lsp_actions.go", "applyTextEditsToPath")
}

// TestRollbackUsesAtomicWrite ensures rollback paths cannot reintroduce truncating writes.
func TestRollbackUsesAtomicWrite(t *testing.T) {
	assertEditFunctionUsesAtomicWrite(t, "tool_edit_replace_update.go", "rollbackReplaceRangeUpdate")
	assertEditFunctionUsesAtomicWrite(t, "tool_edit_rename.go", "applyWorkspaceEdit")
}

func assertEditFunctionUsesAtomicWrite(t *testing.T, fileName string, funcName string) {
	t.Helper()
	fn := parseFunctionDecl(t, fileName, funcName)
	if functionCallsSelector(fn, "os", "WriteFile") {
		t.Fatalf("%s must preserve original content with atomicReplaceFile; found direct os.WriteFile", funcName)
	}
	if !functionCallsIdentifier(fn, "atomicReplaceFile") {
		t.Fatalf("%s must write through atomicReplaceFile", funcName)
	}
}

func parseFunctionDecl(t *testing.T, path string, funcName string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == funcName {
			return fn
		}
	}
	t.Fatalf("function %s not found in %s", funcName, path)
	return nil
}

func functionCallsIdentifier(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		if ok && ident.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func functionCallsSelector(fn *ast.FuncDecl, packageName string, selectorName string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != selectorName {
			return true
		}
		ident, ok := selector.X.(*ast.Ident)
		if ok && strings.TrimSpace(ident.Name) == packageName {
			found = true
			return false
		}
		return true
	})
	return found
}
