package provider_test

import (
	"go/ast"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderAcceptanceManifest(t *testing.T) {
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
			assertProviderContractSnapshotFiles(t, provider, "event_snapshots")
			assertProviderContractSnapshotFiles(t, provider, "prompt_snapshots")
			assertProviderContractEventCapture(t, file)
			assertProviderContractEventTranslatorLocal(t, provider, file)
			assertProviderAcceptanceCovered(t, file)
		})
	}
}

func assertProviderContractSnapshotFiles(t *testing.T, provider, snapshotDir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(provider, "testdata", snapshotDir, "*.json"))
	if err != nil {
		t.Fatalf("glob provider %s %s snapshots: %v", provider, snapshotDir, err)
	}
	if len(matches) == 0 {
		t.Fatalf("%s must include at least one testdata/%s/*.json golden snapshot", provider, snapshotDir)
	}
}

func assertProviderContractEventTranslatorLocal(t *testing.T, provider string, file *ast.File) {
	t.Helper()
	localFuncs := providerLocalFunctionNames(t, provider)
	var translators []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !selectorCall(call.Fun, "contracttest", "CaptureProviderEventTranslation") {
			return true
		}
		if len(call.Args) < 4 {
			t.Fatalf("CaptureProviderEventTranslation has %d args, want provider-local translator arg", len(call.Args))
		}
		translator, ok := call.Args[3].(*ast.Ident)
		if !ok || strings.TrimSpace(translator.Name) == "" || translator.Name == "nil" {
			t.Fatalf("CaptureProviderEventTranslation translator = %T, want provider-local function identifier", call.Args[3])
		}
		translators = append(translators, translator.Name)
		return true
	})
	if len(translators) == 0 {
		t.Fatal("provider contract test must capture provider event translation")
	}
	for _, translator := range translators {
		if !localFuncs[translator] {
			t.Fatalf("CaptureProviderEventTranslation translator %s is not declared in provider package %s", translator, provider)
		}
	}
}

func providerLocalFunctionNames(t *testing.T, provider string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(provider)
	if err != nil {
		t.Fatalf("read provider package %s: %v", provider, err)
	}
	funcs := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := filepath.Join(provider, entry.Name())
		file := parseGoFile(t, path, 0)
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Recv == nil {
				funcs[fn.Name.Name] = true
			}
		}
	}
	return funcs
}

func assertProviderAcceptanceCovered(t *testing.T, file *ast.File) {
	t.Helper()
	covered := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if selectorCall(call.Fun, "contracttest", "Run") || selectorCall(call.Fun, "contracttest", "ValidateAcceptanceSpec") {
			covered = true
			return false
		}
		return true
	})
	if !covered {
		t.Fatal("provider contract test must call contracttest.Run or contracttest.ValidateAcceptanceSpec to cover acceptance criteria")
	}
}
