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
	uncached := gate.Workload{ID: string(gate.GateIDProjectMapCheck)}
	uncachedID := gate.GateID(uncached.ID)
	candidates := map[gate.GateID][]gate.WorkloadPassEvidence{
		workloadID: {{OriginSourceTreeSHA: source.tree}},
		uncachedID: {{OriginSourceTreeSHA: source.tree}},
	}
	workloads := map[gate.GateID]gate.Workload{workloadID: workload, uncachedID: uncached}
	if err := preloadRemoteWorkloadPassSourceInputs(t.Context(), "repo", target.tree, candidates, workloads, nil, cache); err != nil {
		t.Fatal(err)
	}
	key := remoteReplayWorkloadKey{tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: source.tree}, workloadID: workload.ID}
	assertPersistentInputDigest(t, cache, key, wantDigest)
	assertPersistentSemanticVotes(t, cache, key)
	uncachedKey := remoteReplayWorkloadKey{tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: source.tree}, workloadID: uncached.ID}
	if _, ok := cache.inputDigests[uncachedKey]; !ok {
		t.Fatal("source preload deferred an uncached workload to serial source voting")
	}
	if cache.inputComputations != 1 || cache.persistentInputHits != 1 {
		t.Fatalf("source preload computations=%d persistent hits=%d, want 1/1", cache.inputComputations, cache.persistentInputHits)
	}
}

func TestPreloadRemoteWorkloadPassSourceInputsPrewarmsTargetSemanticVote(t *testing.T) {
	const repositoryRoot = "repo"
	newSnapshot := func(tree string) *remoteGitTreeSnapshot {
		snapshot := testExactGoTestDigestSnapshotWithObservedFiles(
			"package fixture\nimport \"testing\"\nfunc TestX(t *testing.T) {}\n", "fixture", "unrelated",
		)
		snapshot.repositoryRoot = repositoryRoot
		snapshot.tree = tree
		return snapshot
	}
	target := newSnapshot(strings.Repeat("a", 40))
	source := newSnapshot(strings.Repeat("b", 40))
	cache, err := newRemoteReplayCache(repositoryRoot, target.tree, target)
	if err != nil {
		t.Fatal(err)
	}
	cache.snapshots[remoteReplayTreeKey{repositoryRoot: repositoryRoot, tree: source.tree}] = remoteReplaySnapshotResult{snapshot: source, available: true}
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./fixture", "TestX", 1_000)
	if err != nil {
		t.Fatal(err)
	}
	workloadID := gate.GateID(workload.ID)
	candidates := map[gate.GateID][]gate.WorkloadPassEvidence{workloadID: {{OriginSourceTreeSHA: source.tree}}}
	workloads := map[gate.GateID]gate.Workload{workloadID: workload}
	if err := preloadRemoteWorkloadPassSourceInputs(t.Context(), repositoryRoot, target.tree, candidates, workloads, nil, cache); err != nil {
		t.Fatal(err)
	}
	targetKey := remoteReplayWorkloadKey{tree: cache.currentTree, workloadID: workload.ID}
	if cached := cache.semanticInputVotes[targetKey]; !cached.supported || cached.digests.declaration == "" || cached.digests.runtime == "" {
		t.Fatalf("target semantic vote remained serial: %#v", cached)
	}
}

func TestRemoteWorkloadPassSourceInputPartitionsOnlyPrewarmPreferredTree(t *testing.T) {
	workload := gate.Workload{ID: string(gate.GateIDWhitespaceCheck)}
	workloadID := gate.GateID(workload.ID)
	candidates := map[gate.GateID][]gate.WorkloadPassEvidence{workloadID: {
		{OriginSourceTreeSHA: "slower"},
		{OriginSourceTreeSHA: "preferred"},
	}}
	partitions := remoteWorkloadPassSourceInputPartitions(
		candidates,
		map[gate.GateID]gate.Workload{workloadID: workload},
		map[string]int{"preferred": 0, "slower": 1},
	)
	if len(partitions) != 1 || len(partitions["preferred"]) != 1 {
		t.Fatalf("source input prewarm partitions = %#v", partitions)
	}
	if _, overcomputed := partitions["slower"]; overcomputed {
		t.Fatal("source input prewarm computed a later best-first candidate")
	}
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

func TestPersistentInputReplayRejectsSeededBroadDigestDrift(t *testing.T) {
	cache, source, _, workload, _ := persistentInputReplayTestFixture(t)
	key := remoteReplayWorkloadKey{
		tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: source.tree}, workloadID: workload.ID,
	}
	cache.inputDigests[key] = remoteReplayInputDigestResult{digest: "sha256:" + strings.Repeat("9", 64), available: true}
	err := cache.preloadPersistentInputDigests(source, []gate.Workload{workload})
	if err == nil || !strings.Contains(err.Error(), "drifted from the current identity") {
		t.Fatalf("persistent seeded digest drift error = %v", err)
	}
}

func TestPersistentInputReplayRoundTripsCurrentTargetVotesAcrossPrepare(t *testing.T) {
	fixture := newPersistentSemanticReplayFixture(t)
	key := remoteReplayWorkloadKey{
		tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: fixture.target.tree}, workloadID: fixture.workload.ID,
	}
	first := fixture.persistKey(t, key)
	second, err := newRemoteReplayCache("repo", fixture.target.tree, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.bindPersistentInputReplayCache(fixture.store, 1, fixture.target); err != nil {
		t.Fatal(err)
	}
	second.inputDigests[key] = first.inputDigests[key]
	if err := second.preloadPersistentInputDigests(fixture.target, []gate.Workload{fixture.workload}); err != nil {
		t.Fatal(err)
	}
	if got := second.semanticInputVotes[key]; !got.supported || got.digests != first.semanticInputVotes[key].digests {
		t.Fatalf("second Prepare target votes = %#v", got)
	}
}

// TestPrepareIdentityUsesPersistentCurrentTreeInputDigest 锁定第二次 Prepare 在
// 当前树已有 SQLite 派生摘要时直接复用；只有缺项才重新计算。
func TestPrepareIdentityUsesPersistentCurrentTreeInputDigest(t *testing.T) {
	fixture := newPersistentSemanticReplayFixture(t)
	key := remoteReplayWorkloadKey{
		tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: fixture.target.tree}, workloadID: fixture.workload.ID,
	}
	fixture.persistKey(t, key)
	input := RunInput{LedgerStore: fixture.store, AcceptedGeneration: 1, Tree: fixture.target.tree}
	digests, err := remoteWorkloadInputDigestsForPrepare(t.Context(), input, fixture.target, []gate.Workload{fixture.workload})
	if err != nil {
		t.Fatal(err)
	}
	want := "sha256:" + strings.Repeat("c", 64)
	if got := digests[fixture.workload.ID]; got != want {
		t.Fatalf("prepared current-tree input digest = %q, want persistent %q", got, want)
	}
}

func TestPersistentWorkerExecutionDigestAvoidsSecondPrepareRebuild(t *testing.T) {
	fixture := newPersistentSemanticReplayFixture(t)
	key := remoteReplayTreeKey{repositoryRoot: "repo", tree: fixture.source.tree}
	want := "sha256:" + strings.Repeat("9", 64)
	fixture.persistWorkerDigest(t, key, want)
	second := fixture.loadWorkerDigest(t)
	got, err := second.preciseWorkerDigest(t.Context(), fixture.source)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || second.preciseComputations != 0 || second.persistentWorkerHits != 1 {
		t.Fatalf("second Prepare worker digest=%q computations=%d hits=%d", got, second.preciseComputations, second.persistentWorkerHits)
	}
}

func TestPersistentPreviousUnscopedWorkerDigestAvoidsSecondPrepareRebuild(t *testing.T) {
	fixture := newPersistentSemanticReplayFixture(t)
	key := remoteReplayTreeKey{repositoryRoot: "repo", tree: fixture.source.tree}
	want := "sha256:" + strings.Repeat("8", 64)
	first := fixture.persistentCache(t)
	first.previousUnscopedWorkerDigests[key] = want
	if err := first.persistComputedInputDigests(); err != nil {
		t.Fatal(err)
	}
	if first.persistentWorkerWrites != 1 {
		t.Fatalf("first Prepare previous-unscoped worker writes = %d, want 1", first.persistentWorkerWrites)
	}
	second := fixture.loadWorkerDigest(t)
	got, err := second.previousUnscopedWorkerDigest(t.Context(), fixture.source)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || second.previousUnscopedComputations != 0 || second.persistentWorkerHits != 1 {
		t.Fatalf("second Prepare previous-unscoped digest=%q computations=%d hits=%d", got, second.previousUnscopedComputations, second.persistentWorkerHits)
	}
}

// TestPrepareIdentityUsesPersistentCurrentTreeWorkerDigest 锁定 identity 阶段
// 优先消费当前树 worker 派生摘要，历史为空时才重建执行闭包。
func TestPrepareIdentityUsesPersistentCurrentTreeWorkerDigest(t *testing.T) {
	fixture := newPersistentSemanticReplayFixture(t)
	key := remoteReplayTreeKey{repositoryRoot: "repo", tree: fixture.target.tree}
	want := "sha256:" + strings.Repeat("9", 64)
	fixture.persistWorkerDigest(t, key, want)
	input := RunInput{LedgerStore: fixture.store, AcceptedGeneration: 1, Tree: fixture.target.tree}
	got, err := remoteWorkerExecutionDigestForPrepare(t.Context(), input, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("prepared current-tree worker digest = %q, want persistent %q", got, want)
	}
}

// TestPersistentInputReplaySeedsCompileDigest 验证持久语义缓存同时预热按包 compile 摘要，
// 第二次 Prepare 的 source vote 不得重复遍历同一 Go package 编译闭包。
func TestPersistentInputReplaySeedsCompileDigest(t *testing.T) {
	cache, source, target, workload := persistentCompileReplayFixture(t)
	decision, supported, err := cache.compileInputDecision(t.Context(), workload, source, target)
	if err != nil || !supported || decision.compileMiss {
		t.Fatalf("persistent compile decision = %#v supported=%t err=%v", decision, supported, err)
	}
	if cache.compileComputations != 0 {
		t.Fatalf("second Prepare recomputed %d compile inputs, want 0", cache.compileComputations)
	}
}

func persistentCompileReplayFixture(t *testing.T) (*remoteReplayCache, *remoteGitTreeSnapshot, *remoteGitTreeSnapshot, gate.Workload) {
	t.Helper()
	target := testRemoteReplayAlgorithmSnapshot(strings.Repeat("a", 40), "stable-algorithm", "target-unrelated")
	source := testRemoteReplayAlgorithmSnapshot(strings.Repeat("b", 40), "stable-algorithm", "source-unrelated")
	cache, err := newRemoteReplayCache("repo", target.tree, target)
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
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/devtools/gate", "TestPersistentInputReplaySeedsCompileDigest", 1_000)
	if err != nil {
		t.Fatal(err)
	}
	compileDigest := "sha256:" + strings.Repeat("d", 64)
	value := gate.WorkloadInputReplayCacheValue{
		InputDigest: "sha256:" + strings.Repeat("c", 64), CompileDigest: compileDigest,
		DeclarationDigest: "sha256:" + strings.Repeat("e", 64), RuntimeDigest: "sha256:" + strings.Repeat("f", 64),
	}
	entries := []gate.WorkloadInputReplayCacheEntry{
		{SourceTreeSHA: source.tree, WorkloadID: gate.GateID(workload.ID), Value: value},
		{SourceTreeSHA: target.tree, WorkloadID: gate.GateID(workload.ID), Value: value},
	}
	if err := store.SaveWorkloadInputReplayCache(1, cache.persistentInputAlgorithm, entries); err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []*remoteGitTreeSnapshot{source, target} {
		if err := cache.preloadPersistentInputDigests(snapshot, []gate.Workload{workload}); err != nil {
			t.Fatal(err)
		}
	}
	return cache, source, target, workload
}

func TestPrewarmCurrentSemanticInputDigestsCompletesTargetCacheEntry(t *testing.T) {
	fixture := newPersistentSemanticReplayFixture(t)
	cache, err := newRemoteReplayCache("repo", fixture.target.tree, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	key := remoteReplayWorkloadKey{
		tree: remoteReplayTreeKey{repositoryRoot: "repo", tree: fixture.target.tree}, workloadID: fixture.workload.ID,
	}
	cache.semanticInputVotes[key] = remoteReplaySemanticDigestResult{supported: true}
	workloads := map[gate.GateID]gate.Workload{gate.GateID(fixture.workload.ID): fixture.workload}
	if err := cache.prewarmCurrentSemanticInputDigests(t.Context(), workloads); err != nil {
		t.Fatal(err)
	}
	if got := cache.inputDigests[key]; !got.available || got.digest == "" {
		t.Fatalf("current target broad digest = %#v", got)
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
	return fixture.persistKey(t, fixture.key)
}

func (fixture persistentSemanticReplayFixture) persistentCache(t *testing.T) *remoteReplayCache {
	t.Helper()
	cache, err := newRemoteReplayCache("repo", fixture.target.tree, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.bindPersistentInputReplayCache(fixture.store, 1, fixture.target); err != nil {
		t.Fatal(err)
	}
	return cache
}

func (fixture persistentSemanticReplayFixture) persistWorkerDigest(t *testing.T, key remoteReplayTreeKey, digest string) {
	t.Helper()
	cache := fixture.persistentCache(t)
	cache.preciseWorkerDigests[key] = digest
	if err := cache.persistComputedInputDigests(); err != nil {
		t.Fatal(err)
	}
	if cache.persistentWorkerWrites != 1 {
		t.Fatalf("first Prepare worker writes = %d, want 1", cache.persistentWorkerWrites)
	}
}

func (fixture persistentSemanticReplayFixture) loadWorkerDigest(t *testing.T) *remoteReplayCache {
	t.Helper()
	cache := fixture.persistentCache(t)
	if err := cache.preloadPersistentWorkerDigests("repo", []string{fixture.source.tree}); err != nil {
		t.Fatal(err)
	}
	return cache
}

func (fixture persistentSemanticReplayFixture) persistKey(t *testing.T, key remoteReplayWorkloadKey) *remoteReplayCache {
	t.Helper()
	cache, err := newRemoteReplayCache("repo", fixture.target.tree, fixture.target)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.bindPersistentInputReplayCache(fixture.store, 1, fixture.target); err != nil {
		t.Fatal(err)
	}
	cache.inputDigests[key] = remoteReplayInputDigestResult{digest: "sha256:" + strings.Repeat("c", 64), available: true}
	cache.semanticInputVotes[key] = remoteReplaySemanticDigestResult{supported: true, digests: remoteWorkloadInputVoteDigests{
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
