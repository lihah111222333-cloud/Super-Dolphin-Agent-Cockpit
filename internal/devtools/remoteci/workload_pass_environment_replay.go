package remoteci

import (
	"context"
	"errors"
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
	confirmations remoteReuseMissConfirmations,
	diagnostic *ReuseReplayDiagnostic,
) error {
	missing := missingRemoteWorkloadPassIdentities(identities, reused)
	if len(missing) == 0 {
		return nil
	}
	hints, err := input.LedgerStore.LookupWorkloadPassEnvironmentReplayHints(missing)
	if err != nil {
		return err
	}
	recordRemoteEnvironmentReplayHints(diagnostic, hints)
	workloads, err := remoteReplayWorkloadIndex(catalog)
	if err != nil {
		return err
	}
	selectedIdentities := make([]gate.WorkloadPassIdentity, 0, len(missing))
	selectedHints := make([]gate.WorkloadPassEnvironmentReplayHint, 0, len(missing))
	for _, identity := range missing {
		workload := workloads[identity.WorkloadID]
		hint, ok, err := selectRemoteWorkloadPassEnvironmentReplayHint(
			ctx, input, identity, workload, hints[identity.WorkloadID], workerTimeout, resourcePolicy, cache, diagnostic,
		)
		if err != nil {
			return err
		}
		if ok {
			selectedIdentities = append(selectedIdentities, identity)
			selectedHints = append(selectedHints, hint)
			continue
		}
		confirmations.confirm(identity.WorkloadID, remoteReuseEnvironmentMiss)
	}
	return authorizeRemoteWorkloadPassEnvironmentHints(input, selectedIdentities, selectedHints, reused, proofs)
}

func recordRemoteEnvironmentReplayHints(diagnostic *ReuseReplayDiagnostic, hints map[gate.GateID][]gate.WorkloadPassEnvironmentReplayHint) {
	for _, workloadHints := range hints {
		if len(workloadHints) > 0 {
			diagnostic.EnvironmentHintWorkloads++
			diagnostic.EnvironmentHints += len(workloadHints)
		}
	}
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
	diagnostic *ReuseReplayDiagnostic,
) (gate.WorkloadPassEnvironmentReplayHint, bool, error) {
	if workload.ID == "" {
		return gate.WorkloadPassEnvironmentReplayHint{}, false, fmt.Errorf("remote workload PASS environment replay %q is absent from current catalog", identity.WorkloadID)
	}
	target, available, err := cache.snapshot(ctx, input.RepositoryRoot, input.Tree)
	if err != nil || !available {
		if err == nil {
			diagnostic.EnvironmentTargetUnavailable++
		}
		return gate.WorkloadPassEnvironmentReplayHint{}, false, err
	}
	goFlags, err := remoteWorkloadGoFlags(string(identity.WorkloadID))
	if err != nil {
		return gate.WorkloadPassEnvironmentReplayHint{}, false, err
	}
	for _, hint := range hints {
		candidate := hint.UntrustedCandidate()
		ok, err := matchesRemoteWorkloadPassEnvironmentCandidate(ctx, input, identity, workload, candidate, target, goFlags, workerTimeout, resourcePolicy, cache, diagnostic)
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
	diagnostic *ReuseReplayDiagnostic,
) (bool, error) {
	if candidate.OriginAcceptedGeneration != input.AcceptedGeneration {
		diagnostic.EnvironmentGenerationMismatch++
		return false, nil
	}
	source, available, err := cache.snapshot(ctx, input.RepositoryRoot, candidate.OriginSourceTreeSHA)
	if err != nil || !available {
		if err == nil {
			diagnostic.EnvironmentSourceUnavailable++
		}
		return false, err
	}
	valid, err := verifyRemoteWorkloadPassEnvironmentReplay(ctx, input, identity, candidate, source, target, goFlags, workerTimeout, resourcePolicy, cache, workload, diagnostic)
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

// verifyRemoteWorkloadPassEnvironmentReplay 逐层核对历史环境、当前 worker 与 workload 输入，任一不一致都拒绝复用。
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
	diagnostic *ReuseReplayDiagnostic,
) (bool, error) {
	historicalMatches, err := verifyRemoteWorkloadPassHistoricalEnvironment(ctx, input, candidate, source, goFlags, workerTimeout, resourcePolicy, cache)
	if err != nil || !historicalMatches {
		if err == nil {
			diagnostic.EnvironmentHistoricalMismatch++
		}
		return false, err
	}
	preciseMatches, err := verifyRemoteWorkloadPassPreciseEnvironment(ctx, input, identity, source, target, goFlags, workerTimeout, resourcePolicy, cache)
	if err != nil || !preciseMatches {
		if err == nil {
			diagnostic.EnvironmentCurrentWorkerMismatch++
		}
		return false, err
	}
	inputMatches, err := verifyRemoteWorkloadPassSourceInput(ctx, input, identity, candidate, workload, source, target, cache)
	if err == nil && !inputMatches {
		diagnostic.EnvironmentInputMismatch++
	}
	return inputMatches, err
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
	legacyDigest, err := cache.legacyWorkerDigest(ctx, source)
	if err != nil {
		return false, err
	}
	legacyMatches, err := remoteWorkloadPassEnvironmentMatches(
		input, candidate, legacyDigest, goFlags, workerTimeout, resourcePolicy,
	)
	if err != nil {
		return false, err
	}
	if legacyMatches {
		return true, nil
	}
	previousDigest, err := cache.previousWorkerDigest(ctx, source)
	if err != nil {
		return false, err
	}
	previousMatches, err := remoteWorkloadPassEnvironmentMatches(
		input, candidate, previousDigest, goFlags, workerTimeout, resourcePolicy,
	)
	if err != nil || previousMatches {
		return previousMatches, err
	}
	previousStableDigest, err := cache.previousStableWorkerDigest(ctx, source)
	if err != nil {
		return false, err
	}
	return remoteWorkloadPassEnvironmentMatches(
		input, candidate, previousStableDigest, goFlags, workerTimeout, resourcePolicy,
	)
}

func remoteWorkloadPassEnvironmentMatches(
	input RunInput,
	candidate gate.WorkloadPassEvidence,
	workerDigest string,
	goFlags string,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
) (bool, error) {
	historicalInput := input
	historicalInput.WorkerExecutionSemanticDigest = workerDigest
	historicalEnvironment, err := remoteWorkloadEnvironmentDigestForGoFlags(historicalInput, workerTimeout, resourcePolicy, goFlags)
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
	cache *remoteReplayCache,
) (bool, error) {
	inputDigest, available, err := cache.inputDigest(ctx, input.RepositoryRoot, candidate.OriginSourceTreeSHA, workload)
	if err != nil {
		return false, err
	}
	if !available || inputDigest != candidate.Identity.InputDigest {
		return false, nil
	}
	if inputDigest == identity.InputDigest {
		return true, nil
	}
	return remoteWorkloadSemanticInputMatches(ctx, workload, source, target, cache)
}

// remoteWorkloadSemanticInputMatches 以 selector 语义闭包交叉 broad 输入；只支持可精确解析的 Go selector。
func remoteWorkloadSemanticInputMatches(ctx context.Context, workload gate.Workload, source, target *remoteGitTreeSnapshot, cache *remoteReplayCache) (bool, error) {
	return cache.semanticInputMatches(ctx, workload, source, target)
}
