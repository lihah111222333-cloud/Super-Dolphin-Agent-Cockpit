package remoteci

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestRemoteReplayCacheDeduplicatesExactTreeMaterials(t *testing.T) {
	repositoryRoot, input, snapshot, cache := remoteReplayCacheFixture(t)
	workloads := remoteShardableWorkloads(mustCoordinatorCatalog(t, input))
	if len(workloads) == 0 {
		t.Fatal("remote replay fixture has no workloads")
	}
	assertRemoteReplayInputDeduplication(t, cache, repositoryRoot, input.Tree, workloads[0])
	assertRemoteReplayWorkerDeduplication(t, cache, snapshot)
	if cache.snapshotComputations != 0 || cache.snapshotLoads != 0 {
		t.Fatalf("seeded current snapshot recomputed: resolutions=%d loads=%d", cache.snapshotComputations, cache.snapshotLoads)
	}
}

func assertRemoteReplayInputDeduplication(t *testing.T, cache *remoteReplayCache, repositoryRoot, tree string, workload gate.Workload) {
	t.Helper()
	firstInput := cachedRemoteReplayInputDigest(t, cache, repositoryRoot, tree, workload)
	secondInput := cachedRemoteReplayInputDigest(t, cache, repositoryRoot, tree, workload)
	if firstInput != secondInput || cache.inputComputations != 1 {
		t.Fatalf("input replay cache digest=%q/%q computations=%d", firstInput, secondInput, cache.inputComputations)
	}
}

func assertRemoteReplayWorkerDeduplication(t *testing.T, cache *remoteReplayCache, snapshot *remoteGitTreeSnapshot) {
	t.Helper()
	firstLegacy := cachedRemoteReplayWorkerDigest(t, cache.legacyWorkerDigest, snapshot)
	secondLegacy := cachedRemoteReplayWorkerDigest(t, cache.legacyWorkerDigest, snapshot)
	firstPrecise := cachedRemoteReplayWorkerDigest(t, cache.preciseWorkerDigest, snapshot)
	secondPrecise := cachedRemoteReplayWorkerDigest(t, cache.preciseWorkerDigest, snapshot)
	if firstLegacy != secondLegacy || firstPrecise != secondPrecise {
		t.Fatal("worker replay cache changed an immutable exact-tree digest")
	}
	if cache.legacyComputations != 1 || cache.preciseComputations != 1 {
		t.Fatalf("worker replay computations legacy=%d precise=%d", cache.legacyComputations, cache.preciseComputations)
	}
}

func TestRemoteReplayCacheCachesUnavailableButNotErrors(t *testing.T) {
	repositoryRoot, input := coordinatorReuseFixture(t)
	cache, err := newRemoteReplayCache(repositoryRoot, input.Tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertRemoteReplayUnavailableCached(t, cache, repositoryRoot)
	assertRemoteReplayErrorsNotCached(t, repositoryRoot, input)
}

func assertRemoteReplayUnavailableCached(t *testing.T, cache *remoteReplayCache, repositoryRoot string) {
	t.Helper()
	missingTree := strings.Repeat("0", 40)
	for range 2 {
		if snapshot, available, err := cache.snapshot(context.Background(), repositoryRoot, missingTree); err != nil || available || snapshot != nil {
			t.Fatalf("missing replay snapshot = %#v available=%t error=%v", snapshot, available, err)
		}
	}
	if cache.snapshotComputations != 1 || len(cache.snapshots) != 1 {
		t.Fatalf("unavailable replay snapshot was not cached: resolutions=%d entries=%d", cache.snapshotComputations, len(cache.snapshots))
	}
}

func assertRemoteReplayErrorsNotCached(t *testing.T, repositoryRoot string, input RunInput) {
	t.Helper()
	snapshot, err := loadRemoteGitTreeSnapshot(context.Background(), repositoryRoot, input.Tree)
	if err != nil {
		t.Fatal(err)
	}
	seeded, err := newRemoteReplayCache(repositoryRoot, input.Tree, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := seeded.inputDigest(context.Background(), repositoryRoot, input.Tree, gate.Workload{ID: "bad::id"}); err == nil {
		t.Fatal("invalid replay workload did not fail closed")
	}
	if len(seeded.inputDigests) != 0 {
		t.Fatal("failed replay input digest entered the cache")
	}
	broken, err := newRemoteReplayCache(repositoryRoot, input.Tree, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := broken.snapshot(context.Background(), filepath.Join(repositoryRoot, "missing"), input.Tree); err == nil {
		t.Fatal("replay Git execution failure did not fail fast")
	}
	if len(broken.snapshots) != 0 {
		t.Fatal("replay Git execution failure entered the cache")
	}
}

func TestRemoteReplayCacheRejectsMismatchedCurrentSnapshot(t *testing.T) {
	snapshot := &remoteGitTreeSnapshot{repositoryRoot: "wrong", tree: strings.Repeat("1", 40)}
	if _, err := newRemoteReplayCache("repository", strings.Repeat("2", 40), snapshot); err == nil {
		t.Fatal("replay cache accepted a mismatched current snapshot")
	}
	var cache *remoteReplayCache
	if _, _, err := cache.inputDigest(context.Background(), "repository", strings.Repeat("2", 40), gate.Workload{}); err == nil {
		t.Fatal("nil replay cache did not fail closed")
	}
}

func remoteReplayCacheFixture(t *testing.T) (string, RunInput, *remoteGitTreeSnapshot, *remoteReplayCache) {
	t.Helper()
	repositoryRoot, input := coordinatorReuseFixture(t)
	snapshot, err := loadRemoteGitTreeSnapshot(context.Background(), repositoryRoot, input.Tree)
	if err != nil {
		t.Fatal(err)
	}
	cache, err := newRemoteReplayCache(repositoryRoot, input.Tree, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return repositoryRoot, input, snapshot, cache
}

func cachedRemoteReplayInputDigest(t *testing.T, cache *remoteReplayCache, repositoryRoot, tree string, workload gate.Workload) string {
	t.Helper()
	digest, available, err := cache.inputDigest(context.Background(), repositoryRoot, tree, workload)
	if err != nil || !available || digest == "" {
		t.Fatalf("cache replay input digest=%q available=%t error=%v", digest, available, err)
	}
	return digest
}

func cachedRemoteReplayWorkerDigest(
	t *testing.T,
	resolve func(context.Context, *remoteGitTreeSnapshot) (string, error),
	snapshot *remoteGitTreeSnapshot,
) string {
	t.Helper()
	digest, err := resolve(context.Background(), snapshot)
	if err != nil || digest == "" {
		t.Fatalf("cache replay worker digest=%q error=%v", digest, err)
	}
	return digest
}
