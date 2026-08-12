package remoteci

import (
	"context"
	"fmt"

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
	cache *remoteReplayCache,
	confirmations remoteReuseMissConfirmations,
	diagnostic *ReuseReplayDiagnostic,
) error {
	missing := missingRemoteWorkloadPassIdentities(identities, reused)
	if len(missing) == 0 {
		return nil
	}
	candidates, err := input.LedgerStore.LookupWorkloadPassSourceReplayCandidates(missing)
	if err != nil {
		return err
	}
	recordRemoteSourceReplayCandidates(diagnostic, candidates)
	workloads, err := remoteReplayWorkloadIndex(catalog)
	if err != nil {
		return err
	}
	for _, identity := range missing {
		evidence, ok, err := selectRemoteWorkloadPassReplay(ctx, input.RepositoryRoot, input.Tree, identity, workloads, candidates[identity.WorkloadID], cache, diagnostic)
		if err != nil {
			return err
		}
		if ok {
			reused[string(identity.WorkloadID)] = evidence
			continue
		}
		confirmations.confirm(identity.WorkloadID, remoteReuseSourceMiss)
	}
	return nil
}

func recordRemoteSourceReplayCandidates(diagnostic *ReuseReplayDiagnostic, candidates map[gate.GateID][]gate.WorkloadPassEvidence) {
	for _, workloadCandidates := range candidates {
		if len(workloadCandidates) > 0 {
			diagnostic.SourceCandidateWorkloads++
			diagnostic.SourceCandidates += len(workloadCandidates)
		}
	}
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

// selectRemoteWorkloadPassReplay 按确定顺序重算来源树，首个精确 input digest 命中才返回直接证据。
func selectRemoteWorkloadPassReplay(
	ctx context.Context,
	repositoryRoot string,
	targetTree string,
	identity gate.WorkloadPassIdentity,
	workloads map[gate.GateID]gate.Workload,
	candidates []gate.WorkloadPassEvidence,
	cache *remoteReplayCache,
	diagnostic *ReuseReplayDiagnostic,
) (gate.WorkloadPassEvidence, bool, error) {
	workload, ok := workloads[identity.WorkloadID]
	if !ok {
		return gate.WorkloadPassEvidence{}, false, fmt.Errorf("remote workload PASS source replay %q is absent from current catalog", identity.WorkloadID)
	}
	target, available, err := cache.snapshot(ctx, repositoryRoot, targetTree)
	if err != nil || !available {
		return gate.WorkloadPassEvidence{}, false, err
	}
	for _, candidate := range candidates {
		matches, err := matchesRemoteWorkloadPassSourceCandidate(ctx, repositoryRoot, identity, workload, candidate, target, cache, diagnostic)
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

// matchesRemoteWorkloadPassSourceCandidate 从权威来源树重算当前 broad 摘要并交叉 selector 语义；
// 历史 evidence 的旧摘要算法不替代当前重算，也不在进入多票裁决前制造假失败。
func matchesRemoteWorkloadPassSourceCandidate(ctx context.Context, repositoryRoot string, identity gate.WorkloadPassIdentity, workload gate.Workload, candidate gate.WorkloadPassEvidence, target *remoteGitTreeSnapshot, cache *remoteReplayCache, diagnostic *ReuseReplayDiagnostic) (bool, error) {
	digest, available, err := cache.inputDigest(ctx, repositoryRoot, candidate.OriginSourceTreeSHA, workload)
	if err != nil {
		return false, fmt.Errorf("recompute remote workload PASS source %q: %w", identity.WorkloadID, err)
	}
	if !available {
		diagnostic.SourceInputUnavailable++
		return false, nil
	}
	if digest == identity.InputDigest {
		return true, nil
	}
	source, sourceAvailable, err := cache.snapshot(ctx, repositoryRoot, candidate.OriginSourceTreeSHA)
	if err != nil {
		return false, err
	}
	matches := false
	if sourceAvailable {
		decision, decisionErr := cache.semanticInputDecision(ctx, workload, source, target)
		err = decisionErr
		matches = decision.allowReuse()
		diagnostic.observeSourceInputVoteDecision(decision)
	}
	if err == nil && !matches {
		diagnostic.SourceInputMismatch++
	}
	return matches, err
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
