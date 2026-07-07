package main

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
		"runRename(opts, collectEdits, applyEdits)",
		"filepath.WalkDir(rootAbs",
		"planRenameWalkDir(rootAbs",
		"collectRenamePlans(rootAbs, collect)",
		"applyRenamePlans(plans, applyEditSet)",
		"writeReportWithRollback(trimmedReportOut, rep, opts.Apply, plans)",
		"trimmedReportOut := strings.TrimSpace(opts.ReportOut)",
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
		"type edit struct {",
		"type renamePlan struct {",
		"type editCollector func(",
		"type editApplier func(",
		"func planRenameWalkDir(",
		"func collectRenamePlans(",
		"func buildRenamePlan(",
		"func renameWalkDir(",
		"func processRenameFile(",
		"func applyRenamePlans(",
		"func rollbackRenamePlans(",
		"func writeReportWithRollback(",
		"func applyEdits(",
		"func rewriteImportPath(",
		"func writeReport(",
		"func fatalf(",
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

	shape := collectRenameScriptWorkflowShape(fileAST)
	assertSeenMap(t, "main missing flag.Bool for %q", shape.boolFlags)
	assertSeenMap(t, "main missing flag.String for %q", shape.stringFlags)
	assertSeenMap(t, "main missing call to %s", shape.callTargets)
}

type renameScriptMainShape struct {
	boolFlags   map[string]bool
	stringFlags map[string]bool
	callTargets map[string]bool
}

func newRenameScriptMainShape() renameScriptMainShape {
	return renameScriptMainShape{
		boolFlags:   map[string]bool{"dry-run": false, "apply": false},
		stringFlags: map[string]bool{"report": false, "root": false},
		callTargets: map[string]bool{
			"runRename":               false,
			"collectRenamePlans":      false,
			"filepath.WalkDir":        false,
			"planRenameWalkDir":       false,
			"applyRenamePlans":        false,
			"rollbackRenamePlans":     false,
			"writeReportWithRollback": false,
			"writeReport":             false,
		},
	}
}

func collectRenameScriptWorkflowShape(fileAST *ast.File) renameScriptMainShape {
	shape := newRenameScriptMainShape()
	for _, decl := range fileAST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if ok {
				observeRenameScriptMainCall(&shape, call)
			}
			return true
		})
	}
	return shape
}

func observeRenameScriptMainCall(shape *renameScriptMainShape, call *ast.CallExpr) {
	if ident, ok := call.Fun.(*ast.Ident); ok {
		markSeen(shape.callTargets, ident.Name)
		return
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkgIdent, ok := selector.X.(*ast.Ident)
	if !ok {
		return
	}
	markRenameScriptSelectorCall(shape, pkgIdent.Name, selector.Sel.Name, call.Args)
}

func markRenameScriptSelectorCall(shape *renameScriptMainShape, pkgName, selectorName string, args []ast.Expr) {
	if pkgName == "filepath" && selectorName == "WalkDir" {
		shape.callTargets["filepath.WalkDir"] = true
		return
	}
	if pkgName != "flag" || len(args) == 0 {
		return
	}
	name, ok := stringLiteralValue(args[0])
	if !ok {
		return
	}
	switch selectorName {
	case "Bool":
		markSeen(shape.boolFlags, name)
	case "String":
		markSeen(shape.stringFlags, name)
	}
}

func markSeen(target map[string]bool, name string) {
	if _, exists := target[name]; exists {
		target[name] = true
	}
}

func assertSeenMap(t *testing.T, message string, seenByName map[string]bool) {
	t.Helper()
	for name, seen := range seenByName {
		if !seen {
			t.Fatalf(message, name)
		}
	}
}

func TestRenameScriptPlanTypeAndHelperOwnership(t *testing.T) {
	t.Parallel()

	content := readRenameScriptSourceFile(t, "rename_codexsdk_to_agentsdk.go")
	for _, marker := range []string{
		"type edit struct {",
		"Start  int",
		"End    int",
		"OldLit string",
		"NewLit string",
		"Line   int",
	} {
		if !strings.Contains(content, marker) {
			t.Fatalf("edit shape missing marker %q", marker)
		}
	}

	_, fileAST := parseRenameScriptAST(t)
	for _, funcName := range []string{
		"parseRenameOptions",
		"runRename",
		"collectRenamePlans",
		"planRenameWalkDir",
		"buildRenamePlan",
		"renameWalkDir",
		"processRenameFile",
		"applyRenamePlans",
		"rollbackRenamePlans",
		"writeReportWithRollback",
		"collectEdits",
		"applyEdits",
		"writeReport",
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
	owners := collectMainOwnersInRefactorDir(t, dir, entries)
	if len(owners) != 1 {
		t.Fatalf("non-test main owner count = %d, want 1 (%v)", len(owners), owners)
	}
	return owners[0]
}

func collectMainOwnersInRefactorDir(t *testing.T, dir string, entries []os.DirEntry) []string {
	t.Helper()

	owners := make([]string, 0, 1)
	for _, entry := range entries {
		if skipRefactorOwnerEntry(entry) {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		if fileDeclaresTopLevelFunc(t, path, "main") {
			owners = append(owners, path)
		}
	}
	return owners
}

func skipRefactorOwnerEntry(entry os.DirEntry) bool {
	name := entry.Name()
	return entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go")
}

func fileDeclaresTopLevelFunc(t *testing.T, path, name string) bool {
	t.Helper()

	fset := token.NewFileSet()
	fileAST, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return astFileDeclaresTopLevelFunc(fileAST, name)
}

func astFileDeclaresTopLevelFunc(fileAST *ast.File, name string) bool {
	for _, decl := range fileAST.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return true
		}
	}
	return false
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
