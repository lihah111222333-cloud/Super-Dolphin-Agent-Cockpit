package remoteci

import (
	"context"
	"fmt"
	"sort"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// replayRemoteWorkloadPassMisses 只为 exact identity MISS 枚举历史直接 PASS，
// 再从来源内容重算当前 workload 指纹；tree/commit/worktree 从不进入目标 identity。
func replayRemoteWorkloadPassMisses(
	ctx context.Context,
	input RunInput,
	catalog gate.WorkloadCatalog,
	identities []gate.WorkloadPassIdentity,
	reused map[string]gate.WorkloadPassEvidence,
	compileCoverage remoteReplayCompileCoverage,
	cache *remoteReplayCache,
	confirmations remoteReuseMissConfirmations,
	diagnostic *ReuseReplayDiagnostic,
	observe remoteWorkloadReuseProgress,
) error {
	missing := missingRemoteWorkloadPassIdentities(identities, reused)
	if len(missing) == 0 {
		return nil
	}
	observe.phase("reuse_source_candidate_query_started")
	candidates, err := input.LedgerStore.LookupWorkloadPassSourceReplayCandidates(missing)
	if err != nil {
		return err
	}
	recordRemoteSourceReplayCandidates(diagnostic, candidates)
	observe.phase("reuse_source_candidate_query_completed")
	workloads, err := remoteReplayWorkloadIndex(catalog)
	if err != nil {
		return err
	}
	observe.phase("reuse_source_rank_started")
	treeRanks, err := rankRemoteWorkloadPassSourceTrees(ctx, input.RepositoryRoot, input.Tree, candidates, cache)
	if err != nil {
		return err
	}
	observe.phase("reuse_source_rank_completed")
	observe.phase("reuse_source_input_cache_started")
	if err := preloadRemoteWorkloadPassSourceInputs(ctx, input.RepositoryRoot, input.Tree, candidates, workloads, cache); err != nil {
		return err
	}
	observe.phase("reuse_source_input_cache_completed")
	observe.phase("reuse_source_vote_started")
	for _, identity := range missing {
		workload, ok := workloads[identity.WorkloadID]
		if !ok {
			return fmt.Errorf("remote workload PASS source replay %q is absent from current catalog", identity.WorkloadID)
		}
		if err := replayRemoteWorkloadPassMiss(ctx, input.RepositoryRoot, input.Tree, identity, workload, candidates[identity.WorkloadID], treeRanks, reused, compileCoverage, cache, confirmations, diagnostic); err != nil {
			return err
		}
	}
	observe.phase("reuse_source_vote_completed")
	return nil
}

// preloadRemoteWorkloadPassSourceInputs 按 immutable 来源树批量读取当前 producer
// 的派生输入索引；缺失键仍由后续 best-first 候选裁决按需重算。
func preloadRemoteWorkloadPassSourceInputs(ctx context.Context, repositoryRoot, targetTree string, candidates map[gate.GateID][]gate.WorkloadPassEvidence, workloads map[gate.GateID]gate.Workload, cache *remoteReplayCache) error {
	target, available, err := cache.snapshot(ctx, repositoryRoot, targetTree)
	if err != nil || !available {
		return err
	}
	partitions := remoteWorkloadPassSourceInputPartitions(candidates, workloads)
	trees := make([]string, 0, len(partitions))
	for tree := range partitions {
		trees = append(trees, tree)
	}
	sort.Strings(trees)
	for _, tree := range trees {
		source, sourceAvailable, err := cache.snapshot(ctx, repositoryRoot, tree)
		if err != nil {
			return err
		}
		if !sourceAvailable {
			continue
		}
		compatible, err := cache.inputAlgorithmsCompatible(source, target)
		if err != nil {
			return err
		}
		if !compatible {
			if err := cache.preloadPersistentInputDigests(source, partitions[tree]); err != nil {
				return err
			}
		}
	}
	return nil
}

func remoteWorkloadPassSourceInputPartitions(candidates map[gate.GateID][]gate.WorkloadPassEvidence, workloads map[gate.GateID]gate.Workload) map[string][]gate.Workload {
	partitions := make(map[string][]gate.Workload)
	seen := make(map[string]map[gate.GateID]struct{})
	for workloadID, workloadCandidates := range candidates {
		workload, ok := workloads[workloadID]
		if !ok {
			continue
		}
		for _, candidate := range canonicalRemoteWorkloadPassSourceCandidates(workloadCandidates) {
			if seen[candidate.OriginSourceTreeSHA] == nil {
				seen[candidate.OriginSourceTreeSHA] = make(map[gate.GateID]struct{})
			}
			if _, duplicate := seen[candidate.OriginSourceTreeSHA][workloadID]; duplicate {
				continue
			}
			seen[candidate.OriginSourceTreeSHA][workloadID] = struct{}{}
			partitions[candidate.OriginSourceTreeSHA] = append(partitions[candidate.OriginSourceTreeSHA], workload)
		}
	}
	return partitions
}

// replayRemoteWorkloadPassMiss 裁决一个 direct MISS，并让首个未复用 selector
// 承担其 compile owner 的 fresh 编译义务。
func replayRemoteWorkloadPassMiss(ctx context.Context, repositoryRoot, targetTree string, identity gate.WorkloadPassIdentity, workload gate.Workload, candidates []gate.WorkloadPassEvidence, treeRanks map[string]int, reused map[string]gate.WorkloadPassEvidence, compileCoverage remoteReplayCompileCoverage, cache *remoteReplayCache, confirmations remoteReuseMissConfirmations, diagnostic *ReuseReplayDiagnostic) error {
	compileCovered, err := compileCoverage.covers(workload)
	if err != nil {
		return err
	}
	evidence, reusedPass, err := selectRemoteWorkloadPassReplay(ctx, repositoryRoot, targetTree, identity, workload, candidates, treeRanks, compileCovered, cache, diagnostic)
	if err != nil {
		return err
	}
	if reusedPass {
		reused[string(identity.WorkloadID)] = evidence
		return nil
	}
	covered, err := compileCoverage.cover(workload)
	if err != nil {
		return err
	}
	if covered {
		diagnostic.SourceCompileObligations++
	}
	confirmations.confirm(identity.WorkloadID, remoteReuseSourceMiss)
	return nil
}

func rankRemoteWorkloadPassSourceTrees(ctx context.Context, repositoryRoot, targetTree string, candidates map[gate.GateID][]gate.WorkloadPassEvidence, cache *remoteReplayCache) (map[string]int, error) {
	target, available, err := cache.snapshot(ctx, repositoryRoot, targetTree)
	if err != nil || !available {
		return nil, err
	}
	ranks := make(map[string]int)
	for _, workloadCandidates := range candidates {
		for _, candidate := range workloadCandidates {
			if _, ranked := ranks[candidate.OriginSourceTreeSHA]; ranked {
				continue
			}
			source, sourceAvailable, err := cache.snapshot(ctx, repositoryRoot, candidate.OriginSourceTreeSHA)
			if err != nil {
				return nil, err
			}
			ranks[candidate.OriginSourceTreeSHA] = remoteReplayTreeDistance(source, target)
			if !sourceAvailable {
				ranks[candidate.OriginSourceTreeSHA] = int(^uint(0) >> 1)
			}
		}
	}
	return ranks, nil
}

func recordRemoteSourceReplayCandidates(diagnostic *ReuseReplayDiagnostic, candidates map[gate.GateID][]gate.WorkloadPassEvidence) {
	trees := make(map[string]struct{})
	for _, workloadCandidates := range candidates {
		if len(workloadCandidates) > 0 {
			diagnostic.SourceCandidateWorkloads++
			diagnostic.SourceCandidates += len(workloadCandidates)
		}
		for _, candidate := range workloadCandidates {
			trees[candidate.OriginSourceTreeSHA] = struct{}{}
		}
	}
	diagnostic.SourceCandidateTrees = len(trees)
}

func missingRemoteWorkloadPassIdentities(identities []gate.WorkloadPassIdentity, reused map[string]gate.WorkloadPassEvidence) []gate.WorkloadPassIdentity {
	missing := make([]gate.WorkloadPassIdentity, 0, len(identities)-len(reused))
	for _, identity := range identities {
		if _, ok := reused[string(identity.WorkloadID)]; !ok {
			missing = append(missing, identity)
		}
	}
	return missing
}

func remoteReplayWorkloadIndex(catalog gate.WorkloadCatalog) (map[gate.GateID]gate.Workload, error) {
	indexed := make(map[gate.GateID]gate.Workload)
	for _, workload := range remoteShardableWorkloads(catalog) {
		workloadID := gate.GateID(workload.ID)
		if _, duplicate := indexed[workloadID]; duplicate {
			return nil, fmt.Errorf("remote workload PASS source replay catalog %q is duplicated", workloadID)
		}
		indexed[workloadID] = workload
	}
	return indexed, nil
}

// canonicalRemoteWorkloadPassSourceCandidates 按来源树去重已验证候选，
// 保留 SQLite 规范排序中的首个 provenance，避免同树历史证据重复计算。
func canonicalRemoteWorkloadPassSourceCandidates(candidates []gate.WorkloadPassEvidence) []gate.WorkloadPassEvidence {
	canonical := make([]gate.WorkloadPassEvidence, 0, len(candidates))
	seenTrees := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, duplicated := seenTrees[candidate.OriginSourceTreeSHA]; duplicated {
			continue
		}
		seenTrees[candidate.OriginSourceTreeSHA] = struct{}{}
		canonical = append(canonical, candidate)
	}
	return canonical
}

func rankedRemoteWorkloadPassSourceCandidates(candidates []gate.WorkloadPassEvidence, treeRanks map[string]int) []gate.WorkloadPassEvidence {
	canonical := canonicalRemoteWorkloadPassSourceCandidates(candidates)
	sort.SliceStable(canonical, func(left, right int) bool {
		return treeRanks[canonical[left].OriginSourceTreeSHA] < treeRanks[canonical[right].OriginSourceTreeSHA]
	})
	return canonical
}

// selectRemoteWorkloadPassReplay 按确定顺序重算来源树，首个精确 input digest 命中才返回直接证据。
func selectRemoteWorkloadPassReplay(
	ctx context.Context,
	repositoryRoot string,
	targetTree string,
	identity gate.WorkloadPassIdentity,
	workload gate.Workload,
	candidates []gate.WorkloadPassEvidence,
	treeRanks map[string]int,
	compileCovered bool,
	cache *remoteReplayCache,
	diagnostic *ReuseReplayDiagnostic,
) (gate.WorkloadPassEvidence, bool, error) {
	target, available, err := cache.snapshot(ctx, repositoryRoot, targetTree)
	if err != nil || !available {
		return gate.WorkloadPassEvidence{}, false, err
	}
	for _, candidate := range rankedRemoteWorkloadPassSourceCandidates(candidates, treeRanks) {
		diagnostic.SourceCandidateEvaluations++
		matches, err := matchesRemoteWorkloadPassSourceCandidate(ctx, repositoryRoot, identity, workload, candidate, target, compileCovered, cache, diagnostic)
		if err != nil {
			return gate.WorkloadPassEvidence{}, false, err
		}
		if !matches {
			continue
		}
		if _, err := gate.WorkloadPassSourceReplaySHA256(identity, candidate); err != nil {
			return gate.WorkloadPassEvidence{}, false, err
		}
		return candidate, true, nil
	}
	return gate.WorkloadPassEvidence{}, false, nil
}

// remoteWorkloadPassSourceCompileMismatch 加载权威来源树并按包比较编译闭包。
func remoteWorkloadPassSourceCompileMismatch(ctx context.Context, repositoryRoot string, workload gate.Workload, candidate gate.WorkloadPassEvidence, target *remoteGitTreeSnapshot, compileCovered bool, cache *remoteReplayCache, diagnostic *ReuseReplayDiagnostic) (*remoteGitTreeSnapshot, bool, error) {
	source, sourceAvailable, err := cache.snapshot(ctx, repositoryRoot, candidate.OriginSourceTreeSHA)
	if err != nil || !sourceAvailable {
		return nil, false, err
	}
	decision, supported, err := cache.compileInputDecision(ctx, workload, source, target)
	if err != nil {
		return nil, false, err
	}
	if supported && decision.compileMiss && !compileCovered {
		diagnostic.observeSourceInputVoteDecision(decision)
		return source, true, nil
	}
	return source, false, nil
}

// matchesRemoteWorkloadPassSourceCandidate 从权威来源树重算当前 broad 摘要并交叉 selector 语义；
// 历史 evidence 的旧摘要算法不替代当前重算，也不在进入多票裁决前制造假失败。
func matchesRemoteWorkloadPassSourceCandidate(ctx context.Context, repositoryRoot string, identity gate.WorkloadPassIdentity, workload gate.Workload, candidate gate.WorkloadPassEvidence, target *remoteGitTreeSnapshot, compileCovered bool, cache *remoteReplayCache, diagnostic *ReuseReplayDiagnostic) (bool, error) {
	source, compileMismatch, err := remoteWorkloadPassSourceCompileMismatch(ctx, repositoryRoot, workload, candidate, target, compileCovered, cache, diagnostic)
	if err != nil {
		return false, err
	}
	if source == nil {
		diagnostic.SourceInputUnavailable++
		return false, nil
	}
	if compileMismatch {
		diagnostic.SourceInputMismatch++
		return false, nil
	}
	digest := candidate.Identity.InputDigest
	compatible, err := cache.inputAlgorithmsCompatible(source, target)
	if err != nil {
		return false, err
	}
	if !compatible {
		var available bool
		digest, available, err = cache.inputDigest(ctx, repositoryRoot, candidate.OriginSourceTreeSHA, workload)
		if err != nil {
			return false, fmt.Errorf("recompute remote workload PASS source %q: %w", identity.WorkloadID, err)
		}
		if !available {
			diagnostic.SourceInputUnavailable++
			return false, nil
		}
	} else {
		diagnostic.SourceAlgorithmCompatibleRecoveries++
	}
	if digest == identity.InputDigest {
		return true, nil
	}
	decision, err := cache.semanticInputDecisionWithCompileCoverage(ctx, workload, source, target, compileCovered)
	matches := decision.allowReuse()
	recordRemoteSourceSemanticDecision(diagnostic, decision, compileCovered, matches, err)
	return matches, err
}

// recordRemoteSourceSemanticDecision 同时记录 selector 投票与 compile obligation 恢复结果。
func recordRemoteSourceSemanticDecision(diagnostic *ReuseReplayDiagnostic, decision remoteWorkloadInputVoteDecision, compileCovered, matches bool, decisionErr error) {
	diagnostic.observeSourceInputVoteDecision(decision)
	if matches && compileCovered && decision.compileMiss {
		diagnostic.SourceCompileCoveredRecoveries++
	}
	if decisionErr == nil && !matches {
		diagnostic.SourceInputMismatch++
	}
}

type remoteReplayCompileCoverage map[string]gate.GateID

// covers 判断当前 compile owner 是否已有一个确定 source MISS 将进入 fresh execution。
func (coverage remoteReplayCompileCoverage) covers(workload gate.Workload) (bool, error) {
	key, supported, err := remoteReplayCompileCoverageKey(workload)
	if err != nil || !supported {
		return false, err
	}
	_, covered := coverage[key]
	return covered, nil
}

// owns 判断 workload 是否是当前 compile owner 的唯一 fresh 执行者。
func (coverage remoteReplayCompileCoverage) owns(workload gate.Workload) (bool, error) {
	key, supported, err := remoteReplayCompileCoverageKey(workload)
	if err != nil || !supported {
		return false, err
	}
	return coverage[key] == gate.GateID(workload.ID), nil
}

// cover 用当前确定 MISS 覆盖同 package+semantic 的精确树编译义务。
func (coverage remoteReplayCompileCoverage) cover(workload gate.Workload) (bool, error) {
	key, supported, err := remoteReplayCompileCoverageKey(workload)
	if err != nil || !supported {
		return false, err
	}
	if _, covered := coverage[key]; covered {
		return false, nil
	}
	coverage[key] = gate.GateID(workload.ID)
	return true, nil
}

// remoteReplayCompileCoverageKey 复用 compile-group owner 身份，避免 normal、race
// 和 benchmark 之间互相借用编译义务。
func remoteReplayCompileCoverageKey(workload gate.Workload) (string, bool, error) {
	target, _, supported, err := remoteGoWorkloadInputTarget(workload)
	if err != nil || !supported {
		return "", supported, err
	}
	semantic, err := gate.CompileGroupSemanticKeyForWorkloadID(gate.GateID(workload.ID))
	if err != nil {
		return "", false, err
	}
	return gate.CompileOwnerKey(target.Package, semantic), true, nil
}

// observeSourceInputVoteDecision 聚合独立 MISS 算法的票型，不记录 workload 或摘要。
func (diagnostic *ReuseReplayDiagnostic) observeSourceInputVoteDecision(decision remoteWorkloadInputVoteDecision) {
	if diagnostic == nil || decision.missVotes == 0 {
		return
	}
	if decision.allowReuse() {
		diagnostic.SourceSingleVoteRecovered++
	} else {
		diagnostic.SourceConfirmedMisses++
	}
	if decision.declarationMiss {
		diagnostic.SourceDeclarationMissVotes++
	}
	if decision.runtimeMiss {
		diagnostic.SourceRuntimeMissVotes++
	}
	if decision.compileMiss {
		diagnostic.SourceCompileMissVotes++
	}
}
