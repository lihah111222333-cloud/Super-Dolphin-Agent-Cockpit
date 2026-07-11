package provider_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/contracttest"
)

func TestProviderPackagesHaveContractTests(t *testing.T) {
	providers := discoverProviderPackages(t)
	if len(providers) == 0 {
		t.Fatal("no provider packages with provider.<name> Module declarations found")
	}

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			file := parseProviderContractTest(t, provider)
			_, specFunc := findProviderContractEntrypoint(t, file)
			assertProviderContractRequiredCases(t, file, specFunc)
			assertProviderContractSnapshots(t, file)
			assertProviderContractEventCapture(t, file)
			assertProviderContractNoForbiddenShortcuts(t, file)
		})
	}
}

func discoverProviderPackages(t *testing.T) []string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	entries, err := os.ReadDir(wd)
	if err != nil {
		t.Fatalf("read provider dir %s: %v", wd, err)
	}

	var providers []string
	for _, entry := range entries {
		provider, ok := providerPackageCandidate(t, wd, entry)
		if ok {
			providers = append(providers, provider)
		}
	}
	sort.Strings(providers)
	return providers
}

func providerPackageCandidate(t *testing.T, wd string, entry os.DirEntry) (string, bool) {
	t.Helper()
	provider := entry.Name()
	if !entry.IsDir() || isNonProviderPackage(provider) {
		return "", false
	}
	modulePath := filepath.Join(wd, provider, "module.go")
	if _, err := os.Stat(modulePath); os.IsNotExist(err) {
		return "", false
	} else if err != nil {
		t.Fatalf("stat %s: %v", modulePath, err)
	}
	if !declaresProviderModule(t, modulePath, provider) {
		t.Fatalf("%s has module.go but no fx.Module(%q); exclude it explicitly if it is not a provider package", provider, "provider."+provider)
	}
	return provider, true
}

func isNonProviderPackage(name string) bool {
	switch name {
	case "_template", "contracttest", "dreamexec", "e2e", "e2efixture", "manifestbuilder", "shared", "toolfilter":
		return true
	default:
		return false
	}
}

func declaresProviderModule(t *testing.T, modulePath, provider string) bool {
	t.Helper()
	file := parseGoFile(t, modulePath, 0)
	want := "provider." + provider
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, ok := fxModuleName(t, modulePath, call)
		if !ok {
			return true
		}
		found = name == want
		return !found
	})
	return found
}

func fxModuleName(t *testing.T, modulePath string, call *ast.CallExpr) (string, bool) {
	t.Helper()
	if !selectorCall(call.Fun, "fx", "Module") || len(call.Args) == 0 {
		return "", false
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	name, err := strconv.Unquote(lit.Value)
	if err != nil {
		t.Fatalf("unquote fx.Module name in %s: %v", modulePath, err)
	}
	return name, true
}

func parseProviderContractTest(t *testing.T, provider string) *ast.File {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	path := filepath.Join(wd, provider, "provider_contract_test.go")
	return parseGoFile(t, path, parser.ParseComments)
}

func parseGoFile(t *testing.T, path string, mode parser.Mode) *ast.File {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, mode)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return file
}

func findProviderContractEntrypoint(t *testing.T, file *ast.File) (string, string) {
	t.Helper()
	var testFunc, specFunc string
	for _, decl := range file.Decls {
		fn := testFuncDecl(decl)
		if fn == nil {
			continue
		}
		spec, ok := providerContractSpecCall(t, fn)
		if !ok {
			continue
		}
		if testFunc != "" {
			t.Fatalf("multiple provider contract entrypoints found: %s and %s", testFunc, fn.Name.Name)
		}
		testFunc = fn.Name.Name
		specFunc = spec
	}
	if testFunc == "" || specFunc == "" {
		t.Fatal("provider contract test must define Test*ProviderContract calling contracttest.Run(t, Complete*ContractSpec())")
	}
	return testFunc, specFunc
}

func testFuncDecl(decl ast.Decl) *ast.FuncDecl {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || !strings.HasPrefix(fn.Name.Name, "Test") {
		return nil
	}
	return fn
}

func providerContractSpecCall(t *testing.T, fn *ast.FuncDecl) (string, bool) {
	t.Helper()
	var specFunc string
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !selectorCall(call.Fun, "contracttest", "Run") || len(call.Args) < 2 {
			return true
		}
		specFunc = contractSpecFuncName(t, fn.Name.Name, call.Args[1])
		return false
	})
	return specFunc, specFunc != ""
}

func contractSpecFuncName(t *testing.T, testFunc string, expr ast.Expr) string {
	t.Helper()
	specCall, ok := expr.(*ast.CallExpr)
	if !ok {
		t.Fatalf("%s must call contracttest.Run(t, Complete*ContractSpec())", testFunc)
	}
	specIdent, ok := specCall.Fun.(*ast.Ident)
	if !ok || !strings.HasPrefix(specIdent.Name, "Complete") || !strings.HasSuffix(specIdent.Name, "ContractSpec") {
		t.Fatalf("%s contract spec call = %T, want Complete*ContractSpec()", testFunc, specCall.Fun)
	}
	return specIdent.Name
}

func assertProviderContractRequiredCases(t *testing.T, file *ast.File, specFunc string) {
	t.Helper()
	fn := findFuncDecl(file, specFunc)
	if fn == nil {
		t.Fatalf("%s is required", specFunc)
	}
	required, promptAlternatives := providerAcceptanceRequiredSelectors(t)
	ast.Inspect(fn, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !identName(sel.X, "contracttest") {
			return true
		}
		if _, ok := required[sel.Sel.Name]; ok {
			required[sel.Sel.Name] = true
		}
		if _, ok := promptAlternatives[sel.Sel.Name]; ok {
			promptAlternatives[sel.Sel.Name] = true
		}
		return true
	})
	if !anyProviderContractCaseFound(promptAlternatives) {
		t.Fatalf("%s missing prompt contract case: want contracttest.CasePromptParity or contracttest.CasePromptMaterializedCarrier", specFunc)
	}
	for key, found := range required {
		if !found {
			t.Fatalf("%s missing contracttest.%s", specFunc, key)
		}
	}
}

func providerAcceptanceRequiredSelectors(t *testing.T) (map[string]bool, map[string]bool) {
	t.Helper()
	required := map[string]bool{}
	promptAlternatives := map[string]bool{}
	for _, prompt := range []contracttest.AcceptanceCriterion{
		contracttest.AcceptancePromptSnapshotParity,
		contracttest.AcceptancePromptMaterializedCarrier,
	} {
		spec := contracttest.Spec{
			RequiredCases: map[contracttest.CaseKey]contracttest.Case{
				contracttest.CaseKey(prompt): {Name: string(prompt), Run: func(*testing.T, *contracttest.CaseEvidence) {}},
			},
		}
		for _, criterion := range contracttest.RequiredAcceptanceCriteria(spec) {
			selector := acceptanceCriterionSelector(t, criterion)
			switch criterion {
			case contracttest.AcceptancePromptSnapshotParity, contracttest.AcceptancePromptMaterializedCarrier:
				promptAlternatives[selector] = false
			default:
				required[selector] = false
			}
		}
	}
	return required, promptAlternatives
}

func acceptanceCriterionSelector(t *testing.T, criterion contracttest.AcceptanceCriterion) string {
	t.Helper()
	name := strings.TrimSpace(string(criterion))
	if name == "" {
		t.Fatalf("unknown acceptance criterion %q", criterion)
	}
	parts := strings.Split(name, "_")
	var builder strings.Builder
	builder.WriteString("Case")
	for _, part := range parts {
		if part == "" {
			t.Fatalf("unknown acceptance criterion %q", criterion)
		}
		builder.WriteString(strings.ToUpper(part[:1]))
		builder.WriteString(part[1:])
	}
	return builder.String()
}

func anyProviderContractCaseFound(cases map[string]bool) bool {
	for _, found := range cases {
		if found {
			return true
		}
	}
	return false
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
	case "CompleteFixtureSpec", "DependencyModeError":
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
	if sel.Sel.Name == "NewDependencyModeError" {
		t.Fatalf("provider contract test uses forbidden shortcut %s", sel.Sel.Name)
	}
	if sel.Sel.Name == "DependencyModeError" {
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
