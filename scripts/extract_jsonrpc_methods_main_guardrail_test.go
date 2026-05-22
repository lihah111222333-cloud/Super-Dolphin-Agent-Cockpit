package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestExtractJSONRPCMethodsMainShapeAndFileOwnership(t *testing.T) {
	scriptPath := extractJSONRPCMethodsScriptPath(t)
	if filepath.Base(scriptPath) != "extract_jsonrpc_methods.go" {
		t.Fatalf("script path = %q, want extract_jsonrpc_methods.go", scriptPath)
	}

	file := parseExtractJSONRPCMethodsScript(t, scriptPath)
	mainDecl := findExtractJSONRPCMethodsMain(t, file)
	assertExtractJSONRPCMethodsMainShape(t, mainDecl)

	roots := extractMainRoots(mainDecl)
	wantRoots := []string{"internal/module/thread", "internal/module/turn", "internal/module/uistate"}
	if !reflect.DeepEqual(roots, wantRoots) {
		t.Fatalf("main roots = %v, want %v", roots, wantRoots)
	}
	assertExtractJSONRPCMethodsMainFeatures(t, inspectExtractJSONRPCMethodsMain(mainDecl))
}

func parseExtractJSONRPCMethodsScript(t *testing.T, scriptPath string) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, scriptPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse script: %v", err)
	}
	return file
}

func findExtractJSONRPCMethodsMain(t *testing.T, file *ast.File) *ast.FuncDecl {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		if fn.Name == nil {
			continue
		}
		if fn.Name.Name == "main" {
			return fn
		}
	}
	t.Fatal("main function not found in extract_jsonrpc_methods.go")
	return nil
}

func assertExtractJSONRPCMethodsMainShape(t *testing.T, mainDecl *ast.FuncDecl) {
	t.Helper()
	if mainDecl.Recv != nil {
		t.Fatal("main must not be a method")
	}
	if mainDecl.Type.Params != nil && len(mainDecl.Type.Params.List) > 0 {
		t.Fatalf("main params = %#v, want none", mainDecl.Type.Params.List)
	}
	if mainDecl.Type.Results != nil && len(mainDecl.Type.Results.List) > 0 {
		t.Fatalf("main results = %#v, want none", mainDecl.Type.Results.List)
	}
}

type extractMainFeatures struct {
	hasWalkDir     bool
	hasInspect     bool
	hasSort        bool
	skipsTestFiles bool
}

func inspectExtractJSONRPCMethodsMain(mainDecl *ast.FuncDecl) extractMainFeatures {
	features := extractMainFeatures{}
	ast.Inspect(mainDecl.Body, func(n ast.Node) bool {
		features.observe(n)
		return true
	})
	return features
}

func (f *extractMainFeatures) observe(n ast.Node) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if sel.Sel == nil {
		return
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}
	f.observeSelector(pkg.Name, sel.Sel.Name, call.Args)
}

func (f *extractMainFeatures) observeSelector(pkg, name string, args []ast.Expr) {
	switch pkg + "." + name {
	case "filepath.WalkDir":
		f.hasWalkDir = true
	case "ast.Inspect":
		f.hasInspect = true
	case "sort.Strings":
		if callHasIdentArg(args, "out") {
			f.hasSort = true
		}
	case "strings.HasSuffix":
		if callHasStringArg(args, 1, "_test.go") {
			f.skipsTestFiles = true
		}
	}
}

func callHasIdentArg(args []ast.Expr, want string) bool {
	if len(args) != 1 {
		return false
	}
	ident, ok := args[0].(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == want
}

func callHasStringArg(args []ast.Expr, index int, want string) bool {
	if len(args) <= index {
		return false
	}
	lit, ok := args[index].(*ast.BasicLit)
	if !ok {
		return false
	}
	return strings.Trim(lit.Value, "\"") == want
}

func assertExtractJSONRPCMethodsMainFeatures(t *testing.T, features extractMainFeatures) {
	t.Helper()
	if !features.hasWalkDir {
		t.Fatal("main must walk the configured roots with filepath.WalkDir")
	}
	if !features.hasInspect {
		t.Fatal("main must inspect parsed files with ast.Inspect")
	}
	if !features.hasSort {
		t.Fatal("main must sort extracted methods before printing")
	}
	if !features.skipsTestFiles {
		t.Fatal("main must explicitly skip _test.go files")
	}
}

func TestExtractJSONRPCMethodsScript_EmitsSortedUniqueValidMethods(t *testing.T) {
	repoRoot := t.TempDir()
	for _, dir := range []string{"internal/module/thread", "internal/module/turn", "internal/module/uistate"} {
		if err := os.MkdirAll(filepath.Join(repoRoot, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/thread/scan.go", strings.Join([]string{
		"package thread",
		"",
		"const zetaMethod = \"zeta/method\"",
		"",
		"func register(string) {}",
		"",
		"func collect() {",
		"\tregister(zetaMethod)",
		"\tregister(\"bad method!\")",
		"\ts.methods[\"alpha/method\"] = 0",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/turn/scan.go", strings.Join([]string{
		"package turn",
		"",
		"func collectAgain() {",
		"\t_ = handler.Map{\"alpha/method\": nil}",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/thread/ignored_test.go", strings.Join([]string{
		"package thread",
		"",
		"func register(string) {}",
		"",
		"func ignored() {",
		"\tregister(\"test-only/method\")",
		"}",
		"",
	}, "\n"))

	stdout, stderr, err := runExtractJSONRPCMethodsScript(t, repoRoot)
	if err != nil {
		t.Fatalf("run script err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	got := nonEmptyLines(stdout)
	want := []string{"alpha/method", "zeta/method"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stdout lines = %v, want %v", got, want)
	}
	if strings.Contains(stdout, "bad method!") {
		t.Fatalf("stdout should not include invalid methods: %q", stdout)
	}
	if strings.Contains(stdout, "test-only/method") {
		t.Fatalf("stdout should ignore _test.go files: %q", stdout)
	}
}

func TestExtractJSONRPCMethodsScript_ReportsWalkAndParseErrorsButKeepsValidOutput(t *testing.T) {
	repoRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repoRoot, "internal", "module", "thread"), 0o755); err != nil {
		t.Fatalf("mkdir internal/module/thread: %v", err)
	}

	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/thread/good.go", strings.Join([]string{
		"package thread",
		"",
		"func register(string) {}",
		"",
		"func collect() {",
		"\tregister(\"turn/interrupt\")",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/module/thread/broken.go", "package thread\nfunc broken(\n")

	stdout, stderr, err := runExtractJSONRPCMethodsScript(t, repoRoot)
	if err != nil {
		t.Fatalf("run script err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}

	got := nonEmptyLines(stdout)
	want := []string{"turn/interrupt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stdout lines = %v, want %v", got, want)
	}
	normalizedStderr := filepath.ToSlash(stderr)
	for _, wantErr := range []string{
		"extract_jsonrpc_methods: parse internal/module/thread/broken.go",
		"extract_jsonrpc_methods: walk internal/module/turn",
		"extract_jsonrpc_methods: walk internal/module/uistate",
	} {
		if !strings.Contains(normalizedStderr, wantErr) {
			t.Fatalf("stderr = %q, want substring %q", stderr, wantErr)
		}
	}
}

func extractJSONRPCMethodsScriptPath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "extract_jsonrpc_methods.go")
}

func runExtractJSONRPCMethodsScript(t *testing.T, repoRoot string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("go", "run", extractJSONRPCMethodsScriptPath(t))
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func writeExtractJSONRPCMethodsFixture(t *testing.T, repoRoot, relPath, content string) {
	t.Helper()
	path := filepath.Join(repoRoot, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", relPath, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func nonEmptyLines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func extractMainRoots(mainDecl *ast.FuncDecl) []string {
	for _, stmt := range mainDecl.Body.List {
		assign, ok := stmt.(*ast.AssignStmt)
		if !ok {
			continue
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident.Name != "roots" || i >= len(assign.Rhs) {
				continue
			}
			lit, ok := assign.Rhs[i].(*ast.CompositeLit)
			if !ok {
				return nil
			}
			roots := make([]string, 0, len(lit.Elts))
			for _, elt := range lit.Elts {
				str, ok := elt.(*ast.BasicLit)
				if !ok {
					return nil
				}
				roots = append(roots, strings.Trim(str.Value, "\""))
			}
			return roots
		}
	}
	return nil
}
