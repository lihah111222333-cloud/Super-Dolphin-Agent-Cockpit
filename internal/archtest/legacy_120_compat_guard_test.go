package archtest_test

import (
	"go/ast"
	"go/constant"
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestLegacy120CompatibilityProductionFieldGuard(t *testing.T) {
	loaded := loadLegacy120CompatibilityPackage(t)
	requireLegacy120StringConstants(t, loaded.Types.Scope())
	applyMigration := findPackageFunction(t, loaded, "applyMigration")
	prepare := findPackageFunction(t, loaded, "prepareMigrationTransaction")
	reconcile := findPackageFunction(t, loaded, "reconcileLegacy120Markers")
	skipRecorded := findPackageFunction(t, loaded, "skipRecordedCanonicalTarget")

	requireApplyMigrationCompatibilityCalls(t, loaded, applyMigration)
	requirePrepareMigrationCompatibilityCalls(t, loaded, prepare)
	requireValidationCalls(t, loaded, reconcile)
	requireValidationCalls(t, loaded, skipRecorded)
}

func loadLegacy120CompatibilityPackage(t *testing.T) *packages.Package {
	t.Helper()
	config := &packages.Config{Mode: packages.LoadSyntax, Dir: repoRoot(t)}
	loaded, err := packages.Load(config, "./internal/platform/db/sqlite")
	if err != nil {
		t.Fatalf("load sqlite package: %v", err)
	}
	if len(loaded) != 1 || len(loaded[0].Errors) != 0 {
		t.Fatalf("load sqlite package result = %d packages, errors %v", len(loaded), loaded[0].Errors)
	}
	return loaded[0]
}

func requireLegacy120StringConstants(t *testing.T, scope *types.Scope) {
	t.Helper()
	expected := map[string]string{
		"terminalOutcomeOutboxMigration": "120_terminal_outcome_outbox.sql",
		"legacyManagedGeneration120":     "120_mcp_managed_generations.sql",
		"legacyProviderRecovery120":      "120_agent_provider_binding_recovery_owner.sql",
		"canonicalManagedGeneration122":  "122_mcp_managed_generations.sql",
		"canonicalProviderRecovery123":   "123_agent_provider_binding_recovery_owner.sql",
	}
	for name, want := range expected {
		object, ok := scope.Lookup(name).(*types.Const)
		if !ok || object.Val().Kind() != constant.String || constant.StringVal(object.Val()) != want {
			t.Fatalf("legacy 120 migration constant %s = %v, want %q", name, scope.Lookup(name), want)
		}
	}
}

func findPackageFunction(t *testing.T, loaded *packages.Package, name string) *ast.FuncDecl {
	t.Helper()
	var found *ast.FuncDecl
	for _, file := range loaded.Syntax {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Recv != nil || function.Name.Name != name {
				continue
			}
			if found != nil {
				t.Fatalf("sqlite package contains duplicate top-level %s declarations", name)
			}
			found = function
		}
	}
	if found == nil || found.Body == nil {
		t.Fatalf("sqlite package does not define %s", name)
	}
	return found
}

func requireApplyMigrationCompatibilityCalls(
	t *testing.T,
	loaded *packages.Package,
	function *ast.FuncDecl,
) {
	t.Helper()
	prepare := requireSingleObjectCall(t, loaded, function, "prepareMigrationTransaction")
	execute := requireSingleObjectCall(t, loaded, function, "executeAndRecordMigration")
	if prepare.Pos() >= execute.Pos() {
		t.Fatal("prepareMigrationTransaction must execute before executeAndRecordMigration")
	}
	requireCallNamedObjects(t, loaded.TypesInfo, function, prepare, "ctx", "tx", "dir", "name")
}

func requirePrepareMigrationCompatibilityCalls(
	t *testing.T,
	loaded *packages.Package,
	function *ast.FuncDecl,
) {
	t.Helper()
	reconcile := requireSingleObjectCall(t, loaded, function, "reconcileLegacy120Markers")
	skipRecorded := requireSingleObjectCall(t, loaded, function, "skipRecordedCanonicalTarget")
	requireCallNamedObjects(t, loaded.TypesInfo, function, reconcile, "ctx", "tx", "dir")
	requireCallNamedObjects(t, loaded.TypesInfo, function, skipRecorded, "ctx", "tx", "dir", "name")
}

func requireValidationCalls(t *testing.T, loaded *packages.Package, function *ast.FuncDecl) {
	t.Helper()
	requireSingleObjectCall(t, loaded, function, "canonicalTargetMarkerExists")
	requireSingleObjectCall(t, loaded, function, "validateLegacy120State")
}

func requireSingleObjectCall(
	t *testing.T,
	loaded *packages.Package,
	function *ast.FuncDecl,
	targetName string,
) *ast.CallExpr {
	t.Helper()
	target := loaded.Types.Scope().Lookup(targetName)
	if target == nil {
		t.Fatalf("sqlite package does not define %s", targetName)
	}
	var calls []*ast.CallExpr
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if ok && calledObject(call.Fun, loaded.TypesInfo) == target {
			calls = append(calls, call)
		}
		return true
	})
	if len(calls) != 1 {
		t.Fatalf("%s calls %s %d times, want exactly one typed call", function.Name.Name, targetName, len(calls))
	}
	return calls[0]
}

func requireCallNamedObjects(
	t *testing.T,
	info *types.Info,
	function *ast.FuncDecl,
	call *ast.CallExpr,
	names ...string,
) {
	t.Helper()
	if len(call.Args) != len(names) {
		t.Fatalf("%s call arguments = %d, want %d", function.Name.Name, len(call.Args), len(names))
	}
	for index, name := range names {
		object := findFunctionNamedObject(t, function, info, name)
		if !isObjectIdentifier(call.Args[index], info, object) {
			t.Fatalf("%s call argument %d is not object %s by types.Object", function.Name.Name, index, name)
		}
	}
}

func findFunctionNamedObject(
	t *testing.T,
	function *ast.FuncDecl,
	info *types.Info,
	name string,
) types.Object {
	t.Helper()
	var object types.Object
	ast.Inspect(function, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if !ok || identifier.Name != name || info.Defs[identifier] == nil {
			return true
		}
		if object != nil && object != info.Defs[identifier] {
			t.Fatalf("%s defines multiple objects named %s", function.Name.Name, name)
		}
		object = info.Defs[identifier]
		return true
	})
	if object == nil {
		t.Fatalf("%s does not define object %s", function.Name.Name, name)
	}
	return object
}
