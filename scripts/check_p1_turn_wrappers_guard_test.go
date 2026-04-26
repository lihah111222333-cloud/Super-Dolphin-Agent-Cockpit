package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckP1TurnWrappersScript_Guardrails(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, root string)
		wantErr    bool
		wantStderr []string
	}{
		{
			name: "allows boundary thin wrapper and ignores excluded inputs",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeCheckP1SandboxFile(t, root, "internal/apiserver/thin.go", strings.Join([]string{
					"package apiserver",
					"",
					"func captureAndInjectTurnSummary() {",
					"\tstep1()",
					"\tstep2()",
					"\tstep3()",
					"\tstep4()",
					"\tstep5()",
					"\tstep6()",
					"}",
					"",
					"func helperHeavy() {",
					"\tfor range []int{1} {",
					"\t\tbreak",
					"\t}",
					"}",
				}, "\n"))
				writeCheckP1SandboxFile(t, root, "internal/apiserver/codexadapter/ignored.go", strings.Join([]string{
					"package codexadapter",
					"",
					"func mergeTrackedTurnCompletionPayload() {",
					"\tfor range []int{1} {",
					"\t\tbreak",
					"\t}",
					"}",
				}, "\n"))
				writeCheckP1SandboxFile(t, root, "internal/apiserver/ignored_test.go", strings.Join([]string{
					"package apiserver",
					"",
					"func threadStatusTerminalFromPayload() {",
					"\tfor range []int{1} {",
					"\t\tbreak",
					"\t}",
					"}",
				}, "\n"))
				writeCheckP1SandboxFile(t, root, "internal/apiserver/ignored.txt", "func trackedTurnTerminalFromEvent() { for {} }")
			},
		},
		{
			name: "rejects oversized wrapper",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeCheckP1SandboxFile(t, root, "internal/apiserver/too_large.go", strings.Join([]string{
					"package apiserver",
					"",
					"func mergeTrackedTurnCompletionPayload() {",
					"\tstep1()",
					"\tstep2()",
					"\tstep3()",
					"\tstep4()",
					"\tstep5()",
					"\tstep6()",
					"\tstep7()",
					"}",
				}, "\n"))
			},
			wantErr:    true,
			wantStderr: []string{"too_large.go", "mergeTrackedTurnCompletionPayload", "too large for thin wrapper"},
		},
		{
			name: "rejects heavy control flow",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeCheckP1SandboxFile(t, root, "internal/apiserver/heavy.go", strings.Join([]string{
					"package apiserver",
					"",
					"func threadStatusTerminalFromPayload() {",
					"\tfor range []int{1} {",
					"\t\tbreak",
					"\t}",
					"}",
				}, "\n"))
			},
			wantErr:    true,
			wantStderr: []string{"heavy.go", "threadStatusTerminalFromPayload", "contains heavy control flow"},
		},
		{
			name: "reports parse errors",
			setup: func(t *testing.T, root string) {
				t.Helper()
				writeCheckP1SandboxFile(t, root, "internal/apiserver/broken.go", "package apiserver\n\nfunc trackedTurnTerminalFromEvent( {\n")
			},
			wantErr:    true,
			wantStderr: []string{"broken.go: parse error:"},
		},
		{
			name: "fails when not run from repository root",
			setup: func(t *testing.T, root string) {
				t.Helper()
				if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
					t.Fatalf("remove sandbox go.mod: %v", err)
				}
			},
			wantErr:    true,
			wantStderr: []string{"go.mod not found", "repository root"},
		},
		{
			name: "skips when legacy internal apiserver root is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := prepareCheckP1TurnWrappersSandbox(t)
			if tt.setup != nil {
				tt.setup(t, root)
			}

			stdout, stderr, err := runCheckP1TurnWrappersScript(t, root)
			if strings.TrimSpace(stdout) != "" {
				t.Fatalf("stdout = %q, want empty", stdout)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected go run to fail; stderr=%s", stderr)
				}
				for _, want := range tt.wantStderr {
					if !strings.Contains(stderr, want) {
						t.Fatalf("stderr = %q, want substring %q", stderr, want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("go run check_p1_turn_wrappers.go: %v\nstderr=%s", err, stderr)
			}
			if strings.TrimSpace(stderr) != "" {
				t.Fatalf("stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestCheckP1TurnWrappersMain_ShapeAndOwnership(t *testing.T) {
	scriptPath := locateCheckP1TurnWrappersSource(t)
	if filepath.Base(scriptPath) != "check_p1_turn_wrappers.go" {
		t.Fatalf("script path = %s, want check_p1_turn_wrappers.go", scriptPath)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, scriptPath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", scriptPath, err)
	}

	mainDecl := findCheckP1FuncDecl(t, file, "main")
	if mainDecl.Type.Params != nil && len(mainDecl.Type.Params.List) > 0 {
		t.Fatalf("main params = %#v, want no params", mainDecl.Type.Params.List)
	}
	if mainDecl.Type.Results != nil && len(mainDecl.Type.Results.List) > 0 {
		t.Fatalf("main results = %#v, want no results", mainDecl.Type.Results.List)
	}
	if !checkP1FuncCalls(mainDecl, "os", "Exit") {
		t.Fatal("main should keep os.Exit failure path")
	}

	collectDecl := findCheckP1FuncDecl(t, file, "collectP1TurnWrapperViolations")
	if !checkP1FuncCalls(collectDecl, "filepath", "WalkDir") {
		t.Fatal("collectP1TurnWrapperViolations should keep filepath.WalkDir")
	}
	if !checkP1FuncReferencesIdent(collectDecl, "legacyWalkRoot") {
		t.Fatal("collectP1TurnWrapperViolations should use legacyWalkRoot")
	}

	parseDecl := findCheckP1FuncDecl(t, file, "parseAndCheckP1WrapperFile")
	if !checkP1FuncCalls(parseDecl, "parser", "ParseFile") {
		t.Fatal("parseAndCheckP1WrapperFile should keep parser.ParseFile")
	}
}

func findCheckP1FuncDecl(t *testing.T, file *ast.File, name string) *ast.FuncDecl {
	t.Helper()
	var found *ast.FuncDecl
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != name {
			continue
		}
		if found != nil {
			t.Fatalf("found duplicate %s", name)
		}
		found = fd
	}
	if found == nil {
		t.Fatalf("%s not found", name)
	}
	return found
}

func checkP1FuncCalls(fn *ast.FuncDecl, pkgName, funcName string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != funcName {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if ok && pkgIdent.Name == pkgName {
			found = true
		}
		return true
	})
	return found
}

func checkP1FuncReferencesIdent(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		ident, ok := n.(*ast.Ident)
		if ok && ident.Name == name {
			found = true
		}
		return true
	})
	return found
}

func prepareCheckP1TurnWrappersSandbox(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module sandbox\n"), 0o644); err != nil {
		t.Fatalf("write sandbox go.mod: %v", err)
	}
	source := locateCheckP1TurnWrappersSource(t)
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read %s: %v", source, err)
	}
	target := filepath.Join(root, "scripts", "check_p1_turn_wrappers.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir scripts dir: %v", err)
	}
	if err := os.WriteFile(target, data, 0o644); err != nil {
		t.Fatalf("write sandbox script: %v", err)
	}
	return root
}

func runCheckP1TurnWrappersScript(t *testing.T, root string) (string, string, error) {
	t.Helper()
	cmd := exec.Command("go", "run", "./scripts/check_p1_turn_wrappers.go")
	cmd.Dir = root
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func writeCheckP1SandboxFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	path := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

func locateCheckP1TurnWrappersSource(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(thisFile), "check_p1_turn_wrappers.go")
}
