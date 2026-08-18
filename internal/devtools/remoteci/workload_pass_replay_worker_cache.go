package remoteci

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// previousUnscopedWorkerDigest 对同一来源树只重建一次 SQLC 函数体收窄前的 v4 摘要。
func (cache *remoteReplayCache) previousUnscopedWorkerDigest(ctx context.Context, snapshot *remoteGitTreeSnapshot) (string, error) {
	if cache == nil {
		return "", errors.New("remote workload PASS replay cache is required")
	}
	key, err := remoteReplaySnapshotKey(snapshot)
	if err != nil {
		return "", err
	}
	if digest := cache.previousUnscopedWorkerDigests[key]; digest != "" {
		return digest, nil
	}
	cache.previousUnscopedComputations++
	digest, err := snapshot.workerExecutionContractDigestPreviousUnscopedV4(ctx)
	if err != nil {
		return "", err
	}
	return cache.rememberWorkerDigest(cache.previousUnscopedWorkerDigests, key, digest)
}

// preloadPersistentWorkerDigests 批量读取 precise 与旧 v4 worker 摘要，避免重复解析执行闭包。
func (cache *remoteReplayCache) preloadPersistentWorkerDigests(repositoryRoot string, trees []string) error {
	if cache == nil || cache.persistentInputStore == nil || len(trees) == 0 {
		return nil
	}
	if err := cache.preloadPersistentWorkerDigestAlgorithm(repositoryRoot, trees, cache.persistentWorkerAlgorithm, cache.preciseWorkerDigests, cache.persistentLoadedWorkers); err != nil {
		return err
	}
	return cache.preloadPersistentWorkerDigestAlgorithm(repositoryRoot, trees, cache.persistentPreviousUnscopedWorkerAlgorithm, cache.previousUnscopedWorkerDigests, cache.persistentLoadedPreviousUnscopedWorkers)
}

// preloadPersistentWorkerDigestAlgorithm 按独立算法域填充一种 worker 摘要，禁止跨版本复用。
func (cache *remoteReplayCache) preloadPersistentWorkerDigestAlgorithm(repositoryRoot string, trees []string, algorithm string, destination map[remoteReplayTreeKey]string, loaded map[remoteReplayTreeKey]struct{}) error {
	hits, err := cache.persistentInputStore.LookupWorkerExecutionReplayCache(cache.persistentAcceptedGeneration, algorithm, trees)
	if err != nil {
		return err
	}
	for tree, digest := range hits {
		key := remoteReplayTreeKey{repositoryRoot: repositoryRoot, tree: tree}
		destination[key] = digest
		loaded[key] = struct{}{}
		cache.persistentWorkerHits++
	}
	return nil
}

// persistComputedWorkerDigests 分别写回 current 与旧 v4 算法域的本轮新结果。
func (cache *remoteReplayCache) persistComputedWorkerDigests() error {
	if err := cache.persistWorkerDigestMap(cache.persistentWorkerAlgorithm, cache.preciseWorkerDigests, cache.persistentLoadedWorkers); err != nil {
		return err
	}
	return cache.persistWorkerDigestMap(cache.persistentPreviousUnscopedWorkerAlgorithm, cache.previousUnscopedWorkerDigests, cache.persistentLoadedPreviousUnscopedWorkers)
}

// persistWorkerDigestMap 只写本轮计算且未从 SQLite 命中的 tree 摘要。
func (cache *remoteReplayCache) persistWorkerDigestMap(algorithm string, digests map[remoteReplayTreeKey]string, loadedKeys map[remoteReplayTreeKey]struct{}) error {
	entries := make([]gate.WorkerExecutionReplayCacheEntry, 0, len(digests))
	for key, digest := range digests {
		if _, loaded := loadedKeys[key]; loaded {
			continue
		}
		entries = append(entries, gate.WorkerExecutionReplayCacheEntry{SourceTreeSHA: key.tree, WorkerDigest: digest})
	}
	if len(entries) == 0 {
		return nil
	}
	if err := cache.persistentInputStore.SaveWorkerExecutionReplayCache(cache.persistentAcceptedGeneration, algorithm, entries); err != nil {
		return err
	}
	cache.persistentWorkerWrites += uint64(len(entries))
	return nil
}
