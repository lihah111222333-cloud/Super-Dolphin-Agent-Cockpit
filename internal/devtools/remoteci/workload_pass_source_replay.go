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
) error {
	missing := missingRemoteWorkloadPassIdentities(identities, reused)
	if len(missing) == 0 {
		return nil
	}
	candidates, err := input.LedgerStore.LookupWorkloadPassSourceReplayCandidates(missing)
	if err != nil {
		return err
	}
	workloads, err := remoteReplayWorkloadIndex(catalog)
	if err != nil {
		return err
	}
	for _, identity := range missing {
		evidence, ok, err := selectRemoteWorkloadPassReplay(ctx, input.RepositoryRoot, identity, workloads, candidates[identity.WorkloadID], cache)
		if err != nil {
			return err
		}
		if ok {
			reused[string(identity.WorkloadID)] = evidence
		}
	}
	return nil
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
	identity gate.WorkloadPassIdentity,
	workloads map[gate.GateID]gate.Workload,
	candidates []gate.WorkloadPassEvidence,
	cache *remoteReplayCache,
) (gate.WorkloadPassEvidence, bool, error) {
	workload, ok := workloads[identity.WorkloadID]
	if !ok {
		return gate.WorkloadPassEvidence{}, false, fmt.Errorf("remote workload PASS source replay %q is absent from current catalog", identity.WorkloadID)
	}
	for _, candidate := range candidates {
		digest, available, err := cache.inputDigest(ctx, repositoryRoot, candidate.OriginSourceTreeSHA, workload)
		if err != nil {
			return gate.WorkloadPassEvidence{}, false, fmt.Errorf("recompute remote workload PASS source %q: %w", identity.WorkloadID, err)
		}
		if !available || digest != identity.InputDigest {
			continue
		}
		if _, err := gate.WorkloadPassSourceReplaySHA256(identity, candidate); err != nil {
			return gate.WorkloadPassEvidence{}, false, err
		}
		return candidate, true, nil
	}
	return gate.WorkloadPassEvidence{}, false, nil
}
