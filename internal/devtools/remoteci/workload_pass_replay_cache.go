package remoteci

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

type remoteReplayTreeKey struct {
	repositoryRoot string
	tree           string
}

type remoteReplayWorkloadKey struct {
	tree       remoteReplayTreeKey
	workloadID string
}

type remoteReplaySnapshotResult struct {
	snapshot  *remoteGitTreeSnapshot
	available bool
}

type remoteReplayInputDigestResult struct {
	digest    string
	available bool
}

// remoteReplayCache 只属于一次串行 Prepare；内容寻址 tree 的成功值和不可用事实可复用，错误始终立即返回。
type remoteReplayCache struct {
	snapshots            map[remoteReplayTreeKey]remoteReplaySnapshotResult
	inputDigests         map[remoteReplayWorkloadKey]remoteReplayInputDigestResult
	legacyWorkerDigests  map[remoteReplayTreeKey]string
	preciseWorkerDigests map[remoteReplayTreeKey]string
	snapshotComputations uint64
	snapshotLoads        uint64
	inputComputations    uint64
	legacyComputations   uint64
	preciseComputations  uint64
}

// newRemoteReplayCache 用已完成 correctness 指纹计算的当前快照预热 replay，拒绝错树绑定。
func newRemoteReplayCache(repositoryRoot, tree string, current *remoteGitTreeSnapshot) (*remoteReplayCache, error) {
	cache := &remoteReplayCache{
		snapshots:            make(map[remoteReplayTreeKey]remoteReplaySnapshotResult),
		inputDigests:         make(map[remoteReplayWorkloadKey]remoteReplayInputDigestResult),
		legacyWorkerDigests:  make(map[remoteReplayTreeKey]string),
		preciseWorkerDigests: make(map[remoteReplayTreeKey]string),
	}
	if current == nil {
		return cache, nil
	}
	if current.repositoryRoot != repositoryRoot || current.tree != tree {
		return nil, errors.New("remote workload PASS replay current snapshot identity drifted")
	}
	key := remoteReplayTreeKey{repositoryRoot: repositoryRoot, tree: tree}
	cache.snapshots[key] = remoteReplaySnapshotResult{snapshot: current, available: true}
	return cache, nil
}

// snapshot 按 repository/tree 复用精确 Git 快照；对象缺失只缓存为不可用，不伪造内容。
func (cache *remoteReplayCache) snapshot(ctx context.Context, repositoryRoot, tree string) (*remoteGitTreeSnapshot, bool, error) {
	if cache == nil {
		return nil, false, errors.New("remote workload PASS replay cache is required")
	}
	key := remoteReplayTreeKey{repositoryRoot: repositoryRoot, tree: tree}
	if cached, ok := cache.snapshots[key]; ok {
		return cached.snapshot, cached.available, nil
	}
	cache.snapshotComputations++
	if !validRemoteGitTreeRequest(ctx, repositoryRoot, tree) {
		cache.snapshots[key] = remoteReplaySnapshotResult{}
		return nil, false, nil
	}
	available, err := remoteReplayTreeAvailable(ctx, repositoryRoot, tree)
	if err != nil {
		return nil, false, err
	}
	if !available {
		cache.snapshots[key] = remoteReplaySnapshotResult{}
		return nil, false, nil
	}
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, tree)
	if err != nil {
		return nil, false, err
	}
	cache.snapshotLoads++
	cache.snapshots[key] = remoteReplaySnapshotResult{snapshot: snapshot, available: true}
	return snapshot, true, nil
}

// remoteReplayTreeAvailable 通过 batch-check 区分对象缺失与 Git 执行故障，并严格校验 tree 输出。
func remoteReplayTreeAvailable(ctx context.Context, repositoryRoot, tree string) (bool, error) {
	query := tree + "^{tree}"
	output, err := runGitOutput(ctx, repositoryRoot, strings.NewReader(query+"\n"), "cat-file", "--batch-check")
	if err != nil {
		return false, fmt.Errorf("verify remote workload PASS replay tree: %w", err)
	}
	line, err := strictGitLine(output)
	if err != nil {
		return false, fmt.Errorf("parse remote workload PASS replay tree: %w", err)
	}
	if line == query+" missing" {
		return false, nil
	}
	fields := strings.Fields(line)
	if len(fields) != 3 || fields[0] != tree || fields[1] != "tree" {
		return false, errors.New("remote workload PASS replay tree batch-check output is invalid")
	}
	if _, err := strconv.ParseUint(fields[2], 10, 64); err != nil {
		return false, errors.New("remote workload PASS replay tree size is invalid")
	}
	return true, nil
}

// inputDigest 缓存同一 tree/workload 的生产输入摘要和确定性不可用结果。
func (cache *remoteReplayCache) inputDigest(ctx context.Context, repositoryRoot, tree string, workload gate.Workload) (string, bool, error) {
	if cache == nil {
		return "", false, errors.New("remote workload PASS replay cache is required")
	}
	key := remoteReplayWorkloadKey{tree: remoteReplayTreeKey{repositoryRoot: repositoryRoot, tree: tree}, workloadID: workload.ID}
	if cached, ok := cache.inputDigests[key]; ok {
		return cached.digest, cached.available, nil
	}
	snapshot, available, err := cache.snapshot(ctx, repositoryRoot, tree)
	if err != nil || !available {
		return cache.rememberInputDigest(key, "", available, err)
	}
	digest, err := snapshot.workloadInputDigest(ctx, workload)
	cache.inputComputations++
	if errors.Is(err, errRemoteWorkloadInputUnavailable) {
		return cache.rememberInputDigest(key, "", false, nil)
	}
	if err != nil {
		return "", false, err
	}
	if digest == "" {
		return "", false, errors.New("remote workload PASS replay produced an empty input digest")
	}
	return cache.rememberInputDigest(key, digest, true, nil)
}

// rememberInputDigest 只保存成功或不可用；真实错误不进入缓存。
func (cache *remoteReplayCache) rememberInputDigest(key remoteReplayWorkloadKey, digest string, available bool, err error) (string, bool, error) {
	if err != nil {
		return "", false, err
	}
	cache.inputDigests[key] = remoteReplayInputDigestResult{digest: digest, available: available}
	return digest, available, nil
}

// legacyWorkerDigest 保证同一 tree 的 broad v4 Worker 摘要在本次 Prepare 中只计算一次。
func (cache *remoteReplayCache) legacyWorkerDigest(ctx context.Context, snapshot *remoteGitTreeSnapshot) (string, error) {
	if cache == nil {
		return "", errors.New("remote workload PASS replay cache is required")
	}
	key, err := remoteReplaySnapshotKey(snapshot)
	if err != nil {
		return "", err
	}
	if digest := cache.legacyWorkerDigests[key]; digest != "" {
		return digest, nil
	}
	cache.legacyComputations++
	digest, err := snapshot.workerExecutionContractDigestLegacyV4(ctx)
	if err != nil {
		return "", err
	}
	return cache.rememberWorkerDigest(cache.legacyWorkerDigests, key, digest)
}

// preciseWorkerDigest 复用 snapshot 的 current Worker 摘要，并在 replay 层按 tree 去重。
func (cache *remoteReplayCache) preciseWorkerDigest(ctx context.Context, snapshot *remoteGitTreeSnapshot) (string, error) {
	if cache == nil {
		return "", errors.New("remote workload PASS replay cache is required")
	}
	key, err := remoteReplaySnapshotKey(snapshot)
	if err != nil {
		return "", err
	}
	if digest := cache.preciseWorkerDigests[key]; digest != "" {
		return digest, nil
	}
	cache.preciseComputations++
	digest, err := snapshot.workerExecutionDigest(ctx)
	if err != nil {
		return "", err
	}
	return cache.rememberWorkerDigest(cache.preciseWorkerDigests, key, digest)
}

// rememberWorkerDigest 拒绝空摘要，避免把未完成计算伪装成 cache hit。
func (cache *remoteReplayCache) rememberWorkerDigest(target map[remoteReplayTreeKey]string, key remoteReplayTreeKey, digest string) (string, error) {
	if cache == nil || digest == "" {
		return "", errors.New("remote workload PASS replay worker digest is empty")
	}
	target[key] = digest
	return digest, nil
}

// remoteReplaySnapshotKey 从已加载快照读取缓存身份，拒绝脱离 repository/tree 的合成对象。
func remoteReplaySnapshotKey(snapshot *remoteGitTreeSnapshot) (remoteReplayTreeKey, error) {
	if snapshot == nil || snapshot.repositoryRoot == "" || snapshot.tree == "" {
		return remoteReplayTreeKey{}, fmt.Errorf("remote workload PASS replay snapshot identity is incomplete")
	}
	return remoteReplayTreeKey{repositoryRoot: snapshot.repositoryRoot, tree: snapshot.tree}, nil
}
