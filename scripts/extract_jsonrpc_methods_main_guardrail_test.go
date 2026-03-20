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

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, scriptPath, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse script: %v", err)
	}

	var mainDecl *ast.FuncDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name != nil && fn.Name.Name == "main" {
			mainDecl = fn
			break
		}
	}
	if mainDecl == nil {
		t.Fatal("main function not found in extract_jsonrpc_methods.go")
	}
	if mainDecl.Recv != nil {
		t.Fatal("main must not be a method")
	}
	if mainDecl.Type.Params != nil && len(mainDecl.Type.Params.List) > 0 {
		t.Fatalf("main params = %#v, want none", mainDecl.Type.Params.List)
	}
	if mainDecl.Type.Results != nil && len(mainDecl.Type.Results.List) > 0 {
		t.Fatalf("main results = %#v, want none", mainDecl.Type.Results.List)
	}

	roots := extractMainRoots(mainDecl)
	wantRoots := []string{"internal/apiserver", "internal/dashrpc", "internal/skills"}
	if !reflect.DeepEqual(roots, wantRoots) {
		t.Fatalf("main roots = %v, want %v", roots, wantRoots)
	}

	var hasWalkDir, hasInspect, hasSort, skipsTestFiles bool
	ast.Inspect(mainDecl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case pkg.Name == "filepath" && sel.Sel.Name == "WalkDir":
			hasWalkDir = true
		case pkg.Name == "ast" && sel.Sel.Name == "Inspect":
			hasInspect = true
		case pkg.Name == "sort" && sel.Sel.Name == "Strings" && len(call.Args) == 1:
			if ident, ok := call.Args[0].(*ast.Ident); ok && ident.Name == "out" {
				hasSort = true
			}
		case pkg.Name == "strings" && sel.Sel.Name == "HasSuffix" && len(call.Args) == 2:
			if lit, ok := call.Args[1].(*ast.BasicLit); ok && strings.Trim(lit.Value, "\"") == "_test.go" {
				skipsTestFiles = true
			}
		}
		return true
	})
	if !hasWalkDir {
		t.Fatal("main must walk the configured roots with filepath.WalkDir")
	}
	if !hasInspect {
		t.Fatal("main must inspect parsed files with ast.Inspect")
	}
	if !hasSort {
		t.Fatal("main must sort extracted methods before printing")
	}
	if !skipsTestFiles {
		t.Fatal("main must explicitly skip _test.go files")
	}
}

func TestExtractJSONRPCMethodsScript_EmitsSortedUniqueValidMethods(t *testing.T) {
	repoRoot := t.TempDir()
	for _, dir := range []string{"internal/apiserver", "internal/dashrpc", "internal/skills"} {
		if err := os.MkdirAll(filepath.Join(repoRoot, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/apiserver/scan.go", strings.Join([]string{
		"package apiserver",
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
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/dashrpc/scan.go", strings.Join([]string{
		"package dashrpc",
		"",
		"func register(string) {}",
		"",
		"func collectAgain() {",
		"\tregister(\"alpha/method\")",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/apiserver/ignored_test.go", strings.Join([]string{
		"package apiserver",
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
	if err := os.MkdirAll(filepath.Join(repoRoot, "internal", "apiserver"), 0o755); err != nil {
		t.Fatalf("mkdir internal/apiserver: %v", err)
	}

	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/apiserver/good.go", strings.Join([]string{
		"package apiserver",
		"",
		"func register(string) {}",
		"",
		"func collect() {",
		"\tregister(\"turn/interrupt\")",
		"}",
		"",
	}, "\n"))
	writeExtractJSONRPCMethodsFixture(t, repoRoot, "internal/apiserver/broken.go", "package apiserver\nfunc broken(\n")

	stdout, stderr, err := runExtractJSONRPCMethodsScript(t, repoRoot)
	if err != nil {
		t.Fatalf("run script err=%v stdout=%q stderr=%q", err, stdout, stderr)
	}

	got := nonEmptyLines(stdout)
	want := []string{"turn/interrupt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("stdout lines = %v, want %v", got, want)
	}
	for _, wantErr := range []string{
		"extract_jsonrpc_methods: parse internal/apiserver/broken.go",
		"extract_jsonrpc_methods: walk internal/dashrpc",
		"extract_jsonrpc_methods: walk internal/skills",
	} {
		if !strings.Contains(stderr, wantErr) {
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
