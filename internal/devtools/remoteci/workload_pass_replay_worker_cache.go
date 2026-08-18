package remoteci

import (
	"context"
	"errors"
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
