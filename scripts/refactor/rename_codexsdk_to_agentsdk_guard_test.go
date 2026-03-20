package main

/* ROLLBACK_SKIP_START

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// =============================================================
// code_size_guard allowlist 路径验证
// =============================================================

func TestGuardAllowlistPathIntegrity_RenameCodexSDKScript(t *testing.T) {
	t.Parallel()

	content := readRenameScriptSourceFile(t, "rename_codexsdk_to_agentsdk.go")
	if !strings.Contains(content, "func main()") {
		t.Fatal("main not found in rename_codexsdk_to_agentsdk.go")
	}
}

func TestSplitGuard_RenameScriptWorkflowAnchors(t *testing.T) {
	t.Parallel()

	content := readRenameScriptSourceFile(t, "rename_codexsdk_to_agentsdk.go")
	for _, marker := range []string{
		"flag.Bool(\"dry-run\"",
		"flag.Bool(\"apply\"",
		"filepath.WalkDir(rootAbs",
		"planRenameWalkDir(rootAbs",
		"collectEdits(path, src)",
		"applyEdits(src, edits)",
		"applyRenamePlans(plans, applyEditSet)",
		"rollbackRenamePlans(plans)",
		"trimmedReportOut := strings.TrimSpace(*reportOut)",
		"func collectEdits(",
		"func writeReport(",
	} {
		if !strings.Contains(content, marker) {
			t.Fatalf("rename_codexsdk_to_agentsdk.go missing marker %q", marker)
		}
	}
}

func TestSplitGuard_RenameScriptHelperPlacement(t *testing.T) {
	t.Parallel()

	content := readRenameScriptSourceFile(t, "rename_codexsdk_to_agentsdk.go")
	for _, marker := range []string{
		"type renamePlan struct {",
		"func planRenameWalkDir(",
		"func buildRenamePlan(",
		"func recordRenamePlan(",
		"func applyEdits(",
		"func applyRenamePlans(",
		"func rollbackRenamePlans(",
		"func rewriteImportPath(",
		"func writeReport(",
		"func fatalf(",
		"FileMode     fs.FileMode",
		"sort.Slice(edits",
		"json.MarshalIndent(rep, \"\", \"  \")",
		"return os.WriteFile(path, append(data, '\\n'), 0o644)",
	} {
		if !strings.Contains(content, marker) {
			t.Fatalf("rename_codexsdk_to_agentsdk.go missing marker %q", marker)
		}
	}
}

func TestRenameScriptMainASTShapeAndOwnership(t *testing.T) {
	t.Parallel()

	scriptPath, fileAST := parseRenameScriptAST(t)
	if got := filepath.Base(scriptPath); got != "rename_codexsdk_to_agentsdk.go" {
		t.Fatalf("main file = %q, want %q", got, "rename_codexsdk_to_agentsdk.go")
	}
	ownerPath := findMainOwnerInRefactorDir(t, filepath.Dir(scriptPath))
	if ownerPath != scriptPath {
		t.Fatalf("main declared in %q, want %q", ownerPath, scriptPath)
	}

	mainDecl := findTopLevelFuncDecl(t, fileAST, "main")
	if mainDecl.Recv != nil {
		t.Fatal("main must remain a top-level function")
	}
	if got := countFieldList(mainDecl.Type.Params); got != 0 {
		t.Fatalf("main params = %d, want 0", got)
	}
	if got := countFieldList(mainDecl.Type.Results); got != 0 {
		t.Fatalf("main results = %d, want 0", got)
	}

	boolFlags := map[string]bool{"dry-run": false, "apply": false}
	stringFlags := map[string]bool{"report": false, "root": false}
	callTargets := map[string]bool{
		"filepath.WalkDir":    false,
		"planRenameWalkDir":   false,
		"collectEdits":        false,
		"applyEdits":          false,
		"applyRenamePlans":    false,
		"rollbackRenamePlans": false,
		"writeReport":         false,
	}

	ast.Inspect(mainDecl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			pkgIdent, ok := fun.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case pkgIdent.Name == "flag" && fun.Sel.Name == "Bool":
				if len(call.Args) > 0 {
					if name, ok := stringLiteralValue(call.Args[0]); ok {
						if _, exists := boolFlags[name]; exists {
							boolFlags[name] = true
						}
					}
				}
			case pkgIdent.Name == "flag" && fun.Sel.Name == "String":
				if len(call.Args) > 0 {
					if name, ok := stringLiteralValue(call.Args[0]); ok {
						if _, exists := stringFlags[name]; exists {
							stringFlags[name] = true
						}
					}
				}
			case pkgIdent.Name == "filepath" && fun.Sel.Name == "WalkDir":
				callTargets["filepath.WalkDir"] = true
			}
		case *ast.Ident:
			if _, exists := callTargets[fun.Name]; exists {
				callTargets[fun.Name] = true
			}
		}
		return true
	})

	for name, seen := range boolFlags {
		if !seen {
			t.Fatalf("main missing flag.Bool for %q", name)
		}
	}
	for name, seen := range stringFlags {
		if !seen {
			t.Fatalf("main missing flag.String for %q", name)
		}
	}
	for name, seen := range callTargets {
		if !seen {
			t.Fatalf("main missing call to %s", name)
		}
	}
}

func TestRenameScriptPlanTypeAndHelperOwnership(t *testing.T) {
	t.Parallel()

	content := readRenameScriptSourceFile(t, "rename_codexsdk_to_agentsdk.go")
	for _, marker := range []string{
		"type renamePlan struct {",
		"Path         string",
		"Rel          string",
		"Src          []byte",
		"Edits        []edit",
		"Replacements []replacement",
		"FileMode     fs.FileMode",
	} {
		if !strings.Contains(content, marker) {
			t.Fatalf("rename plan shape missing marker %q", marker)
		}
	}

	_, fileAST := parseRenameScriptAST(t)
	for _, funcName := range []string{
		"planRenameWalkDir",
		"buildRenamePlan",
		"recordRenamePlan",
		"applyRenamePlans",
		"rollbackRenamePlans",
	} {
		fn := findTopLevelFuncDecl(t, fileAST, funcName)
		if fn.Recv != nil {
			t.Fatalf("%s must remain a top-level function", funcName)
		}
	}
}

func parseRenameScriptAST(t *testing.T) (string, *ast.File) {
	t.Helper()

	scriptPath := renameScriptSourcePath(t)
	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, scriptPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", scriptPath, err)
	}
	return scriptPath, fileAST
}

func findMainOwnerInRefactorDir(t *testing.T, dir string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	owners := make([]string, 0, 1)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		fset := token.NewFileSet()
		fileAST, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, decl := range fileAST.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == "main" {
				owners = append(owners, path)
			}
		}
	}
	if len(owners) != 1 {
		t.Fatalf("non-test main owner count = %d, want 1 (%v)", len(owners), owners)
	}
	return owners[0]
}

func findTopLevelFuncDecl(t *testing.T, fileAST *ast.File, name string) *ast.FuncDecl {
	t.Helper()

	for _, decl := range fileAST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	t.Fatalf("top-level func %s not found", name)
	return nil
}

func countFieldList(list *ast.FieldList) int {
	if list == nil {
		return 0
	}
	return list.NumFields()
}

func stringLiteralValue(expr ast.Expr) (string, bool) {
	basic, ok := expr.(*ast.BasicLit)
	if !ok || basic.Kind != token.STRING {
		return "", false
	}
	value, err := strconv.Unquote(basic.Value)
	if err != nil {
		return "", false
	}
	return value, true
}

func readRenameScriptSourceFile(t *testing.T, fileName string) string {
	t.Helper()

	scriptPath := renameScriptSourcePath(t)
	targetPath := filepath.Join(filepath.Dir(scriptPath), fileName)
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("read %s: %v", fileName, err)
	}
	return string(data)
}

func renameScriptSourcePath(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "rename_codexsdk_to_agentsdk.go")
}

ROLLBACK_SKIP_END */
