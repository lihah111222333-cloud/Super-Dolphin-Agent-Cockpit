package remoteci

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteReplayInputAlgorithmCompatibilityAndTreeRank(t *testing.T) {
	target := testRemoteReplayAlgorithmSnapshot("target", "same", "target-unrelated")
	same := testRemoteReplayAlgorithmSnapshot("same", "same", "source-unrelated")
	changed := testRemoteReplayAlgorithmSnapshot("changed", "changed", "source-unrelated")
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatal(err)
	}
	compatible, err := cache.inputAlgorithmsCompatible(same, target)
	if err != nil || !compatible {
		t.Fatalf("same input algorithm compatible=%t err=%v", compatible, err)
	}
	compatible, err = cache.inputAlgorithmsCompatible(changed, target)
	if err != nil || compatible {
		t.Fatalf("changed input algorithm compatible=%t err=%v", compatible, err)
	}
	if remoteReplayTreeDistance(same, target) >= remoteReplayTreeDistance(changed, target) {
		t.Fatal("tree distance did not prefer the closer immutable source")
	}
	candidates := []gate.WorkloadPassEvidence{
		{OriginSourceTreeSHA: "changed"}, {OriginSourceTreeSHA: "same"},
	}
	ranked := rankedRemoteWorkloadPassSourceCandidates(candidates, map[string]int{"changed": 2, "same": 1})
	if ranked[0].OriginSourceTreeSHA != "same" {
		t.Fatalf("ranked source tree = %q, want same", ranked[0].OriginSourceTreeSHA)
	}
}

func TestRemoteReplayInputAlgorithmRejectsEveryProducerChange(t *testing.T) {
	target := testRemoteReplayAlgorithmSnapshot("target", "same", "same-unrelated")
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatal(err)
	}
	for _, filePath := range remoteWorkloadInputAlgorithmRequiredPaths() {
		t.Run(filePath, func(t *testing.T) {
			source := testRemoteReplayAlgorithmSnapshot("source:"+filePath, "same", "same-unrelated")
			entry := source.byPath[filePath]
			entry.objectID = "changed"
			source.byPath[filePath] = entry
			for index := range source.entries {
				if source.entries[index].path == filePath {
					source.entries[index] = entry
					break
				}
			}
			compatible, err := cache.inputAlgorithmsCompatible(source, target)
			if err != nil || compatible {
				t.Fatalf("producer %q compatible=%t err=%v", filePath, compatible, err)
			}
		})
	}
}

func TestVerifyRemoteWorkloadPassSourceInputReusesCompatibleAuthoritativeDigest(t *testing.T) {
	target := testRemoteReplayAlgorithmSnapshot("target", "same", "target-unrelated")
	source := testRemoteReplayAlgorithmSnapshot("source", "same", "source-unrelated")
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatal(err)
	}
	identity := gate.WorkloadPassIdentity{InputDigest: "sha256:input"}
	candidate := gate.WorkloadPassEvidence{
		Identity: identity, OriginSourceTreeSHA: "source",
	}
	matched, err := verifyRemoteWorkloadPassSourceInput(
		context.Background(), RunInput{RepositoryRoot: "repo"}, identity, candidate,
		gate.Workload{}, source, target, false, cache, &ReuseReplayDiagnostic{},
	)
	if err != nil || !matched {
		t.Fatalf("compatible authoritative input matched=%t err=%v", matched, err)
	}
	if cache.inputComputations != 0 {
		t.Fatalf("compatible authoritative input recomputed %d digests", cache.inputComputations)
	}
}

func TestPrewarmRemoteWorkloadPassSourceInputsSkipsCompatibleAlgorithm(t *testing.T) {
	target := testRemoteReplayAlgorithmSnapshot("target", "same", "target-unrelated")
	source := testRemoteReplayAlgorithmSnapshot("source", "same", "source-unrelated")
	cache, err := newRemoteReplayCache("repo", "target", target)
	if err != nil {
		t.Fatal(err)
	}
	compatible, err := prewarmRemoteWorkloadPassSourceInputs(
		context.Background(), "repo", "source", []gate.Workload{{ID: "would-require-full-input"}},
		source, target, cache,
	)
	if err != nil || !compatible {
		t.Fatalf("compatible prewarm skipped=%t err=%v", compatible, err)
	}
	if cache.inputComputations != 0 {
		t.Fatalf("compatible prewarm computed %d source inputs", cache.inputComputations)
	}
}

func TestPrewarmRemoteWorkloadPassSourceInputsLoadsPersistentDigest(t *testing.T) {
	cache, source, target, workload, wantDigest := persistentInputReplayTestFixture(t)
	sourceTree := source.tree
	compatible, err := prewarmRemoteWorkloadPassSourceInputs(context.Background(), "repo", sourceTree, []gate.Workload{workload}, source, target, cache)
	if err != nil || compatible {
		t.Fatalf("persistent incompatible prewarm compatible=%t err=%v", compatible, err)
	}
	key := remoteReplayWorkloadKey{tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: sourceTree}, workloadID: workload.ID}
	assertPersistentInputDigest(t, cache, key, wantDigest)
	assertPersistentSemanticVotes(t, cache, key)
	assertPersistentInputCounters(t, cache)
	if err := cache.persistComputedInputDigests(); err != nil {
		t.Fatal(err)
	}
	if cache.persistentInputWrites != 0 {
		t.Fatalf("persistent cache rewrote %d loaded inputs", cache.persistentInputWrites)
	}
}

func TestPreloadRemoteWorkloadPassSourceInputsLoadsPersistentDigest(t *testing.T) {
	cache, source, target, workload, wantDigest := persistentInputReplayTestFixture(t)
	workloadID := gate.GateID(workload.ID)
	candidates := map[gate.GateID][]gate.WorkloadPassEvidence{
		workloadID: {{OriginSourceTreeSHA: source.tree}},
	}
	workloads := map[gate.GateID]gate.Workload{workloadID: workload}
	if err := preloadRemoteWorkloadPassSourceInputs(t.Context(), "repo", target.tree, candidates, workloads, cache); err != nil {
		t.Fatal(err)
	}
	key := remoteReplayWorkloadKey{tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: source.tree}, workloadID: workload.ID}
	assertPersistentInputDigest(t, cache, key, wantDigest)
	assertPersistentSemanticVotes(t, cache, key)
	assertPersistentInputCounters(t, cache)
}

func assertPersistentInputDigest(t *testing.T, cache *remoteReplayCache, key remoteReplayWorkloadKey, want string) {
	t.Helper()
	if got := cache.inputDigests[key]; !got.available || got.digest != want {
		t.Fatalf("persistent workload input digest = %#v, want %s", got, want)
	}
}

func assertPersistentSemanticVotes(t *testing.T, cache *remoteReplayCache, key remoteReplayWorkloadKey) {
	t.Helper()
	if got := cache.semanticInputVotes[key]; !got.supported || got.digests.declaration == "" || got.digests.runtime == "" {
		t.Fatalf("persistent semantic votes = %#v", got)
	}
}

func assertPersistentInputCounters(t *testing.T, cache *remoteReplayCache) {
	t.Helper()
	if cache.inputComputations != 0 || cache.semanticComputations != 0 || cache.persistentInputHits != 1 {
		t.Fatalf("persistent cache input=%d semantic=%d hits=%d", cache.inputComputations, cache.semanticComputations, cache.persistentInputHits)
	}
}

func TestPersistentInputReplayRoundTripsSemanticVotesAcrossPrepare(t *testing.T) {
	fixture := newPersistentSemanticReplayFixture(t)
	first := fixture.persist(t)
	second := fixture.load(t)
	if got := second.semanticInputVotes[fixture.key]; !got.supported || got.digests != first.semanticInputVotes[fixture.key].digests {
		t.Fatalf("second Prepare semantic votes = %#v", got)
	}
	if second.semanticComputations != 0 || second.persistentInputHits != 1 {
		t.Fatalf("second Prepare semantic computations=%d persistent hits=%d", second.semanticComputations, second.persistentInputHits)
	}
}

type persistentSemanticReplayFixture struct {
	store    *gate.DurationLedgerStore
	target   *remoteGitTreeSnapshot
	source   *remoteGitTreeSnapshot
	workload gate.Workload
	key      remoteReplayWorkloadKey
}

func newPersistentSemanticReplayFixture(t *testing.T) persistentSemanticReplayFixture {
	t.Helper()
	targetTree := strings.Repeat("a", 40)
	sourceTree := strings.Repeat("b", 40)
	store, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(0, gate.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedRemoteCITestAcceptedGeneration(t, store, 1)
	workload := gate.Workload{ID: string(gate.GateIDWhitespaceCheck)}
	return persistentSemanticReplayFixture{
		store: store, target: testRemoteReplayAlgorithmSnapshot(targetTree, "stable-algorithm", "target-unrelated"),
		source: testRemoteReplayAlgorithmSnapshot(sourceTree, "stable-algorithm", "source-unrelated"), workload: workload,
		key: remoteReplayWorkloadKey{tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: sourceTree}, workloadID: workload.ID},
	}
}

func (fixture persistentSemanticReplayFixture) persist(t *testing.T) *remoteReplayCache {
	t.Helper()
	cache, err := newRemoteReplayCache("repo", fixture.target.tree, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.bindPersistentInputReplayCache(fixture.store, 1, fixture.target); err != nil {
		t.Fatal(err)
	}
	cache.inputDigests[fixture.key] = remoteReplayInputDigestResult{digest: "sha256:" + strings.Repeat("c", 64), available: true}
	cache.semanticInputVotes[fixture.key] = remoteReplaySemanticDigestResult{supported: true, digests: remoteWorkloadInputVoteDigests{
		compile: "sha256:" + strings.Repeat("d", 64), declaration: "sha256:" + strings.Repeat("e", 64), runtime: "sha256:" + strings.Repeat("f", 64),
	}}
	if err := cache.persistComputedInputDigests(); err != nil {
		t.Fatal(err)
	}
	if cache.persistentInputWrites != 1 {
		t.Fatalf("first Prepare persistent writes = %d, want 1", cache.persistentInputWrites)
	}
	return cache
}

func (fixture persistentSemanticReplayFixture) load(t *testing.T) *remoteReplayCache {
	t.Helper()
	cache, err := newRemoteReplayCache("repo", fixture.target.tree, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.bindPersistentInputReplayCache(fixture.store, 1, fixture.target); err != nil {
		t.Fatal(err)
	}
	cache.snapshots[fixture.key.tree] = remoteReplaySnapshotResult{snapshot: fixture.source, available: true}
	if err := cache.preloadPersistentInputDigests(fixture.source, []gate.Workload{fixture.workload}); err != nil {
		t.Fatal(err)
	}
	return cache
}

func persistentInputReplayTestFixture(t *testing.T) (*remoteReplayCache, *remoteGitTreeSnapshot, *remoteGitTreeSnapshot, gate.Workload, string) {
	t.Helper()
	targetTree := strings.Repeat("a", 40)
	sourceTree := strings.Repeat("b", 40)
	target := testRemoteReplayAlgorithmSnapshot(targetTree, "target-algorithm", "target-unrelated")
	source := testRemoteReplayAlgorithmSnapshot(sourceTree, "source-algorithm", "source-unrelated")
	cache, err := newRemoteReplayCache("repo", targetTree, target)
	if err != nil {
		t.Fatal(err)
	}
	store, err := gate.NewDurationLedgerStore(filepath.Join(t.TempDir(), "duration-ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(0, gate.NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedRemoteCITestAcceptedGeneration(t, store, 1)
	if err := cache.bindPersistentInputReplayCache(store, 1, target); err != nil {
		t.Fatal(err)
	}
	workload := gate.Workload{ID: string(gate.GateIDWhitespaceCheck)}
	wantDigest := "sha256:" + strings.Repeat("c", 64)
	value := gate.WorkloadInputReplayCacheValue{
		InputDigest: wantDigest, CompileDigest: "sha256:" + strings.Repeat("d", 64),
		DeclarationDigest: "sha256:" + strings.Repeat("e", 64), RuntimeDigest: "sha256:" + strings.Repeat("f", 64),
	}
	if err := store.SaveWorkloadInputReplayCache(1, cache.persistentInputAlgorithm, []gate.WorkloadInputReplayCacheEntry{{SourceTreeSHA: sourceTree, WorkloadID: gate.GateID(workload.ID), Value: value}}); err != nil {
		t.Fatal(err)
	}
	cache.snapshots[remoteReplayTreeKey{repositoryRoot: "repo", tree: sourceTree}] = remoteReplaySnapshotResult{snapshot: source, available: true}
	return cache, source, target, workload, wantDigest
}

func testRemoteReplayAlgorithmSnapshot(tree, algorithmObject, unrelatedObject string) *remoteGitTreeSnapshot {
	entries := make([]remoteGitTreeEntry, 0, len(remoteWorkloadInputAlgorithmRequiredPaths())+1)
	for _, filePath := range remoteWorkloadInputAlgorithmRequiredPaths() {
		objectID := algorithmObject
		if filePath != "internal/devtools/remoteci/workload_fingerprint_digests.go" {
			objectID = "stable"
		}
		entries = append(entries, remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: objectID, path: filePath})
	}
	entries = append(entries, remoteGitTreeEntry{mode: "100644", kind: "blob", objectID: unrelatedObject, path: "docs/unrelated.md"})
	byPath := make(map[string]remoteGitTreeEntry, len(entries))
	for _, entry := range entries {
		byPath[entry.path] = entry
	}
	return &remoteGitTreeSnapshot{repositoryRoot: "repo", tree: tree, entries: entries, byPath: byPath}
}
