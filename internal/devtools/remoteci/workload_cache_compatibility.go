package remoteci

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// promoteCompatiblePassedWorkloadCache 将旧指纹算法下的 PASS 安全迁移到当前身份。
// 只有旧证据对应源码树按当前算法重算后仍与当前目标输入完全相同，才允许复用。
func promoteCompatiblePassedWorkloadCache(
	ctx context.Context,
	ledgerStore *gate.DurationLedgerStore,
	now func() time.Time,
	repositoryRoot string,
	workerWorkloads []gate.Workload,
	entries []remoteWorkloadCacheEntry,
	forceRerun bool,
) (map[string]gate.PlanGateExecution, error) {
	promoted := make(map[string]gate.PlanGateExecution)
	if compatiblePassPromotionDisabled(ledgerStore, entries, forceRerun) {
		return promoted, nil
	}
	requests, requestByWorkload := compatiblePassCandidateRequests(entries)
	candidates, err := ledgerStore.LookupCompatibleWorkloadPassCandidates(
		requests, remoteCompatiblePassCandidateLimit,
	)
	if err != nil {
		return nil, fmt.Errorf("query compatible SQLite PASS proofs: %w", err)
	}
	if len(candidates) == 0 {
		return promoted, nil
	}
	parentIDsByTree, expandChildrenByTree, err := compatiblePassParentIDs(
		candidates,
		requestByWorkload,
		workerWorkloads,
	)
	if err != nil {
		return nil, err
	}
	digestsByTree, err := compatiblePassInputDigests(
		ctx,
		repositoryRoot,
		workerWorkloads,
		parentIDsByTree,
		expandChildrenByTree,
	)
	if err != nil {
		return nil, err
	}
	promoted = projectPassedWorkloadCache(
		now().UTC(), entries, compatiblePassMatches(entries, candidates, digestsByTree),
	)
	if len(promoted) == 0 {
		return promoted, nil
	}
	if err := recordPassedWorkloadCacheProofs(ledgerStore, entries, promoted, now().UTC()); err != nil {
		return nil, fmt.Errorf("record compatible SQLite PASS proofs: %w", err)
	}
	return promoted, nil
}

func compatiblePassPromotionDisabled(
	ledgerStore *gate.DurationLedgerStore,
	entries []remoteWorkloadCacheEntry,
	forceRerun bool,
) bool {
	return ledgerStore == nil || forceRerun || len(entries) == 0
}

func compatiblePassCandidateRequests(
	entries []remoteWorkloadCacheEntry,
) ([]gate.WorkloadPassCandidateQuery, map[string]gate.WorkloadPassCandidateQuery) {
	requests := make([]gate.WorkloadPassCandidateQuery, len(entries))
	requestByWorkload := make(map[string]gate.WorkloadPassCandidateQuery, len(entries))
	for index, entry := range entries {
		request := gate.WorkloadPassCandidateQuery{
			WorkloadID: entry.workloadID, ExecutionDigest: entry.executionDigest,
			EnvironmentDigest: entry.environmentDigest,
		}
		requests[index] = request
		requestByWorkload[entry.workloadID] = request
	}
	return requests, requestByWorkload
}

func compatiblePassParentIDs(
	candidates map[string][]gate.WorkloadPassCandidate,
	requestByWorkload map[string]gate.WorkloadPassCandidateQuery,
	workerWorkloads []gate.Workload,
) (map[string]map[string]struct{}, map[string]bool, error) {
	workloadByID := make(map[string]gate.Workload, len(workerWorkloads))
	for _, workload := range workerWorkloads {
		workloadByID[workload.ID] = workload
	}
	parentIDsByTree := make(map[string]map[string]struct{})
	expandChildrenByTree := make(map[string]bool)
	for workloadID, workloadCandidates := range candidates {
		parentID, detachedChild, err := compatiblePassParentWorkloadID(workloadID, workloadByID)
		if err != nil {
			return nil, nil, err
		}
		for _, candidate := range workloadCandidates {
			if err := validateCompatiblePassCandidate(candidate, requestByWorkload[workloadID]); err != nil {
				return nil, nil, err
			}
			if parentIDsByTree[candidate.SourceTreeSHA] == nil {
				parentIDsByTree[candidate.SourceTreeSHA] = make(map[string]struct{})
			}
			parentIDsByTree[candidate.SourceTreeSHA][parentID] = struct{}{}
			expandChildrenByTree[candidate.SourceTreeSHA] =
				expandChildrenByTree[candidate.SourceTreeSHA] || detachedChild
		}
	}
	return parentIDsByTree, expandChildrenByTree, nil
}

func compatiblePassInputDigests(
	ctx context.Context,
	repositoryRoot string,
	workerWorkloads []gate.Workload,
	parentIDsByTree map[string]map[string]struct{},
	expandChildrenByTree map[string]bool,
) (map[string]map[string]string, error) {
	digestsByTree := make(map[string]map[string]string, len(parentIDsByTree))
	trees := make([]string, 0, len(parentIDsByTree))
	for tree := range parentIDsByTree {
		trees = append(trees, tree)
	}
	sort.Strings(trees)
	for _, tree := range trees {
		digests, err := compatiblePassTreeInputDigests(
			ctx,
			repositoryRoot,
			tree,
			workerWorkloads,
			parentIDsByTree[tree],
			expandChildrenByTree[tree],
		)
		if err != nil {
			return nil, err
		}
		digestsByTree[tree] = digests
	}
	return digestsByTree, nil
}

func compatiblePassTreeInputDigests(
	ctx context.Context,
	repositoryRoot string,
	tree string,
	workerWorkloads []gate.Workload,
	parentIDs map[string]struct{},
	expandChildren bool,
) (map[string]string, error) {
	snapshot, err := loadRemoteGitTreeSnapshot(ctx, repositoryRoot, tree)
	if err != nil {
		return nil, fmt.Errorf("load compatible PASS source tree %s: %w", tree, err)
	}
	subset := make([]gate.Workload, 0, len(parentIDs))
	for _, workload := range workerWorkloads {
		if _, ok := parentIDs[workload.ID]; ok {
			subset = append(subset, workload)
		}
	}
	if len(subset) != len(parentIDs) {
		return nil, fmt.Errorf("compatible PASS source tree %s has unresolved parent workloads", tree)
	}
	digests, err := snapshot.remoteWorkloadInputDigests(ctx, subset)
	if err != nil {
		return nil, fmt.Errorf("recompute compatible PASS source tree %s: %w", tree, err)
	}
	if expandChildren {
		childDigests, childErr := snapshot.remoteExactGoTestInputDigests(ctx, subset)
		if childErr != nil {
			return nil, fmt.Errorf(
				"recompute compatible PASS child inputs for source tree %s: %w",
				tree,
				childErr,
			)
		}
		maps.Copy(digests, childDigests)
	}
	return digests, nil
}

func compatiblePassMatches(
	entries []remoteWorkloadCacheEntry,
	candidates map[string][]gate.WorkloadPassCandidate,
	digestsByTree map[string]map[string]string,
) []bool {
	matched := make([]bool, len(entries))
	for index, entry := range entries {
		for _, candidate := range candidates[entry.workloadID] {
			if digestsByTree[candidate.SourceTreeSHA][entry.workloadID] == entry.inputDigest {
				matched[index] = true
				break
			}
		}
	}
	return matched
}

func compatiblePassParentWorkloadID(
	workloadID string,
	workloadByID map[string]gate.Workload,
) (string, bool, error) {
	if _, ok := workloadByID[workloadID]; ok {
		return workloadID, false, nil
	}
	parentGate, kind, target, targeted, err := gate.ParseWorkloadID(workloadID)
	if err != nil {
		return "", false, fmt.Errorf("parse compatible PASS workload %q: %w", workloadID, err)
	}
	if !targeted {
		return "", false, fmt.Errorf("compatible PASS workload %q has no catalog parent", workloadID)
	}
	var testTarget gate.GoTestTarget
	switch kind {
	case gate.WorkloadTargetGoTest:
		testTarget, err = gate.ParseGoTestTarget(target)
	case gate.WorkloadTargetGoBenchmark:
		testTarget, err = gate.ParseGoBenchmarkTarget(target)
	default:
		return "", false, fmt.Errorf(
			"compatible PASS workload %q has unsupported detached target kind %q",
			workloadID,
			kind,
		)
	}
	if err != nil {
		return "", false, fmt.Errorf("parse compatible PASS target %q: %w", workloadID, err)
	}
	parentWorkload, err := gate.NewGoPackageWorkload(parentGate, testTarget.Package, 1)
	if err != nil {
		return "", false, fmt.Errorf("rebuild compatible PASS parent %q: %w", workloadID, err)
	}
	parentWorkloadID := parentWorkload.ID
	if _, ok := workloadByID[parentWorkloadID]; !ok {
		return "", false, fmt.Errorf(
			"compatible PASS workload %q references unknown parent %q",
			workloadID,
			parentWorkloadID,
		)
	}
	return parentWorkloadID, true, nil
}

func validateCompatiblePassCandidate(
	candidate gate.WorkloadPassCandidate,
	request gate.WorkloadPassCandidateQuery,
) error {
	if request.WorkloadID == "" ||
		candidate.Proof.WorkloadID != request.WorkloadID ||
		candidate.Proof.ExecutionDigest != request.ExecutionDigest ||
		candidate.Proof.EnvironmentDigest != request.EnvironmentDigest {
		return fmt.Errorf(
			"compatible PASS candidate %q conflicts with query identity",
			request.WorkloadID,
		)
	}
	if remoteWorkloadCacheIdentityDigest(
		candidate.Proof.EnvironmentDigest,
		candidate.Proof.ExecutionDigest,
		candidate.Proof.InputDigest,
	) != candidate.Proof.IdentityDigest {
		return fmt.Errorf(
			"compatible PASS candidate %q has an invalid content identity",
			request.WorkloadID,
		)
	}
	return nil
}
