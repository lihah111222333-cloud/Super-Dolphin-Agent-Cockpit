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
			name:       "fails when internal apiserver root is missing",
			wantErr:    true,
			wantStderr: []string{"walk internal/apiserver:"},
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

	var mainDecl *ast.FuncDecl
	for _, decl := range file.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != "main" {
			continue
		}
		if mainDecl != nil {
			t.Fatalf("found duplicate main in %s", scriptPath)
		}
		mainDecl = fd
	}
	if mainDecl == nil {
		t.Fatalf("main not found in %s", scriptPath)
	}
	if mainDecl.Type.Params != nil && len(mainDecl.Type.Params.List) > 0 {
		t.Fatalf("main params = %#v, want no params", mainDecl.Type.Params.List)
	}
	if mainDecl.Type.Results != nil && len(mainDecl.Type.Results.List) > 0 {
		t.Fatalf("main results = %#v, want no results", mainDecl.Type.Results.List)
	}

	var (
		sawWalkDir    bool
		walkRoot      string
		sawParseFile  bool
		sawExit       bool
		exitCodeValue string
	)
	ast.Inspect(mainDecl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch pkgIdent.Name + "." + sel.Sel.Name {
		case "filepath.WalkDir":
			sawWalkDir = true
			if len(call.Args) > 0 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok {
					walkRoot = strings.Trim(lit.Value, "\"")
				}
			}
		case "parser.ParseFile":
			sawParseFile = true
		case "os.Exit":
			sawExit = true
			if len(call.Args) > 0 {
				if lit, ok := call.Args[0].(*ast.BasicLit); ok {
					exitCodeValue = lit.Value
				}
			}
		}
		return true
	})

	if !sawWalkDir {
		t.Fatal("main should keep filepath.WalkDir")
	}
	if walkRoot != "internal/apiserver" {
		t.Fatalf("WalkDir root = %q, want %q", walkRoot, "internal/apiserver")
	}
	if !sawParseFile {
		t.Fatal("main should keep parser.ParseFile")
	}
	if !sawExit {
		t.Fatal("main should keep os.Exit failure path")
	}
	if exitCodeValue != "1" {
		t.Fatalf("os.Exit arg = %q, want %q", exitCodeValue, "1")
	}
}

func prepareCheckP1TurnWrappersSandbox(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
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
