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

// TestAtomicReplaceFileReplacesContent verifies atomic replacement on every supported platform.
func TestAtomicReplaceFileReplacesContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.go")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat fixture: %v", err)
	}

	if err := atomicReplaceFile(path, []byte("new\n"), info.Mode(), defaultFileWriter); err != nil {
		t.Fatalf("atomicReplaceFile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if string(got) != "new\n" {
		t.Fatalf("replaced content = %q, want %q", got, "new\n")
	}
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

// TestWindowsAtomicReplacePreservesMetadataWithoutDirectorySync 锁定 Windows 使用保留 DACL 的替换并只刷新结果文件。
func TestWindowsAtomicReplacePreservesMetadataWithoutDirectorySync(t *testing.T) {
	rename := parseFunctionDecl(t, "atomic_write_windows.go", "Rename")
	if !functionCallsIdentifier(rename, "replaceFilePreservingMetadata") || !functionCallsIdentifier(rename, "syncReplacedFile") {
		t.Fatal("Windows Rename must preserve target metadata and flush the replaced file")
	}
	raw, err := os.ReadFile("atomic_write_windows.go")
	if err != nil {
		t.Fatalf("read Windows atomic writer: %v", err)
	}
	source := string(raw)
	if !strings.Contains(source, `NewProc("ReplaceFileW")`) {
		t.Fatal("Windows atomic writer must call ReplaceFileW")
	}
	forbidden := []string{"MoveFileEx", "REPLACEFILE_IGNORE_ACL_ERRORS", "REPLACEFILE_IGNORE_MERGE_ERRORS"}
	if value := firstContainedString(source, forbidden); value != "" {
		t.Fatalf("Windows atomic writer must not use %s", value)
	}
	syncFile := parseFunctionDecl(t, "atomic_write_windows.go", "syncReplacedFile")
	if !functionCallsSelectorName(syncFile, "OpenFile") || !functionCallsSelectorName(syncFile, "Sync") {
		t.Fatal("Windows atomic writer must open and flush the replaced file")
	}
	syncFn := parseFunctionDecl(t, "atomic_write_parent_windows.go", "syncParentDirectory")
	if functionCallsSelectorName(syncFn, "Open") || functionCallsSelectorName(syncFn, "Sync") {
		t.Fatal("Windows syncParentDirectory must not open or flush a directory handle")
	}
}

// firstContainedString 返回第一个出现在源码中的禁用标记。
func firstContainedString(source string, values []string) string {
	for _, value := range values {
		if strings.Contains(source, value) {
			return value
		}
	}
	return ""
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

func functionCallsSelectorName(fn *ast.FuncDecl, selectorName string) bool {
	found := false
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && selector.Sel.Name == selectorName {
			found = true
			return false
		}
		return true
	})
	return found
}
