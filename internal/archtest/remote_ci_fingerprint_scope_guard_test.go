package archtest

import (
	"bytes"
	"context"
	"go/ast"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRemoteCIFingerprintScopeContract keeps the source-fingerprint boundary
// fail-closed without coupling the guard to a particular cache helper name.
// A selector digest may be narrower than a package digest, but an unresolved
// observation must never silently become a smaller closure.
func TestRemoteCIFingerprintScopeContract(t *testing.T) {
	root := findRepoRoot(t)
	digestsPath := filepath.Join(root, "internal/devtools/remoteci/workload_fingerprint_digests.go")
	testsPath := filepath.Join(root, "internal/devtools/remoteci/workload_fingerprint_go_tests.go")
	runtimePath := filepath.Join(root, "internal/devtools/remoteci/workload_fingerprint_go_runtime_observation.go")
	runtimeTreePath := filepath.Join(root, "internal/devtools/remoteci/workload_fingerprint_go_runtime_tree.go")
	packagesPath := filepath.Join(root, "internal/devtools/remoteci/workload_fingerprint_go_packages.go")
	cachePath := filepath.Join(root, "internal/devtools/remoteci/workload_fingerprint_exact_compile_cache.go")

	digests := readRemoteCIContractGuardFile(t, digestsPath)
	tests := readRemoteCIContractGuardFile(t, testsPath)
	runtime := readRemoteCIContractGuardFile(t, runtimePath)
	runtimeTree := readRemoteCIContractGuardFile(t, runtimeTreePath)
	packages := readRemoteCIContractGuardFile(t, packagesPath)
	cache := readRemoteCIContractGuardFile(t, cachePath)

	assertRemoteFingerprintUnknownTargetFailsClosed(t, digestsPath)
	assertRemoteFingerprintSelectedDynamicFailsClosed(t, digests+"\n"+tests+"\n"+runtime+"\n"+runtimeTree)
	assertRemoteFingerprintRetainsSiblingTestCompileRoot(t, packages+"\n"+cache)
	assertRemoteFingerprintCacheHasNoAuthorityWriter(t, cachePath, cache)
}

// assertRemoteFingerprintUnknownTargetFailsClosed requires a default AST
// branch which returns an error, rather than a zero digest or whole-tree
// success for an unregistered workload kind.
func assertRemoteFingerprintUnknownTargetFailsClosed(t *testing.T, path string) {
	t.Helper()
	file := parseRemoteCIContractGuardFile(t, path)
	fn := remoteFingerprintFunction(file, "workloadInputDigest")
	if fn == nil {
		t.Fatal("workloadInputDigest function is missing")
	}
	defaultFound, errorReturnFound := remoteFingerprintUnknownTargetFacts(fn)
	if !defaultFound || !errorReturnFound {
		t.Fatalf("workloadInputDigest must reject unknown target kinds (default=%t error-return=%t)", defaultFound, errorReturnFound)
	}
}

func remoteFingerprintUnknownTargetFacts(fn *ast.FuncDecl) (defaultFound, errorReturnFound bool) {
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.CaseClause:
			if value.List == nil {
				defaultFound = true
			}
		case *ast.CallExpr:
			selector, ok := value.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel == nil || selector.Sel.Name != "Errorf" {
				return true
			}
			if packageName, ok := selector.X.(*ast.Ident); ok && packageName.Name == "fmt" {
				errorReturnFound = true
			}
		}
		return true
	})
	return defaultFound, errorReturnFound
}

// assertRemoteFingerprintSelectedDynamicFailsClosed keeps the two important
// dynamic branches explicit: unresolved target observations bind the whole
// tree, while a package-wide compile root still scans every sibling test file.
func assertRemoteFingerprintSelectedDynamicFailsClosed(t *testing.T, source string) {
	t.Helper()
	for _, marker := range []string{
		"return remoteGoTestScopeTree",
		"return \"tree\", \"\", remoteGoTestScopeTree",
		"return remoteGoTestObservationKind(method), \"\", remoteGoTestScopeTree",
		"snapshot.addGoProductionRuntimeTreeEntries(",
		"snapshot.digestEntries(",
		"snapshot.entries",
	} {
		if !strings.Contains(source, marker) {
			t.Fatalf("fingerprint dynamic-observation fail-closed marker %q is missing", marker)
		}
	}
}

// assertRemoteFingerprintRetainsSiblingTestCompileRoot rejects a future
// selector-only compile shortcut.  The exact test digest must retain all
// applicable same-directory _test.go inputs even when only one test runs.
func assertRemoteFingerprintRetainsSiblingTestCompileRoot(t *testing.T, source string) {
	t.Helper()
	for _, marker := range []string{
		"remoteGoTestDeclarations(",
		"for _, file := range files",
		"_test.go",
	} {
		if !strings.Contains(source, marker) {
			t.Fatalf("exact compile root is missing sibling-test marker %q", marker)
		}
	}
}

// assertRemoteFingerprintCacheHasNoAuthorityWriter ensures the snapshot-local
// compile-root cache remains a pure in-memory acceleration path.  Authority,
// artifact promotion, and cross-shard CAS belong to the existing ECI worker
// and SQLite finalizer; they must not be introduced in fingerprint code.
func assertRemoteFingerprintCacheHasNoAuthorityWriter(t *testing.T, path, source string) {
	t.Helper()
	file := parseRemoteCIContractGuardFile(t, path)
	for _, importSpec := range file.Imports {
		importPath := strings.Trim(importSpec.Path.Value, "\"")
		if strings.Contains(importPath, "database/sql") || strings.Contains(importPath, "sqlite") || strings.Contains(importPath, "alicloud/oss") {
			t.Fatalf("fingerprint cache imports authority/transport package %q", importPath)
		}
	}
	for _, forbidden := range []string{
		"CREATE TABLE", "INSERT INTO", "UPDATE ci_", "CompareAndSwap", "CreateImageCache",
		"PutObject", "WriteFile", "cross-shard", "cross_shard", "artifact_cas",
	} {
		if strings.Contains(strings.ToLower(source), strings.ToLower(forbidden)) {
			t.Fatalf("fingerprint cache contains forbidden authority/CAS marker %q", forbidden)
		}
	}
}

func remoteFingerprintFunction(file *ast.File, name string) *ast.FuncDecl {
	var found *ast.FuncDecl
	for _, declaration := range file.Decls {
		fn, ok := declaration.(*ast.FuncDecl)
		if ok && fn.Name != nil && fn.Name.Name == name {
			found = fn
			break
		}
	}
	return found
}

// TestRemoteCIFingerprintDynamicCounterexamples executes the existing
// remoteci fixtures as a dynamic architecture guard.  The fixtures mutate
// unrelated tree files, unresolved reflection/process paths, and unselected
// sibling tests; a green package test is required for the guard to pass.
func TestRemoteCIFingerprintDynamicCounterexamples(t *testing.T) {
	root := findRepoRoot(t)
	pattern := `Test(ExactGoTestDigest(IncludesUnselectedPackageTestCompileInputs|IgnoresUnselectedDynamicRuntimeObservation|FailsClosedForDynamicReflection|FailsClosedForPackageReflectAlias|FailsClosedForDynamicRepositoryObservations|FailsClosedForProcessAndCWDObservations|TargetDynamicObservationIncludesProjectMap|BindsImportedProductionPackageAssets|KeepsStaticObservationPrecise|ClassifiesReadDirAsTreeObservation)|GoPackageDigest(ScopesDynamicObservationsToPackageClosure|BindsTestOnlyAndLocalDependencyInputs|RetainsStaticObservationAfterDynamic)|GoProductionRuntimeObservation.*|ExactCompileRoot.*|MigrateRemoteWorkloadPassCandidatesChangedInputMisses|RemoteWorkloadMissesRejectReuseOverlapBeforeRemoteSideEffects|ClassifyRemoteWorkloadPassesMixed(SamePackageIsAtomicMiss|DifferentPackagesKeepsPartialReuse))`
	runRemoteCIFingerprintFixtures(t, root, pattern)
}

// TestRemoteCIMixedReuseIsFailClosed dynamically exercises partial reuse and
// overlap rejection.  Any malformed/mixed identity must remain MISS/error and
// must not become a passed shortcut.
func TestRemoteCIMixedReuseIsFailClosed(t *testing.T) {
	root := findRepoRoot(t)
	pattern := `Test(RemoteWorkloadMissesRejectReuseOverlapBeforeRemoteSideEffects|MigrateRemoteWorkloadPassCandidates(FailedEvidenceMisses|ChangedInputMisses)|ClassifyRemoteWorkloadPassesMixed(SamePackageIsAtomicMiss|DifferentPackagesKeepsPartialReuse))`
	runRemoteCIFingerprintFixtures(t, root, pattern)
}

func runRemoteCIFingerprintFixtures(t *testing.T, root, pattern string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "./internal/devtools/remoteci", "-run", pattern, "-count=1")
	command.Dir = root
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("remote CI fingerprint fixture command timed out: %v", ctx.Err())
	}
	if err != nil {
		t.Fatalf("remote CI fingerprint fixture command failed: %v\n%s", err, bytes.TrimSpace(output))
	}
}
