package provider_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderPackagesHaveContractTests(t *testing.T) {
	cases := []struct {
		provider string
		testFunc string
		specFunc string
	}{
		{provider: "unified", testFunc: "TestUnifiedProviderContract", specFunc: "CompleteUnifiedContractSpec"},
		{provider: "claudecli", testFunc: "TestClaudeCLIProviderContract", specFunc: "CompleteClaudeCLIContractSpec"},
		{provider: "codexapp", testFunc: "TestCodexAppProviderContract", specFunc: "CompleteCodexAppContractSpec"},
	}

	for _, tc := range cases {
		t.Run(tc.provider, func(t *testing.T) {
			file := parseProviderContractTest(t, tc.provider)
			assertProviderContractEntrypoint(t, file, tc.testFunc, tc.specFunc)
			assertProviderContractRequiredCases(t, file, tc.specFunc)
			assertProviderContractSnapshots(t, file)
			assertProviderContractEventCapture(t, file)
			assertProviderContractNoForbiddenShortcuts(t, file)
		})
	}
}

func parseProviderContractTest(t *testing.T, provider string) *ast.File {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	path := filepath.Join(wd, provider, "provider_contract_test.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func assertProviderContractEntrypoint(t *testing.T, file *ast.File, testFunc, specFunc string) {
	t.Helper()
	fn := findFuncDecl(file, testFunc)
	if fn == nil {
		t.Fatalf("%s is required", testFunc)
	}
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !selectorCall(call.Fun, "contracttest", "Run") || len(call.Args) < 2 {
			return true
		}
		if specCall, ok := call.Args[1].(*ast.CallExpr); ok && identCall(specCall.Fun, specFunc) {
			found = true
		}
		return true
	})
	if !found {
		t.Fatalf("%s must call contracttest.Run(t, %s())", testFunc, specFunc)
	}
}

func assertProviderContractRequiredCases(t *testing.T, file *ast.File, specFunc string) {
	t.Helper()
	fn := findFuncDecl(file, specFunc)
	if fn == nil {
		t.Fatalf("%s is required", specFunc)
	}
	required := map[string]bool{
		"CasePromptParity":  false,
		"CaseApproval":      false,
		"CaseInterrupt":     false,
		"CaseForceComplete": false,
		"CaseResume":        false,
		"CaseToolbridge":    false,
		"CaseRuntimeReport": false,
	}
	ast.Inspect(fn, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !identName(sel.X, "contracttest") {
			return true
		}
		if _, ok := required[sel.Sel.Name]; ok {
			required[sel.Sel.Name] = true
		}
		return true
	})
	for key, found := range required {
		if !found {
			t.Fatalf("%s missing contracttest.%s", specFunc, key)
		}
	}
}

func assertProviderContractSnapshots(t *testing.T, file *ast.File) {
	t.Helper()
	var eventSnapshot, promptSnapshot bool
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selectorCall(call.Fun, "contracttest", "LoadExpectedEventSnapshot") {
			eventSnapshot = true
		}
		if selectorCall(call.Fun, "contracttest", "LoadExpectedPromptSnapshot") {
			promptSnapshot = true
		}
		return true
	})
	if !eventSnapshot {
		t.Fatal("provider contract test must load event golden snapshots")
	}
	if !promptSnapshot {
		t.Fatal("provider contract test must load prompt golden snapshots")
	}
}

func assertProviderContractEventCapture(t *testing.T, file *ast.File) {
	t.Helper()
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !selectorCall(call.Fun, "contracttest", "CaptureProviderEventTranslation") {
			return true
		}
		if len(call.Args) < 4 {
			t.Fatalf("CaptureProviderEventTranslation has %d args, want translator arg", len(call.Args))
		}
		switch arg := call.Args[3].(type) {
		case *ast.Ident:
			if strings.TrimSpace(arg.Name) == "" || arg.Name == "nil" {
				t.Fatal("event capture translator must be a package-local function")
			}
		case *ast.SelectorExpr:
			if strings.TrimSpace(arg.Sel.Name) == "" {
				t.Fatal("event capture translator selector is empty")
			}
		default:
			t.Fatalf("event capture translator = %T, want function identifier", arg)
		}
		found = true
		return true
	})
	if !found {
		t.Fatal("provider contract test must capture provider event translation")
	}
}

func assertProviderContractNoForbiddenShortcuts(t *testing.T, file *ast.File) {
	t.Helper()
	assertNoDotContracttestImport(t, file)
	ast.Inspect(file, func(n ast.Node) bool {
		assertNoForbiddenIdent(t, n)
		assertNoForbiddenSelector(t, n)
		assertNoCaseEvidenceComposite(t, n)
		return true
	})
}

func assertNoDotContracttestImport(t *testing.T, file *ast.File) {
	t.Helper()
	for _, imp := range file.Imports {
		if imp.Name != nil && imp.Name.Name == "." && strings.Contains(imp.Path.Value, "internal/provider/contracttest") {
			t.Fatal("provider contract tests must not dot-import contracttest")
		}
	}
}

func assertNoForbiddenIdent(t *testing.T, n ast.Node) {
	t.Helper()
	ident, ok := n.(*ast.Ident)
	if !ok {
		return
	}
	switch ident.Name {
	case "CompleteFixtureSpec", "NewDependencyModeError", "DependencyModeError":
		t.Fatalf("provider contract test uses forbidden shortcut %s", ident.Name)
	}
}

func assertNoForbiddenSelector(t *testing.T, n ast.Node) {
	t.Helper()
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return
	}
	if strings.HasPrefix(sel.Sel.Name, "Skip") {
		t.Fatalf("provider contract test uses %s", sel.Sel.Name)
	}
	if sel.Sel.Name == "NewDependencyModeError" || sel.Sel.Name == "DependencyModeError" {
		t.Fatalf("provider contract test uses forbidden shortcut %s", sel.Sel.Name)
	}
}

func assertNoCaseEvidenceComposite(t *testing.T, n ast.Node) {
	t.Helper()
	lit, ok := n.(*ast.CompositeLit)
	if !ok {
		return
	}
	if selectorType(lit.Type, "contracttest", "CaseEvidence") || identType(lit.Type, "CaseEvidence") {
		t.Fatal("provider contract test must not construct CaseEvidence directly")
	}
}

func findFuncDecl(file *ast.File, name string) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn
		}
	}
	return nil
}

func selectorCall(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && identName(sel.X, pkg) && sel.Sel.Name == name
}

func identCall(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func identName(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

func selectorType(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	return ok && identName(sel.X, pkg) && sel.Sel.Name == name
}

func identType(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}
