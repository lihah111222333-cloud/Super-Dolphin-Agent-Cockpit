package remoteci

import (
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestRemoteGoProductionIndexCacheIsScopedToProfile(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	normal, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("normal production index: %v", err)
	}
	if _, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{}); err != nil {
		t.Fatalf("repeated normal production index: %v", err)
	}
	race, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{race: true})
	if err != nil {
		t.Fatalf("race production index: %v", err)
	}
	if got := snapshot.productionIndexComputations; got != 2 {
		t.Fatalf("production index computations = %d, want 2", got)
	}
	if len(normal.byPackage) != len(race.byPackage) {
		t.Fatalf("normal/race production index package count differs: %d vs %d", len(normal.byPackage), len(race.byPackage))
	}
}

func TestRemoteGoProductionIndexCacheSerializesConcurrentBuild(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	start := make(chan struct{})
	var group errgroup.Group
	for range 8 {
		group.Go(func() error {
			<-start
			_, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
			return err
		})
	}
	close(start)
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent production index: %v", err)
	}
	if got := snapshot.productionIndexComputations; got != 1 {
		t.Fatalf("concurrent production index computations = %d, want 1", got)
	}
}

func TestRemoteGoProductionIndexCacheStoresErrors(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	snapshot.goSources["fixture/main.go"] = []byte("package fixture\nfunc broken(\n")
	first, firstErr := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	second, secondErr := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	if firstErr == nil || secondErr == nil {
		t.Fatal("invalid production source was accepted")
	}
	if firstErr.Error() != secondErr.Error() {
		t.Fatalf("cached production index error changed: first=%v second=%v", firstErr, secondErr)
	}
	if len(first.byPackage) != 0 || len(second.byPackage) != 0 {
		t.Fatal("failed production index returned declarations")
	}
	if got := snapshot.productionIndexComputations; got != 1 {
		t.Fatalf("error production index computations = %d, want 1", got)
	}
}

func TestRemoteGoProductionIndexCacheReusesImmutableIndexWithoutAllocations(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	_, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("warm production index: %v", err)
	}
	var runErr error
	allocations := testing.AllocsPerRun(20, func() {
		_, runErr = snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	})
	if runErr != nil {
		t.Fatalf("cached production index: %v", runErr)
	}
	if allocations > 2 {
		t.Fatalf("cached immutable production index allocations = %.1f, want <= 2", allocations)
	}
}

func TestRemoteGoProductionImportsCacheIsProfileScopedAndReturnsCopies(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nimport _ \"example.com/fixture/support\"\n"))
	testExactGoTestDigestReplaceFile(snapshot, "support/support.go", []byte("package support\n"))
	index, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("production index: %v", err)
	}
	first, err := snapshot.remoteGoProductionImportedDirectories("fixture", index, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("first production imports: %v", err)
	}
	first[0] = "mutated"
	second, err := snapshot.remoteGoProductionImportedDirectories("fixture", index, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("second production imports: %v", err)
	}
	if len(second) != 1 || second[0] != "support" {
		t.Fatalf("cached production imports = %v, want [support]", second)
	}
	if got := snapshot.productionImportsComputations; got != 1 {
		t.Fatalf("production imports computations = %d, want 1", got)
	}
}

func TestRemoteGoProductionRuntimeCacheReplaysObservedEntries(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nimport \"os\"\nfunc run() { _, _ = os.ReadFile(\"testdata/input.txt\") }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { run() }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/input.txt", []byte("input"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	first := make(map[string]remoteGitTreeEntry)
	second := make(map[string]remoteGitTreeEntry)
	if _, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, first, remoteGoBuildProfile{}); err != nil {
		t.Fatalf("first production runtime: %v", err)
	}
	if _, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, second, remoteGoBuildProfile{}); err != nil {
		t.Fatalf("second production runtime: %v", err)
	}
	if _, ok := first["fixture/testdata/input.txt"]; !ok {
		t.Fatal("first runtime observation omitted input")
	}
	if _, ok := second["fixture/testdata/input.txt"]; !ok {
		t.Fatal("cached runtime observation omitted input")
	}
	if got := snapshot.productionRuntimeComputations; got != 1 {
		t.Fatalf("production runtime computations = %d, want 1", got)
	}
}

func TestGoProductionRuntimeObservationRecursesGenericImportedLocalCall(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "support/support.go", []byte("package support\nimport \"os\"\nfunc Read[T any]() { _, _ = os.ReadFile(\"testdata/input.txt\") }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/testdata/input.txt", []byte("input"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nimport \"example.com/fixture/support\"\nfunc run() { support.Read[string]() }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { run() }\n"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	selected := make(map[string]remoteGitTreeEntry)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, selected, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
	if _, ok := selected["fixture/testdata/input.txt"]; !ok {
		t.Fatal("generic imported helper observation omitted target asset")
	}
}

func TestGoProductionRuntimeObservationAcceptsImportedLocalTypeConversion(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	testExactGoTestDigestReplaceFile(snapshot, "support/support.go", []byte("package support\ntype ID string\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/main.go", []byte("package fixture\nimport \"example.com/fixture/support\"\nfunc run() { _ = support.ID(\"value\") }\n"))
	testExactGoTestDigestReplaceFile(snapshot, "fixture/target_test.go", []byte("package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) { run() }\n"))
	root := runtimeObservationTestDeclaration(t, snapshot)
	scope, err := snapshot.addGoProductionRuntimeObservedEntries("fixture", root, make(map[string]remoteGitTreeEntry), remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("addGoProductionRuntimeObservedEntries(): %v", err)
	}
	if scope != remoteGoTestScopeSelector {
		t.Fatalf("runtime observation scope = %v, want selector", scope)
	}
}

func BenchmarkRemoteGoProductionIndexCache(b *testing.B) {
	snapshot := &remoteGitTreeSnapshot{goSources: map[string][]byte{
		"fixture/main.go":    []byte("package fixture\nfunc ReadFixture() {}\n"),
		"support/support.go": []byte("package support\nfunc ReadFixture() {}\n"),
	}}
	if _, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{}); err != nil {
		b.Fatalf("warm production index: %v", err)
	}
	b.ResetTimer()
	for b.Loop() {
		if _, err := snapshot.remoteGoProductionIndex(remoteGoBuildProfile{}); err != nil {
			b.Fatalf("cached production index: %v", err)
		}
	}
	b.StopTimer()
	if got := snapshot.productionIndexComputations; got != 1 {
		b.Fatalf("benchmark production index computations = %d, want 1", got)
	}
}
