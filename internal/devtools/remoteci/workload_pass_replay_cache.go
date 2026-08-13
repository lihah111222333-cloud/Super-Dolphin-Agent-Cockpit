package remoteci

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
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

type remoteReplayCompileKey struct {
	tree          remoteReplayTreeKey
	packageTarget string
	race          bool
}

type remoteReplayCompileDigestResult struct {
	digest string
}

type remoteReplaySemanticDigestResult struct {
	digests   remoteWorkloadInputVoteDigests
	supported bool
}

type remoteReplayEnvironmentKey struct {
	platform        string
	policyDigest    string
	toolchainDigest string
	runtimeSeed     string
	workerDigest    string
	goFlags         string
}

// remoteReplayCache 只属于一次串行 Prepare；内容寻址 tree 的成功值和不可用事实可复用，错误始终立即返回。
type remoteReplayCache struct {
	snapshots                    map[remoteReplayTreeKey]remoteReplaySnapshotResult
	inputDigests                 map[remoteReplayWorkloadKey]remoteReplayInputDigestResult
	compileInputDigests          map[remoteReplayCompileKey]remoteReplayCompileDigestResult
	semanticInputVotes           map[remoteReplayWorkloadKey]remoteReplaySemanticDigestResult
	previousGroupedWorkerDigests map[remoteReplayTreeKey]string
	legacyWorkerDigests          map[remoteReplayTreeKey]string
	previousWorkerDigests        map[remoteReplayTreeKey]string
	previousStableWorkerDigests  map[remoteReplayTreeKey]string
	preciseWorkerDigests         map[remoteReplayTreeKey]string
	environmentDigests           map[remoteReplayEnvironmentKey]string
	snapshotComputations         uint64
	snapshotLoads                uint64
	inputComputations            uint64
	compileComputations          uint64
	semanticComputations         uint64
	previousGroupedComputations  uint64
	legacyComputations           uint64
	previousComputations         uint64
	previousStableComputations   uint64
	preciseComputations          uint64
	environmentComputations      uint64
}

// newRemoteReplayCache 用已完成 correctness 指纹计算的当前快照预热 replay，拒绝错树绑定。
func newRemoteReplayCache(repositoryRoot, tree string, current *remoteGitTreeSnapshot) (*remoteReplayCache, error) {
	cache := &remoteReplayCache{
		snapshots:                    make(map[remoteReplayTreeKey]remoteReplaySnapshotResult),
		inputDigests:                 make(map[remoteReplayWorkloadKey]remoteReplayInputDigestResult),
		compileInputDigests:          make(map[remoteReplayCompileKey]remoteReplayCompileDigestResult),
		semanticInputVotes:           make(map[remoteReplayWorkloadKey]remoteReplaySemanticDigestResult),
		previousGroupedWorkerDigests: make(map[remoteReplayTreeKey]string),
		legacyWorkerDigests:          make(map[remoteReplayTreeKey]string),
		previousWorkerDigests:        make(map[remoteReplayTreeKey]string),
		previousStableWorkerDigests:  make(map[remoteReplayTreeKey]string),
		preciseWorkerDigests:         make(map[remoteReplayTreeKey]string),
		environmentDigests:           make(map[remoteReplayEnvironmentKey]string),
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

// environmentDigest 在一次 Prepare 的冻结 input/resource 边界内按 worker+GoFlags
// 复用环境摘要。错误不进入缓存，避免把无效配置降级成历史 PASS。
func (cache *remoteReplayCache) environmentDigest(input RunInput, workerTimeout time.Duration, resourcePolicy shardresource.Policy, goFlags string) (string, error) {
	if cache == nil {
		return "", errors.New("remote workload PASS replay cache is required")
	}
	if err := gate.ValidateExecutorWorkloadTimeout(workerTimeout); err != nil {
		return "", fmt.Errorf("validate remote workload environment timeout: %w", err)
	}
	if err := resourcePolicy.Validate(); err != nil {
		return "", fmt.Errorf("validate remote workload resource policy: %w", err)
	}
	if err := gate.ValidateCanonicalGoFlags(goFlags); err != nil {
		return "", fmt.Errorf("validate remote workload GoFlags: %w", err)
	}
	key := remoteReplayEnvironmentKey{
		platform: input.Platform, policyDigest: input.PolicyDigest,
		toolchainDigest: input.ToolchainDigest, runtimeSeed: input.RuntimeSeedSHA256,
		workerDigest: input.WorkerExecutionSemanticDigest, goFlags: goFlags,
	}
	if cached, ok := cache.environmentDigests[key]; ok {
		return cached, nil
	}
	cache.environmentComputations++
	digest, err := remoteWorkloadEnvironmentDigestForGoFlags(input, workerTimeout, resourcePolicy, goFlags)
	if err != nil {
		return "", err
	}
	cache.environmentDigests[key] = digest
	return digest, nil
}

// compileInputDigest 按来源树、包和 race profile 分组缓存编译闭包；
// 同包 selector 共享一次重算，错误不进入缓存。
func (cache *remoteReplayCache) compileInputDigest(ctx context.Context, snapshot *remoteGitTreeSnapshot, workload gate.Workload) (string, bool, error) {
	if cache == nil {
		return "", false, errors.New("remote workload PASS replay cache is required")
	}
	parsed, profile, supported, err := remoteGoWorkloadInputTarget(workload)
	if err != nil || !supported {
		return "", supported, err
	}
	treeKey, err := remoteReplaySnapshotKey(snapshot)
	if err != nil {
		return "", false, err
	}
	key := remoteReplayCompileKey{tree: treeKey, packageTarget: parsed.Package, race: profile.race}
	if cached, ok := cache.compileInputDigests[key]; ok {
		return cached.digest, true, nil
	}
	cache.compileComputations++
	digest, err := snapshot.goPackageInputDigest(ctx, parsed.Package, profile)
	if err != nil {
		return "", false, err
	}
	cache.compileInputDigests[key] = remoteReplayCompileDigestResult{digest: digest}
	return digest, true, nil
}

// compileInputDecision 先比较按包分组的编译闭包；编译变化与 broad 变化足以确认 MISS。
func (cache *remoteReplayCache) compileInputDecision(ctx context.Context, workload gate.Workload, source, target *remoteGitTreeSnapshot) (remoteWorkloadInputVoteDecision, bool, error) {
	sourceDigest, sourceSupported, err := cache.compileInputDigest(ctx, source, workload)
	if err != nil || !sourceSupported {
		return remoteWorkloadInputVoteDecision{}, sourceSupported, err
	}
	targetDigest, targetSupported, err := cache.compileInputDigest(ctx, target, workload)
	if err != nil || !targetSupported {
		return remoteWorkloadInputVoteDecision{}, targetSupported, err
	}
	decision := remoteWorkloadInputVoteDecision{missVotes: 1}
	if sourceDigest != targetDigest {
		decision.compileMiss = true
		decision.missVotes++
	}
	return decision, true, nil
}

// semanticInputVoteDigests 缓存 selector 声明和运行时观察投票，避免同一来源树重复解析 AST。
func (cache *remoteReplayCache) semanticInputVoteDigests(ctx context.Context, snapshot *remoteGitTreeSnapshot, workload gate.Workload) (remoteWorkloadInputVoteDigests, bool, error) {
	if cache == nil {
		return remoteWorkloadInputVoteDigests{}, false, errors.New("remote workload PASS replay cache is required")
	}
	treeKey, err := remoteReplaySnapshotKey(snapshot)
	if err != nil {
		return remoteWorkloadInputVoteDigests{}, false, err
	}
	key := remoteReplayWorkloadKey{tree: treeKey, workloadID: workload.ID}
	if cached, ok := cache.semanticInputVotes[key]; ok {
		return cached.digests, cached.supported, nil
	}
	cache.semanticComputations++
	digests, supported, err := snapshot.workloadInputVoteDigests(ctx, workload)
	if err != nil {
		return remoteWorkloadInputVoteDigests{}, false, err
	}
	cache.semanticInputVotes[key] = remoteReplaySemanticDigestResult{digests: digests, supported: supported}
	return digests, supported, nil
}

func (cache *remoteReplayCache) semanticInputMatches(ctx context.Context, workload gate.Workload, source, target *remoteGitTreeSnapshot) (bool, error) {
	decision, err := cache.semanticInputDecision(ctx, workload, source, target)
	return decision.allowReuse(), err
}

func (cache *remoteReplayCache) semanticInputDecision(ctx context.Context, workload gate.Workload, source, target *remoteGitTreeSnapshot) (remoteWorkloadInputVoteDecision, error) {
	return cache.semanticInputDecisionWithCompileCoverage(ctx, workload, source, target, false)
}

// semanticInputDecisionWithCompileCoverage 在同 package+semantic 已有确定 MISS 时，
// 只让编译变化保留诊断票，不再把同包未变 selector 的测试体一并判 MISS。
func (cache *remoteReplayCache) semanticInputDecisionWithCompileCoverage(ctx context.Context, workload gate.Workload, source, target *remoteGitTreeSnapshot, compileCovered bool) (remoteWorkloadInputVoteDecision, error) {
	compileDecision, compileSupported, err := cache.compileInputDecision(ctx, workload, source, target)
	if err != nil || !compileSupported {
		return remoteWorkloadInputVoteDecision{}, err
	}
	if compileDecision.compileMiss && !compileCovered {
		return compileDecision, nil
	}
	sourceVotes, sourceSupported, err := cache.semanticInputVoteDigests(ctx, source, workload)
	if err != nil || !sourceSupported {
		return remoteWorkloadInputVoteDecision{}, err
	}
	targetVotes, targetSupported, err := cache.semanticInputVoteDigests(ctx, target, workload)
	if err != nil || !targetSupported {
		return remoteWorkloadInputVoteDecision{}, err
	}
	return remoteWorkloadInputVoteDecisionForCompileCoverage(sourceVotes, targetVotes, compileCovered), nil
}

type remoteWorkloadInputVoteDecision struct {
	missVotes       int
	declarationMiss bool
	runtimeMiss     bool
	compileMiss     bool
}

// remoteWorkloadInputVoteDecisionFor 先计 broad MISS，再独立核对声明、包编译与运行时闭包。
// whole-tree runtime fallback 与 broad 同源，只保留包编译票，禁止重复计票。
func remoteWorkloadInputVoteDecisionFor(source, target remoteWorkloadInputVoteDigests) remoteWorkloadInputVoteDecision {
	return remoteWorkloadInputVoteDecisionForCompileCoverage(source, target, false)
}

// remoteWorkloadInputVoteDecisionForCompileCoverage 只在当前 compile owner 尚无 fresh
// 执行兜底时把编译差异计入 MISS 阈值；声明和运行时票始终独立生效。
func remoteWorkloadInputVoteDecisionForCompileCoverage(source, target remoteWorkloadInputVoteDigests, compileCovered bool) remoteWorkloadInputVoteDecision {
	decision := remoteWorkloadInputVoteDecision{missVotes: 1}
	if source.declaration != target.declaration {
		decision.declarationMiss = true
		decision.missVotes++
	}
	if source.compile != target.compile {
		decision.compileMiss = true
		if !compileCovered {
			decision.missVotes++
		}
	}
	if source.runtimeFallback != target.runtimeFallback {
		decision.runtimeMiss = true
		decision.missVotes++
	} else if !source.runtimeFallback && source.runtime != target.runtime {
		decision.runtimeMiss = true
		decision.missVotes++
	}
	return decision
}

func (decision remoteWorkloadInputVoteDecision) allowReuse() bool {
	return decision.missVotes > 0 && decision.missVotes < remoteReuseMissConfirmationThreshold
}

// remoteWorkloadInputVotesAllowReuse 在 broad 摘要已判 MISS 的前提下交叉声明、包编译和运行时算法；
// 只有至少两种算法判 MISS 才拒绝复用，单个宽泛变化不得扩大远程分片。
func remoteWorkloadInputVotesAllowReuse(source, target remoteWorkloadInputVoteDigests) bool {
	return remoteWorkloadInputVoteDecisionFor(source, target).allowReuse()
}

// previousGroupedWorkerDigest 对同一来源 tree 只重建一次 ValueSpec
// 收窄前的精确摘要；该值只用于验证历史环境。
func (cache *remoteReplayCache) previousGroupedWorkerDigest(ctx context.Context, snapshot *remoteGitTreeSnapshot) (string, error) {
	if cache == nil {
		return "", errors.New("remote workload PASS replay cache is required")
	}
	key, err := remoteReplaySnapshotKey(snapshot)
	if err != nil {
		return "", err
	}
	if digest := cache.previousGroupedWorkerDigests[key]; digest != "" {
		return digest, nil
	}
	cache.previousGroupedComputations++
	digest, err := snapshot.workerExecutionContractDigestPreviousGroupedDeclarationV4(ctx)
	if err != nil {
		return "", err
	}
	return cache.rememberWorkerDigest(cache.previousGroupedWorkerDigests, key, digest)
}

// previousStableWorkerDigest 对同一来源 tree 只计算一次收窄根集合前的 stable-key 摘要。
func (cache *remoteReplayCache) previousStableWorkerDigest(ctx context.Context, snapshot *remoteGitTreeSnapshot) (string, error) {
	if cache == nil {
		return "", errors.New("remote workload PASS replay cache is required")
	}
	key, err := remoteReplaySnapshotKey(snapshot)
	if err != nil {
		return "", err
	}
	if digest := cache.previousStableWorkerDigests[key]; digest != "" {
		return digest, nil
	}
	cache.previousStableComputations++
	digest, err := snapshot.workerExecutionContractDigestPreviousStableV4(ctx)
	if err != nil {
		return "", err
	}
	return cache.rememberWorkerDigest(cache.previousStableWorkerDigests, key, digest)
}

// previousWorkerDigest 保证同一来源 tree 的旧位置键精确摘要在一次 Prepare 中只计算一次。
func (cache *remoteReplayCache) previousWorkerDigest(ctx context.Context, snapshot *remoteGitTreeSnapshot) (string, error) {
	if cache == nil {
		return "", errors.New("remote workload PASS replay cache is required")
	}
	key, err := remoteReplaySnapshotKey(snapshot)
	if err != nil {
		return "", err
	}
	if digest := cache.previousWorkerDigests[key]; digest != "" {
		return digest, nil
	}
	cache.previousComputations++
	digest, err := snapshot.workerExecutionContractDigestPreviousPreciseV4(ctx)
	if err != nil {
		return "", err
	}
	return cache.rememberWorkerDigest(cache.previousWorkerDigests, key, digest)
}

// releaseSnapshotsExcept 释放可由 Git tree 重建的来源快照，只保留当前目标树；
// 已计算的摘要缓存继续有效，不改变后续候选判定。
func (cache *remoteReplayCache) releaseSnapshotsExcept(repositoryRoot, retainedTree string) {
	if cache == nil {
		return
	}
	for key := range cache.snapshots {
		if key.repositoryRoot != repositoryRoot || key.tree == retainedTree {
			continue
		}
		delete(cache.snapshots, key)
	}
}

// releaseSnapshot 在一个来源树分区完成后释放其大对象；目标树始终保留。
func (cache *remoteReplayCache) releaseSnapshot(repositoryRoot, tree, retainedTree string) {
	if cache == nil || tree == retainedTree {
		return
	}
	delete(cache.snapshots, remoteReplayTreeKey{repositoryRoot: repositoryRoot, tree: tree})
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

// prewarmInputDigests 将同一来源树的 exact selector 并行计算，并按输入顺序
// 串行写回 replay cache；非 selector 保持串行，错误顺序不变。
func (cache *remoteReplayCache) prewarmInputDigests(ctx context.Context, repositoryRoot, tree string, workloads []gate.Workload) error {
	if cache == nil {
		return errors.New("remote workload PASS replay cache is required")
	}
	snapshot, available, err := cache.snapshot(ctx, repositoryRoot, tree)
	if err != nil {
		return err
	}
	pending, keys := cache.pendingInputDigests(repositoryRoot, tree, workloads, available)
	results := computeRemoteWorkloadInputDigests(ctx, snapshot, pending)
	return cache.rememberPrewarmedInputDigests(pending, keys, results)
}

// pendingInputDigests 过滤已缓存 workload，并为不可用来源树记录稳定 unavailable 事实。
func (cache *remoteReplayCache) pendingInputDigests(
	repositoryRoot string,
	tree string,
	workloads []gate.Workload,
	available bool,
) ([]gate.Workload, []remoteReplayWorkloadKey) {
	pending := make([]gate.Workload, 0, len(workloads))
	keys := make([]remoteReplayWorkloadKey, 0, len(workloads))
	for _, workload := range workloads {
		key := remoteReplayWorkloadKey{tree: remoteReplayTreeKey{repositoryRoot: repositoryRoot, tree: tree}, workloadID: workload.ID}
		if _, cached := cache.inputDigests[key]; cached {
			continue
		}
		if !available {
			cache.inputDigests[key] = remoteReplayInputDigestResult{}
			continue
		}
		pending = append(pending, workload)
		keys = append(keys, key)
	}
	return pending, keys
}

// computeRemoteWorkloadInputDigests 按既有 selector 分组策略计算一批 workload 输入。
func computeRemoteWorkloadInputDigests(
	ctx context.Context,
	snapshot *remoteGitTreeSnapshot,
	pending []gate.Workload,
) []remoteWorkloadInputDigestResult {
	results := make([]remoteWorkloadInputDigestResult, len(pending))
	parallel := make([]int, 0, len(pending))
	for index, workload := range pending {
		if remoteExactGoSelectorWorkload(workload) {
			parallel = append(parallel, index)
			continue
		}
		results[index].digest, results[index].err = snapshot.workloadInputDigest(ctx, workload)
	}
	snapshot.runRemoteWorkloadInputDigestWorkers(ctx, pending, parallel, results)
	return results
}

// rememberPrewarmedInputDigests 按输入顺序提交批量结果，保持最早错误和 unavailable 语义。
func (cache *remoteReplayCache) rememberPrewarmedInputDigests(
	pending []gate.Workload,
	keys []remoteReplayWorkloadKey,
	results []remoteWorkloadInputDigestResult,
) error {
	for index, result := range results {
		cache.inputComputations++
		if errors.Is(result.err, errRemoteWorkloadInputUnavailable) {
			cache.inputDigests[keys[index]] = remoteReplayInputDigestResult{}
			continue
		}
		if result.err != nil {
			return fmt.Errorf("prewarm remote workload PASS input %q: %w", pending[index].ID, result.err)
		}
		if result.digest == "" {
			return fmt.Errorf("prewarm remote workload PASS input %q produced an empty digest", pending[index].ID)
		}
		cache.inputDigests[keys[index]] = remoteReplayInputDigestResult{digest: result.digest, available: true}
	}
	return nil
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
