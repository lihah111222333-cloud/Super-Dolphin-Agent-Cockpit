package remoteci

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"golang.org/x/sync/errgroup"
)

func TestExactCompileRootCacheIsScopedToPackageAndRace(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	ctx := context.Background()

	normalTest, err := snapshot.goExactTestInputDigest(ctx, gate.GoTestTarget{Package: "fixture", Name: "TestX"}, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("normal TestX digest: %v", err)
	}
	normalBenchmark, err := snapshot.goExactTestInputDigest(ctx, gate.GoTestTarget{Package: "fixture", Name: "BenchmarkX"}, remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("normal BenchmarkX digest: %v", err)
	}
	if normalTest == normalBenchmark {
		t.Fatal("selector runtime closures collapsed into one digest")
	}
	assertExactCompileRootCacheState(t, snapshot, 1)

	if _, err := snapshot.goExactTestInputDigest(ctx, gate.GoTestTarget{Package: "fixture", Name: "TestX"}, remoteGoBuildProfile{race: true}); err != nil {
		t.Fatalf("race TestX digest: %v", err)
	}
	assertExactCompileRootCacheState(t, snapshot, 2)
}

func assertExactCompileRootCacheState(t *testing.T, snapshot *remoteGitTreeSnapshot, want uint64) {
	t.Helper()
	if got := snapshot.exactCompileRootComputations; got != want {
		t.Fatalf("exact compile-root computations = %d, want %d", got, want)
	}
	if got := uint64(len(snapshot.exactCompileRootCache)); got != want {
		t.Fatalf("exact compile-root cache entries = %d, want %d", got, want)
	}
	for key, entry := range snapshot.exactCompileRootCache {
		if entry.err != nil {
			t.Fatalf("compile-root cache %v: %v", key, entry.err)
		}
		if entry.merkleRoot == "" {
			t.Fatalf("compile-root cache %v has empty Merkle root", key)
		}
	}
}

func TestExactCompileRootCacheSerializesConcurrentSelectors(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	ctx := context.Background()
	targets := []gate.GoTestTarget{
		{Package: "fixture", Name: "TestX"},
		{Package: "fixture", Name: "BenchmarkX"},
	}
	if err := runConcurrentExactSelectors(ctx, snapshot, targets); err != nil {
		t.Fatalf("concurrent exact selector digest: %v", err)
	}
	assertExactCompileRootCacheState(t, snapshot, 1)
}

func runConcurrentExactSelectors(ctx context.Context, snapshot *remoteGitTreeSnapshot, targets []gate.GoTestTarget) error {
	start := make(chan struct{})
	var group errgroup.Group
	for range 4 {
		for _, target := range targets {
			target := target
			group.Go(func() error {
				<-start
				_, err := snapshot.goExactTestInputDigest(ctx, target, remoteGoBuildProfile{})
				return err
			})
		}
	}
	close(start)
	return group.Wait()
}

func TestExactCompileRootMerkleIsDeterministic(t *testing.T) {
	entries := []remoteGitTreeEntry{
		{mode: "100644", kind: "blob", objectID: "a", path: "fixture/a.go"},
		{mode: "100644", kind: "blob", objectID: "b", path: "fixture/b_test.go"},
	}
	first := remoteExactCompileRootMerkle(entries)
	second := remoteExactCompileRootMerkle([]remoteGitTreeEntry{entries[1], entries[0]})
	if first == "" || first != second {
		t.Fatalf("Merkle root is not deterministic: first=%q second=%q", first, second)
	}
	entries[1].objectID = "changed"
	if changed := remoteExactCompileRootMerkle(entries); changed == first {
		t.Fatal("Merkle root ignored compile entry identity")
	}
}

func TestExactCompileRootEntriesAreCloned(t *testing.T) {
	snapshot := testExactGoTestDigestSnapshot("")
	first, err := snapshot.exactCompileRootEntries("fixture", remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("first exact compile-root entries: %v", err)
	}
	if len(first) == 0 {
		t.Fatal("exact compile-root entries are empty")
	}
	originalPath := first[0].path
	first[0].path = "mutated"
	second, err := snapshot.exactCompileRootEntries("fixture", remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("second exact compile-root entries: %v", err)
	}
	if second[0].path != originalPath {
		t.Fatalf("cached compile-root entry was mutated through returned slice: got %q want %q", second[0].path, originalPath)
	}
}

func TestExactCompileRootExcludesSelectorRuntimeObservation(t *testing.T) {
	targetSource := `package fixture

import (
	"reflect"
	"testing"
)

func TestX(t *testing.T) { _ = reflect.DeepEqual(1, 1) }
`
	baseline := testExactGoTestDigestSnapshotWithObservedFiles(targetSource, "same", "same")
	changed := testExactGoTestDigestSnapshotWithObservedFiles(targetSource, "same", "same")
	testExactGoTestDigestReplaceFile(changed, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md", []byte("runtime-only-change"))
	baselineRoot, err := baseline.exactCompileRootEntries("fixture", remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("baseline exact compile root: %v", err)
	}
	changedRoot, err := changed.exactCompileRootEntries("fixture", remoteGoBuildProfile{})
	if err != nil {
		t.Fatalf("changed exact compile root: %v", err)
	}
	if got, want := remoteExactCompileRootMerkle(changedRoot), remoteExactCompileRootMerkle(baselineRoot); got != want {
		t.Fatalf("runtime observation changed compile root: got %q want %q", got, want)
	}
	baselineDigest := testExactGoTestDigest(t, baseline, gate.GoTestTarget{Package: "fixture", Name: "TestX"})
	changedDigest := testExactGoTestDigest(t, changed, gate.GoTestTarget{Package: "fixture", Name: "TestX"})
	if baselineDigest != changedDigest {
		t.Fatal("pure selector runtime observation included unrelated project-map input")
	}
}
