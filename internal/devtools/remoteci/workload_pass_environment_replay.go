package remoteci

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// replayRemoteWorkloadPassEnvironment migrates only current-generation PASS
// evidence whose legacy and precise worker closures are both proven against
// source/target trees. It never ignores an EnvironmentDigest mismatch.
// 仅在来源树和当前树的执行、环境及输入均可重算证明时生成复用证据。
func replayRemoteWorkloadPassEnvironment(
	ctx context.Context,
	input RunInput,
	catalog gate.WorkloadCatalog,
	identities []gate.WorkloadPassIdentity,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	reused map[string]gate.WorkloadPassEvidence,
	proofs map[string]string,
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
	observe.phase("reuse_environment_hint_query_started")
	hints, err := input.LedgerStore.LookupWorkloadPassEnvironmentReplayHints(missing)
	if err != nil {
		return err
	}
	recordRemoteEnvironmentReplayHints(diagnostic, hints)
	observe.phase("reuse_environment_hint_query_completed")
	workloads, err := remoteReplayWorkloadIndex(catalog)
	if err != nil {
		return err
	}
	observe.phase("reuse_environment_filter_started")
	candidates, target, err := filterRemoteWorkloadPassEnvironmentCandidates(
		ctx, input, missing, workloads, hints, workerTimeout, resourcePolicy, cache, diagnostic,
	)
	if err != nil {
		return err
	}
	observe.phase("reuse_environment_filter_completed")
	observe.phase("reuse_environment_tree_partitions_started")
	selectedIdentities, selectedHints, selected, err := selectRemoteWorkloadPassEnvironmentReplayHints(
		ctx, input, missing, workloads, candidates, target, compileCoverage, cache, diagnostic, observe,
	)
	if err != nil {
		return err
	}
	for _, identity := range missing {
		if _, ok := selected[identity.WorkloadID]; !ok {
			confirmations.confirm(identity.WorkloadID, remoteReuseEnvironmentMiss)
		}
	}
	observe.phase("reuse_environment_tree_partitions_completed")
	observe.phase("reuse_environment_authorization_started")
	err = authorizeRemoteWorkloadPassEnvironmentHints(input, selectedIdentities, selectedHints, reused, proofs)
	observe.phase("reuse_environment_authorization_completed")
	return err
}

func recordRemoteEnvironmentReplayHints(diagnostic *ReuseReplayDiagnostic, hints map[gate.GateID][]gate.WorkloadPassEnvironmentReplayHint) {
	for _, workloadHints := range hints {
		if len(workloadHints) > 0 {
			diagnostic.EnvironmentHintWorkloads++
			diagnostic.EnvironmentHints += len(workloadHints)
		}
	}
}

type remoteWorkloadPassEnvironmentCandidate struct {
	hint gate.WorkloadPassEnvironmentReplayHint
	tree string
}

func filterRemoteWorkloadPassEnvironmentCandidates(
	ctx context.Context,
	input RunInput,
	identities []gate.WorkloadPassIdentity,
	workloads map[gate.GateID]gate.Workload,
	hints map[gate.GateID][]gate.WorkloadPassEnvironmentReplayHint,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
	diagnostic *ReuseReplayDiagnostic,
) (map[gate.GateID][]remoteWorkloadPassEnvironmentCandidate, *remoteGitTreeSnapshot, error) {
	target, available, err := cache.snapshot(ctx, input.RepositoryRoot, input.Tree)
	if err != nil || !available {
		if err == nil {
			diagnostic.EnvironmentTargetUnavailable++
		}
		return nil, nil, err
	}
	filtered := make(map[gate.GateID][]remoteWorkloadPassEnvironmentCandidate)
	for _, identity := range identities {
		prepared, err := filterRemoteWorkloadPassEnvironmentIdentity(
			ctx, input, identity, workloads[identity.WorkloadID], hints[identity.WorkloadID],
			target, workerTimeout, resourcePolicy, cache, diagnostic,
		)
		if err != nil {
			return nil, nil, err
		}
		if len(prepared) > 0 {
			filtered[identity.WorkloadID] = prepared
		}
	}
	cache.releaseSnapshotsExcept(input.RepositoryRoot, input.Tree)
	return filtered, target, nil
}

// filterRemoteWorkloadPassEnvironmentIdentity 过滤一个 workload 的历史环境候选，
// 保持 SQLite hint 顺序并只缓存树级环境摘要。
func filterRemoteWorkloadPassEnvironmentIdentity(
	ctx context.Context,
	input RunInput,
	identity gate.WorkloadPassIdentity,
	workload gate.Workload,
	hints []gate.WorkloadPassEnvironmentReplayHint,
	target *remoteGitTreeSnapshot,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
	diagnostic *ReuseReplayDiagnostic,
) ([]remoteWorkloadPassEnvironmentCandidate, error) {
	if workload.ID == "" {
		return nil, fmt.Errorf("remote workload PASS environment replay %q is absent from current catalog", identity.WorkloadID)
	}
	goFlags, err := remoteWorkloadGoFlags(string(identity.WorkloadID))
	if err != nil {
		return nil, err
	}
	filtered := make([]remoteWorkloadPassEnvironmentCandidate, 0, len(hints))
	for _, hint := range hints {
		candidate := hint.UntrustedCandidate()
		if candidate.OriginAcceptedGeneration != input.AcceptedGeneration {
			diagnostic.EnvironmentGenerationMismatch++
			continue
		}
		source, available, err := cache.snapshot(ctx, input.RepositoryRoot, candidate.OriginSourceTreeSHA)
		if err != nil {
			return nil, err
		}
		if !available {
			diagnostic.EnvironmentSourceUnavailable++
			continue
		}
		matches, err := verifyRemoteWorkloadPassEnvironmentOnly(
			ctx, input, identity, candidate, source, target, goFlags, workerTimeout, resourcePolicy, cache, diagnostic,
		)
		if err != nil {
			return nil, err
		}
		if matches {
			filtered = append(filtered, remoteWorkloadPassEnvironmentCandidate{hint: hint, tree: candidate.OriginSourceTreeSHA})
		}
	}
	return filtered, nil
}

type remoteWorkloadPassEnvironmentCandidateKey struct {
	workloadID gate.GateID
	index      int
}

// selectRemoteWorkloadPassEnvironmentReplayHints 先验证每个 workload 的首选候选，
// 再只为首选未命中的 workload 扫描剩余候选。每一阶段按来源树分区，既保持
// 原候选优先级，又避免命中 workload 的低优先级历史输入重算。
func selectRemoteWorkloadPassEnvironmentReplayHints(
	ctx context.Context,
	input RunInput,
	identities []gate.WorkloadPassIdentity,
	workloads map[gate.GateID]gate.Workload,
	candidates map[gate.GateID][]remoteWorkloadPassEnvironmentCandidate,
	target *remoteGitTreeSnapshot,
	compileCoverage remoteReplayCompileCoverage,
	cache *remoteReplayCache,
	diagnostic *ReuseReplayDiagnostic,
	observe remoteWorkloadReuseProgress,
) ([]gate.WorkloadPassIdentity, []gate.WorkloadPassEnvironmentReplayHint, map[gate.GateID]struct{}, error) {
	identityByWorkload, firstKeys := preferredRemoteWorkloadPassEnvironmentCandidateKeys(identities, candidates)
	observe.phase("reuse_environment_preferred_partition_started")
	matchedCandidates, err := evaluateRemoteWorkloadPassEnvironmentCandidateKeys(
		ctx, input, identityByWorkload, workloads, candidates, firstKeys, target, compileCoverage, cache, diagnostic,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	observe.phase("reuse_environment_preferred_partition_completed")
	remainingKeys := remainingRemoteWorkloadPassEnvironmentCandidateKeys(identities, candidates, matchedCandidates)
	observe.phase("reuse_environment_remaining_partition_started")
	remainingMatches, err := evaluateRemoteWorkloadPassEnvironmentCandidateKeys(
		ctx, input, identityByWorkload, workloads, candidates, remainingKeys, target, compileCoverage, cache, diagnostic,
	)
	if err != nil {
		return nil, nil, nil, err
	}
	observe.phase("reuse_environment_remaining_partition_completed")
	for key := range remainingMatches {
		matchedCandidates[key] = struct{}{}
	}
	return projectMatchedRemoteWorkloadPassEnvironmentCandidates(identities, candidates, matchedCandidates)
}

// preferredRemoteWorkloadPassEnvironmentCandidateKeys 建立 workload identity 索引，
// 并按 catalog 顺序生成每个 workload 的首选候选 key。
func preferredRemoteWorkloadPassEnvironmentCandidateKeys(
	identities []gate.WorkloadPassIdentity,
	candidates map[gate.GateID][]remoteWorkloadPassEnvironmentCandidate,
) (map[gate.GateID]gate.WorkloadPassIdentity, []remoteWorkloadPassEnvironmentCandidateKey) {
	indexed := make(map[gate.GateID]gate.WorkloadPassIdentity, len(identities))
	preferred := make([]remoteWorkloadPassEnvironmentCandidateKey, 0, len(identities))
	for _, identity := range identities {
		indexed[identity.WorkloadID] = identity
		if len(candidates[identity.WorkloadID]) > 0 {
			preferred = append(preferred, remoteWorkloadPassEnvironmentCandidateKey{workloadID: identity.WorkloadID})
		}
	}
	return indexed, preferred
}

// projectMatchedRemoteWorkloadPassEnvironmentCandidates 按原候选顺序选择首个命中，
// 再按 catalog 顺序投影授权输入。
func projectMatchedRemoteWorkloadPassEnvironmentCandidates(
	identities []gate.WorkloadPassIdentity,
	candidates map[gate.GateID][]remoteWorkloadPassEnvironmentCandidate,
	matched map[remoteWorkloadPassEnvironmentCandidateKey]struct{},
) ([]gate.WorkloadPassIdentity, []gate.WorkloadPassEnvironmentReplayHint, map[gate.GateID]struct{}, error) {
	selected := make(map[gate.GateID]gate.WorkloadPassEnvironmentReplayHint)
	for _, identity := range identities {
		for index, candidate := range candidates[identity.WorkloadID] {
			key := remoteWorkloadPassEnvironmentCandidateKey{workloadID: identity.WorkloadID, index: index}
			if _, ok := matched[key]; ok {
				selected[identity.WorkloadID] = candidate.hint
				break
			}
		}
	}
	selectedSet := make(map[gate.GateID]struct{}, len(selected))
	selectedIdentities := make([]gate.WorkloadPassIdentity, 0, len(selected))
	selectedHints := make([]gate.WorkloadPassEnvironmentReplayHint, 0, len(selected))
	for _, identity := range identities {
		hint, ok := selected[identity.WorkloadID]
		if !ok {
			continue
		}
		selectedSet[identity.WorkloadID] = struct{}{}
		selectedIdentities = append(selectedIdentities, identity)
		selectedHints = append(selectedHints, hint)
	}
	return selectedIdentities, selectedHints, selectedSet, nil
}

// remainingRemoteWorkloadPassEnvironmentCandidateKeys 只保留首选未命中 workload
// 的后续候选，并保持 identity 与候选的原始顺序。
func remainingRemoteWorkloadPassEnvironmentCandidateKeys(
	identities []gate.WorkloadPassIdentity,
	candidates map[gate.GateID][]remoteWorkloadPassEnvironmentCandidate,
	preferredMatches map[remoteWorkloadPassEnvironmentCandidateKey]struct{},
) []remoteWorkloadPassEnvironmentCandidateKey {
	remaining := make([]remoteWorkloadPassEnvironmentCandidateKey, 0)
	for _, identity := range identities {
		preferred := remoteWorkloadPassEnvironmentCandidateKey{workloadID: identity.WorkloadID}
		if _, matched := preferredMatches[preferred]; matched {
			continue
		}
		for index := 1; index < len(candidates[identity.WorkloadID]); index++ {
			remaining = append(remaining, remoteWorkloadPassEnvironmentCandidateKey{workloadID: identity.WorkloadID, index: index})
		}
	}
	return remaining
}

// evaluateRemoteWorkloadPassEnvironmentCandidateKeys 在一个 best-first 阶段内
// 将候选按来源树分区；同一树的 exact 输入批量计算，完成后立即释放快照。
func evaluateRemoteWorkloadPassEnvironmentCandidateKeys(
	ctx context.Context,
	input RunInput,
	identities map[gate.GateID]gate.WorkloadPassIdentity,
	workloads map[gate.GateID]gate.Workload,
	candidates map[gate.GateID][]remoteWorkloadPassEnvironmentCandidate,
	keys []remoteWorkloadPassEnvironmentCandidateKey,
	target *remoteGitTreeSnapshot,
	compileCoverage remoteReplayCompileCoverage,
	cache *remoteReplayCache,
	diagnostic *ReuseReplayDiagnostic,
) (map[remoteWorkloadPassEnvironmentCandidateKey]struct{}, error) {
	byTree := make(map[string][]remoteWorkloadPassEnvironmentCandidateKey)
	for _, key := range keys {
		owner, err := compileCoverage.owns(workloads[key.workloadID])
		if err != nil {
			return nil, err
		}
		if owner {
			if key.index == 0 && diagnostic != nil {
				diagnostic.EnvironmentCompileOwners++
			}
			continue
		}
		candidate := candidates[key.workloadID][key.index]
		byTree[candidate.tree] = append(byTree[candidate.tree], key)
	}
	trees := make([]string, 0, len(byTree))
	for tree := range byTree {
		trees = append(trees, tree)
	}
	sort.Strings(trees)
	matchedCandidates := make(map[remoteWorkloadPassEnvironmentCandidateKey]struct{})
	evaluation := remoteWorkloadPassEnvironmentEvaluation{
		ctx: ctx, input: input, identities: identities, workloads: workloads,
		candidates: candidates, target: target, compileCoverage: compileCoverage,
		cache: cache, diagnostic: diagnostic,
	}
	for _, tree := range trees {
		if err := evaluation.evaluateTree(tree, byTree[tree], matchedCandidates); err != nil {
			return nil, err
		}
	}
	return matchedCandidates, nil
}

type remoteWorkloadPassEnvironmentEvaluation struct {
	ctx             context.Context
	input           RunInput
	identities      map[gate.GateID]gate.WorkloadPassIdentity
	workloads       map[gate.GateID]gate.Workload
	candidates      map[gate.GateID][]remoteWorkloadPassEnvironmentCandidate
	target          *remoteGitTreeSnapshot
	compileCoverage remoteReplayCompileCoverage
	cache           *remoteReplayCache
	diagnostic      *ReuseReplayDiagnostic
}

// sourceInputPartition 先用 selector 多票筛掉确定语义 MISS，再返回后续需要
// 验证历史 broad input 的去重 workload，供同树持久索引批量预热。
func (evaluation remoteWorkloadPassEnvironmentEvaluation) sourceInputPartition(
	source *remoteGitTreeSnapshot,
	keys []remoteWorkloadPassEnvironmentCandidateKey,
) ([]gate.Workload, error) {
	partition := make([]gate.Workload, 0, len(keys))
	seen := make(map[gate.GateID]struct{}, len(keys))
	for _, key := range keys {
		candidate := evaluation.candidates[key.workloadID][key.index].hint.UntrustedCandidate()
		if candidate.Identity.InputDigest != evaluation.identities[key.workloadID].InputDigest {
			compileCovered, err := evaluation.compileCoverage.covers(evaluation.workloads[key.workloadID])
			if err != nil {
				return nil, err
			}
			decision, err := evaluation.cache.semanticInputDecisionWithCompileCoverage(
				evaluation.ctx, evaluation.workloads[key.workloadID], source, evaluation.target, compileCovered,
			)
			if err != nil {
				return nil, err
			}
			if !decision.allowReuse() {
				continue
			}
		}
		if _, ok := seen[key.workloadID]; ok {
			continue
		}
		seen[key.workloadID] = struct{}{}
		partition = append(partition, evaluation.workloads[key.workloadID])
	}
	return partition, nil
}

// evaluateTree 在单个来源树中批量预热 exact 输入、按候选顺序投票并立即释放快照。
func (evaluation remoteWorkloadPassEnvironmentEvaluation) evaluateTree(
	tree string,
	keys []remoteWorkloadPassEnvironmentCandidateKey,
	matchedCandidates map[remoteWorkloadPassEnvironmentCandidateKey]struct{},
) error {
	source, available, err := evaluation.cache.snapshot(evaluation.ctx, evaluation.input.RepositoryRoot, tree)
	if err != nil {
		return err
	}
	if !available {
		return fmt.Errorf("remote workload PASS environment source tree %q disappeared after filtering", tree)
	}
	partition, err := evaluation.sourceInputPartition(source, keys)
	if err != nil {
		return err
	}
	compatible, err := prewarmRemoteWorkloadPassSourceInputs(
		evaluation.ctx, evaluation.input.RepositoryRoot, tree, partition,
		source, evaluation.target, evaluation.cache,
	)
	if err != nil {
		return err
	}
	if compatible {
		evaluation.diagnostic.EnvironmentAlgorithmCompatibleTrees++
		evaluation.diagnostic.EnvironmentInputPrewarmSkipped += len(partition)
	}
	for _, key := range keys {
		prepared := evaluation.candidates[key.workloadID][key.index]
		candidate := prepared.hint.UntrustedCandidate()
		compileCovered, err := evaluation.compileCoverage.covers(evaluation.workloads[key.workloadID])
		if err != nil {
			return err
		}
		matched, err := verifyRemoteWorkloadPassSourceInput(
			evaluation.ctx, evaluation.input, evaluation.identities[key.workloadID], candidate,
			evaluation.workloads[key.workloadID], source, evaluation.target, compileCovered,
			evaluation.cache, evaluation.diagnostic,
		)
		if err != nil {
			return err
		}
		if matched {
			matchedCandidates[key] = struct{}{}
		} else {
			evaluation.diagnostic.EnvironmentInputMismatch++
		}
	}
	evaluation.cache.releaseSnapshot(evaluation.input.RepositoryRoot, tree, evaluation.input.Tree)
	return nil
}

// prewarmRemoteWorkloadPassSourceInputs 先校验来源和目标的 input producer；
// 仅旧算法或闭包变化时才执行昂贵的来源树 workload input 批量重算。
func prewarmRemoteWorkloadPassSourceInputs(
	ctx context.Context,
	repositoryRoot string,
	tree string,
	workloads []gate.Workload,
	source *remoteGitTreeSnapshot,
	target *remoteGitTreeSnapshot,
	cache *remoteReplayCache,
) (bool, error) {
	compatible, err := cache.inputAlgorithmsCompatible(source, target)
	if err != nil || compatible {
		return compatible, err
	}
	if err := cache.preloadPersistentInputDigests(source, workloads); err != nil {
		return false, err
	}
	return false, cache.prewarmInputDigests(ctx, repositoryRoot, tree, workloads)
}

// authorizeRemoteWorkloadPassEnvironmentHints 一次精确重读全部语义命中项；任一
// authority 漂移都不得产生部分 reused/proof，也不得 fallback 到后续 hint。
func authorizeRemoteWorkloadPassEnvironmentHints(
	input RunInput,
	identities []gate.WorkloadPassIdentity,
	hints []gate.WorkloadPassEnvironmentReplayHint,
	reused map[string]gate.WorkloadPassEvidence,
	proofs map[string]string,
) error {
	validated, err := input.LedgerStore.ValidateWorkloadPassEnvironmentReplayHints(hints)
	if err != nil {
		return err
	}
	if len(validated) != len(identities) {
		return fmt.Errorf("validated workload PASS environment replay count = %d, want %d", len(validated), len(identities))
	}
	validatedProofs := make([]string, len(validated))
	for index, evidence := range validated {
		validatedProofs[index], err = gate.WorkloadPassEnvironmentReplaySHA256(identities[index], evidence)
		if err != nil {
			return err
		}
	}
	for index, evidence := range validated {
		workloadID := string(identities[index].WorkloadID)
		reused[workloadID] = evidence
		proofs[workloadID] = validatedProofs[index]
	}
	return nil
}

func verifyRemoteWorkloadPassEnvironmentOnly(
	ctx context.Context,
	input RunInput,
	identity gate.WorkloadPassIdentity,
	candidate gate.WorkloadPassEvidence,
	source *remoteGitTreeSnapshot,
	target *remoteGitTreeSnapshot,
	goFlags string,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
	diagnostic *ReuseReplayDiagnostic,
) (bool, error) {
	preciseMatches, err := verifyRemoteWorkloadPassPreciseEnvironment(ctx, input, identity, source, target, goFlags, workerTimeout, resourcePolicy, cache)
	if err != nil || !preciseMatches {
		if err == nil {
			diagnostic.EnvironmentCurrentWorkerMismatch++
		}
		return false, err
	}
	historicalMatches, err := verifyRemoteWorkloadPassHistoricalEnvironment(ctx, input, candidate, source, goFlags, workerTimeout, resourcePolicy, cache)
	if err != nil || !historicalMatches {
		if err == nil {
			diagnostic.EnvironmentHistoricalMismatch++
		}
		return false, err
	}
	return true, nil
}

// verifyRemoteWorkloadPassHistoricalEnvironment 只接受能由冻结旧版 worker 摘要重建的历史环境身份。
func verifyRemoteWorkloadPassHistoricalEnvironment(
	ctx context.Context,
	input RunInput,
	candidate gate.WorkloadPassEvidence,
	source *remoteGitTreeSnapshot,
	goFlags string,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
) (bool, error) {
	resolvers := []func(context.Context, *remoteGitTreeSnapshot) (string, error){
		cache.previousGroupedWorkerDigest,
		cache.legacyWorkerDigest,
		cache.previousWorkerDigest,
		cache.previousStableWorkerDigest,
	}
	for _, resolve := range resolvers {
		digest, err := resolve(ctx, source)
		if err != nil {
			return false, err
		}
		matches, err := remoteWorkloadPassEnvironmentMatches(
			input, candidate, digest, goFlags, workerTimeout, resourcePolicy, cache,
		)
		if err != nil || matches {
			return matches, err
		}
	}
	return false, nil
}

func remoteWorkloadPassEnvironmentMatches(
	input RunInput,
	candidate gate.WorkloadPassEvidence,
	workerDigest string,
	goFlags string,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
) (bool, error) {
	historicalInput := input
	historicalInput.WorkerExecutionSemanticDigest = workerDigest
	historicalEnvironment, err := cache.environmentDigest(historicalInput, workerTimeout, resourcePolicy, goFlags)
	if err != nil {
		return false, err
	}
	return candidate.Identity.EnvironmentDigest == historicalEnvironment, nil
}

// verifyRemoteWorkloadPassPreciseEnvironment 要求来源与目标的精确 Worker 摘要及当前环境完全一致。
func verifyRemoteWorkloadPassPreciseEnvironment(
	ctx context.Context,
	input RunInput,
	identity gate.WorkloadPassIdentity,
	source *remoteGitTreeSnapshot,
	target *remoteGitTreeSnapshot,
	goFlags string,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
) (bool, error) {
	preciseSourceDigest, available, err := remoteReplayPreciseSourceDigest(ctx, cache, source)
	if err != nil || !available {
		return false, err
	}
	preciseTargetDigest, err := cache.preciseWorkerDigest(ctx, target)
	if err != nil {
		return false, err
	}
	if preciseSourceDigest != preciseTargetDigest || preciseTargetDigest != input.WorkerExecutionSemanticDigest {
		return false, nil
	}
	currentEnvironment, err := cache.environmentDigest(input, workerTimeout, resourcePolicy, goFlags)
	if err != nil {
		return false, err
	}
	return currentEnvironment == identity.EnvironmentDigest, nil
}

// remoteReplayPreciseSourceDigest 将缺少新精确根的旧来源树视为该条候选不可证明；
// 当前目标树仍由调用方直接计算并保持 fail-fast。
func remoteReplayPreciseSourceDigest(ctx context.Context, cache *remoteReplayCache, source *remoteGitTreeSnapshot) (string, bool, error) {
	digest, err := cache.preciseWorkerDigest(ctx, source)
	if errors.Is(err, errWorkerExecutionRootUnavailable) {
		return "", false, nil
	}
	return digest, err == nil, err
}

func verifyRemoteWorkloadPassSourceInput(
	ctx context.Context,
	input RunInput,
	identity gate.WorkloadPassIdentity,
	candidate gate.WorkloadPassEvidence,
	workload gate.Workload,
	source *remoteGitTreeSnapshot,
	target *remoteGitTreeSnapshot,
	compileCovered bool,
	cache *remoteReplayCache,
	diagnostic *ReuseReplayDiagnostic,
) (bool, error) {
	compatible, err := cache.inputAlgorithmsCompatible(source, target)
	if err != nil {
		return false, err
	}
	if candidate.Identity.InputDigest != identity.InputDigest {
		semanticMatches, err := remoteWorkloadSemanticInputMatches(ctx, workload, source, target, compileCovered, cache, diagnostic)
		if err != nil || !semanticMatches {
			return false, err
		}
	}
	if compatible {
		return true, nil
	}
	inputDigest, available, err := cache.inputDigest(ctx, input.RepositoryRoot, candidate.OriginSourceTreeSHA, workload)
	if err != nil {
		return false, err
	}
	if !available || inputDigest != candidate.Identity.InputDigest {
		return false, nil
	}
	return true, nil
}

// remoteWorkloadSemanticInputMatches 以 selector 语义闭包交叉 broad 输入；只支持可精确解析的 Go selector。
func remoteWorkloadSemanticInputMatches(ctx context.Context, workload gate.Workload, source, target *remoteGitTreeSnapshot, compileCovered bool, cache *remoteReplayCache, diagnostic *ReuseReplayDiagnostic) (bool, error) {
	decision, err := cache.semanticInputDecisionWithCompileCoverage(ctx, workload, source, target, compileCovered)
	if err == nil {
		diagnostic.observeEnvironmentInputVoteDecision(decision, compileCovered)
	}
	return decision.allowReuse(), err
}

// observeEnvironmentInputVoteDecision 聚合 environment replay 的 selector 票型，
// 并显式区分 compile owner 与已被同组 fresh 执行覆盖的恢复量。
func (diagnostic *ReuseReplayDiagnostic) observeEnvironmentInputVoteDecision(decision remoteWorkloadInputVoteDecision, compileCovered bool) {
	if diagnostic == nil || decision.missVotes == 0 {
		return
	}
	if decision.allowReuse() {
		diagnostic.EnvironmentSingleVoteRecovered++
	} else {
		diagnostic.EnvironmentConfirmedMisses++
	}
	if decision.declarationMiss {
		diagnostic.EnvironmentDeclarationMissVotes++
	}
	if decision.runtimeMiss {
		diagnostic.EnvironmentRuntimeMissVotes++
	}
	if decision.compileMiss {
		diagnostic.EnvironmentCompileMissVotes++
		if compileCovered && decision.allowReuse() {
			diagnostic.EnvironmentCompileCoveredRecoveries++
		}
	}
}
