package tools

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
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

// TestAtomicReplaceFilePreservesOriginalModeBits locks atomic replacement to the original file mode.
func TestAtomicReplaceFilePreservesOriginalModeBits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.go")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	wantMode := os.FileMode(0o640) | os.ModeSticky
	if err := os.Chmod(path, wantMode); err != nil {
		t.Fatalf("chmod fixture: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}
	if info.Mode()&os.ModeSticky == 0 {
		t.Skipf("filesystem did not preserve sticky bit for regular file: %v", info.Mode())
	}

	if err := atomicReplaceFile(path, []byte("new\n"), info.Mode(), defaultFileWriter); err != nil {
		t.Fatalf("atomicReplaceFile: %v", err)
	}
	gotInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat replaced file: %v", err)
	}
	if gotInfo.Mode()&os.ModeSticky == 0 || gotInfo.Mode().Perm() != wantMode.Perm() {
		t.Fatalf("replaced mode = %v, want permissions %v and sticky bit", gotInfo.Mode(), wantMode.Perm())
	}
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
