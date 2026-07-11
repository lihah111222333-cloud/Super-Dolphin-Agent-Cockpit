package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

func TestAgentRuntimeMainUsesHeadlessAppRun(t *testing.T) {
	file := parseMainFile(t)
	calls := collectSelectorCalls(file)
	if !calls["app.Run"] {
		t.Fatal("main does not call app.Run")
	}
	if calls["app.RunDesktop"] {
		t.Fatal("main must not call app.RunDesktop")
	}
	for _, want := range []string{
		"runtimeenv.ConfigurePackagedApp",
		"runtimeenv.LoadVideoEnv",
	} {
		if !calls[want] {
			t.Fatalf("main does not call %s", want)
		}
	}
	assertSetenvCall(t, file, "SUPER_DOLPHIN_PROCESS_ROLE", "owner")
	assertSetenvCall(t, file, "SUPER_DOLPHIN_ENTRYPOINT", "agent-runtime")
}

func TestRuntimeEnvRejectsUnsupportedRuntimeProcessRole(t *testing.T) {
	_, err := runtimeenv.ResolveRuntime(runtimeenv.RuntimeResolveInput{
		GOOS:     "linux",
		GOARCH:   "amd64",
		Env:      map[string]string{"SUPER_DOLPHIN_PROCESS_ROLE": "runtime"},
		UserHome: "/home/alice",
	})
	if err == nil {
		t.Fatal("ResolveRuntime() error = nil, want invalid process role failure")
	}
	if !strings.Contains(err.Error(), `invalid process role "runtime"`) {
		t.Fatalf("ResolveRuntime() error = %v, want invalid runtime role", err)
	}
}

func TestRuntimeEnvOwnerEntrypointResolvesDevOwner(t *testing.T) {
	got, err := runtimeenv.ResolveRuntime(runtimeenv.RuntimeResolveInput{
		GOOS:   "linux",
		GOARCH: "amd64",
		Env: map[string]string{
			"SUPER_DOLPHIN_PROCESS_ROLE": "owner",
			"SUPER_DOLPHIN_ENTRYPOINT":   "agent-runtime",
		},
		UserHome: "/home/alice",
	})
	if err != nil {
		t.Fatalf("ResolveRuntime() error = %v, want owner/dev", err)
	}
	if got.ProcessRole != runtimeenv.ProcessRoleOwner {
		t.Fatalf("ResolveRuntime() role = %q, want owner", got.ProcessRole)
	}
	if got.RuntimeMode != runtimeenv.RuntimeModeDev {
		t.Fatalf("ResolveRuntime() mode = %q, want dev", got.RuntimeMode)
	}
	if got.PackagedRuntime != nil {
		t.Fatalf("ResolveRuntime() packaged runtime = %#v, want nil", got.PackagedRuntime)
	}
}

func parseMainFile(t *testing.T) *ast.File {
	t.Helper()
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", src, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	return file
}

func collectSelectorCalls(file *ast.File) map[string]bool {
	calls := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		calls[ident.Name+"."+sel.Sel.Name] = true
		return true
	})
	return calls
}

func assertSetenvCall(t *testing.T, file *ast.File, key string, value string) {
	t.Helper()
	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Setenv" {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok || ident.Name != "os" || len(call.Args) != 2 {
			return true
		}
		found = stringLiteralValue(call.Args[0]) == key && stringLiteralValue(call.Args[1]) == value
		return true
	})
	if !found {
		t.Fatalf("main does not call os.Setenv(%q, %q)", key, value)
	}
}

func stringLiteralValue(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	return strings.Trim(lit.Value, `"`)
}
