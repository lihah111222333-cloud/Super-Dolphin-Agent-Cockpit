package remoteci

import (
	"context"
	"fmt"
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
	cache *remoteReplayCache,
) error {
	missing := missingRemoteWorkloadPassIdentities(identities, reused)
	if len(missing) == 0 {
		return nil
	}
	hints, err := input.LedgerStore.LookupWorkloadPassEnvironmentReplayHints(missing)
	if err != nil {
		return err
	}
	workloads, err := remoteReplayWorkloadIndex(catalog)
	if err != nil {
		return err
	}
	selectedIdentities := make([]gate.WorkloadPassIdentity, 0, len(missing))
	selectedHints := make([]gate.WorkloadPassEnvironmentReplayHint, 0, len(missing))
	for _, identity := range missing {
		workload := workloads[identity.WorkloadID]
		hint, ok, err := selectRemoteWorkloadPassEnvironmentReplayHint(
			ctx, input, identity, workload, hints[identity.WorkloadID], workerTimeout, resourcePolicy, cache,
		)
		if err != nil {
			return err
		}
		if ok {
			selectedIdentities = append(selectedIdentities, identity)
			selectedHints = append(selectedHints, hint)
		}
	}
	return authorizeRemoteWorkloadPassEnvironmentHints(input, selectedIdentities, selectedHints, reused, proofs)
}

// selectRemoteWorkloadPassEnvironmentReplayHint 按确定顺序选择首个语义匹配的未授权 hint。
func selectRemoteWorkloadPassEnvironmentReplayHint(
	ctx context.Context,
	input RunInput,
	identity gate.WorkloadPassIdentity,
	workload gate.Workload,
	hints []gate.WorkloadPassEnvironmentReplayHint,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
) (gate.WorkloadPassEnvironmentReplayHint, bool, error) {
	if workload.ID == "" {
		return gate.WorkloadPassEnvironmentReplayHint{}, false, fmt.Errorf("remote workload PASS environment replay %q is absent from current catalog", identity.WorkloadID)
	}
	target, available, err := cache.snapshot(ctx, input.RepositoryRoot, input.Tree)
	if err != nil || !available {
		return gate.WorkloadPassEnvironmentReplayHint{}, false, err
	}
	goFlags, err := remoteWorkloadGoFlags(string(identity.WorkloadID))
	if err != nil {
		return gate.WorkloadPassEnvironmentReplayHint{}, false, err
	}
	for _, hint := range hints {
		candidate := hint.UntrustedCandidate()
		ok, err := matchesRemoteWorkloadPassEnvironmentCandidate(ctx, input, identity, workload, candidate, target, goFlags, workerTimeout, resourcePolicy, cache)
		if err != nil {
			return gate.WorkloadPassEnvironmentReplayHint{}, false, err
		}
		if ok {
			return hint, true, nil
		}
	}
	return gate.WorkloadPassEnvironmentReplayHint{}, false, nil
}

// matchesRemoteWorkloadPassEnvironmentCandidate 只对未授权 hint 做语义筛选。
func matchesRemoteWorkloadPassEnvironmentCandidate(
	ctx context.Context,
	input RunInput,
	identity gate.WorkloadPassIdentity,
	workload gate.Workload,
	candidate gate.WorkloadPassEvidence,
	target *remoteGitTreeSnapshot,
	goFlags string,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
) (bool, error) {
	if candidate.OriginAcceptedGeneration != input.AcceptedGeneration {
		return false, nil
	}
	source, available, err := cache.snapshot(ctx, input.RepositoryRoot, candidate.OriginSourceTreeSHA)
	if err != nil || !available {
		return false, err
	}
	valid, err := verifyRemoteWorkloadPassEnvironmentReplay(ctx, input, identity, candidate, source, target, goFlags, workerTimeout, resourcePolicy, cache, workload)
	return valid, err
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

func verifyRemoteWorkloadPassEnvironmentReplay(
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
	workload gate.Workload,
) (bool, error) {
	legacyMatches, err := verifyRemoteWorkloadPassLegacyEnvironment(ctx, input, candidate, source, goFlags, workerTimeout, resourcePolicy, cache)
	if err != nil || !legacyMatches {
		return false, err
	}
	preciseMatches, err := verifyRemoteWorkloadPassPreciseEnvironment(ctx, input, identity, source, target, goFlags, workerTimeout, resourcePolicy, cache)
	if err != nil || !preciseMatches {
		return false, err
	}
	return verifyRemoteWorkloadPassSourceInput(ctx, input, identity, candidate, workload, cache)
}

func verifyRemoteWorkloadPassLegacyEnvironment(
	ctx context.Context,
	input RunInput,
	candidate gate.WorkloadPassEvidence,
	source *remoteGitTreeSnapshot,
	goFlags string,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	cache *remoteReplayCache,
) (bool, error) {
	legacyDigest, err := cache.legacyWorkerDigest(ctx, source)
	if err != nil {
		return false, err
	}
	legacyInput := input
	legacyInput.WorkerExecutionSemanticDigest = legacyDigest
	legacyEnvironment, err := remoteWorkloadEnvironmentDigestForGoFlags(legacyInput, workerTimeout, resourcePolicy, goFlags)
	if err != nil {
		return false, err
	}
	return candidate.Identity.EnvironmentDigest == legacyEnvironment, nil
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
	preciseSourceDigest, err := cache.preciseWorkerDigest(ctx, source)
	if err != nil {
		return false, err
	}
	preciseTargetDigest, err := cache.preciseWorkerDigest(ctx, target)
	if err != nil {
		return false, err
	}
	if preciseSourceDigest != preciseTargetDigest || preciseTargetDigest != input.WorkerExecutionSemanticDigest {
		return false, nil
	}
	currentInput := input
	currentInput.WorkerExecutionSemanticDigest = preciseSourceDigest
	originEnvironment, err := remoteWorkloadEnvironmentDigestForGoFlags(currentInput, workerTimeout, resourcePolicy, goFlags)
	if err != nil {
		return false, err
	}
	if originEnvironment != identity.EnvironmentDigest {
		return false, nil
	}
	currentTargetEnvironment, err := remoteWorkloadEnvironmentDigestForGoFlags(input, workerTimeout, resourcePolicy, goFlags)
	if err != nil {
		return false, err
	}
	return currentTargetEnvironment == identity.EnvironmentDigest, nil
}

func verifyRemoteWorkloadPassSourceInput(
	ctx context.Context,
	input RunInput,
	identity gate.WorkloadPassIdentity,
	candidate gate.WorkloadPassEvidence,
	workload gate.Workload,
	cache *remoteReplayCache,
) (bool, error) {
	inputDigest, available, err := cache.inputDigest(ctx, input.RepositoryRoot, candidate.OriginSourceTreeSHA, workload)
	if err != nil {
		return false, err
	}
	return available && inputDigest == identity.InputDigest, nil
}
