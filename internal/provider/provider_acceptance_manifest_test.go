package provider_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"testing"
)

func TestProviderPackagesHaveAcceptanceManifest(t *testing.T) {
	providers := discoverProviderPackages(t)
	if len(providers) == 0 {
		t.Fatal("no provider packages with provider.<name> Module declarations found")
	}

	for _, provider := range providers {
		t.Run(provider, func(t *testing.T) {
			file := parseProviderContractTest(t, provider)
			testFunc, specFunc := findProviderContractEntrypoint(t, file)
			assertProviderAcceptanceCoverage(t, file, testFunc, specFunc)
			assertProviderSnapshotManifest(t, provider)
		})
	}
}

func TestContracttestRunCoversProviderAcceptance(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	file := parseGoFile(t, filepath.Join(wd, "contracttest", "suite.go"), 0)
	fn := findFuncDecl(file, "RunSpecForTest")
	if fn == nil {
		t.Fatal("RunSpecForTest is required")
	}
	if !functionCallsIdent(fn, "ValidateAcceptanceSpec") {
		t.Fatal("RunSpecForTest must call ValidateAcceptanceSpec before provider behavior")
	}
}

func assertProviderAcceptanceCoverage(t *testing.T, file *ast.File, testFunc, specFunc string) {
	t.Helper()
	testDecl := findFuncDecl(file, testFunc)
	if testDecl == nil {
		t.Fatalf("%s is required", testFunc)
	}
	specDecl := findFuncDecl(file, specFunc)
	if specDecl == nil {
		t.Fatalf("%s is required", specFunc)
	}
	if functionCallsSelector(testDecl, "contracttest", "Run") {
		return
	}
	if functionCallsSelector(specDecl, "contracttest", "ValidateAcceptanceSpec") {
		return
	}
	t.Fatalf("%s must cover acceptance through contracttest.Run or %s must call contracttest.ValidateAcceptanceSpec", testFunc, specFunc)
}

func assertProviderSnapshotManifest(t *testing.T, provider string) {
	t.Helper()
	assertProviderSnapshotFiles(t, provider, "event_snapshots")
	assertProviderSnapshotFiles(t, provider, "prompt_snapshots")
}

func assertProviderSnapshotFiles(t *testing.T, provider, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd() error = %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(wd, provider, "testdata", dir, "*.json"))
	if err != nil {
		t.Fatalf("glob provider %s %s: %v", provider, dir, err)
	}
	if len(matches) == 0 {
		t.Fatalf("provider %s missing testdata/%s/*.json acceptance snapshots", provider, dir)
	}
}

func functionCallsIdent(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		ident, ok := call.Fun.(*ast.Ident)
		found = found || (ok && ident.Name == name)
		return !found
	})
	return found
}

func functionCallsSelector(fn *ast.FuncDecl, pkg, name string) bool {
	found := false
	ast.Inspect(fn, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		found = found || selectorCall(call.Fun, pkg, name)
		return !found
	})
	return found
}
