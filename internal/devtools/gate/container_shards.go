package gate

import (
	"errors"
	"fmt"
	"slices"
)

const workloadContainerShardSchemaVersion uint32 = 3

// ContainerShard binds one disposable container to an exact subset of a canonical plan.
// It deliberately excludes release:ci-l3: only the trusted owner may aggregate it.
type ContainerShard struct {
	SchemaVersion          uint32   `json:"schema_version"`
	Index                  int      `json:"index"`
	IdentityDigest         string   `json:"identity_digest"`
	Profile                Profile  `json:"profile"`
	PlanDigest             string   `json:"plan_digest"`
	SourceTreeSHA          string   `json:"source_tree_sha"`
	AcceptedManifestDigest string   `json:"accepted_manifest_digest"`
	AcceptedConfigDigest   string   `json:"accepted_config_digest"`
	ShardsPerJob           int      `json:"shards_per_job"`
	WorkloadPlanDigest     string   `json:"workload_plan_digest,omitempty"`
	CatalogDigest          string   `json:"catalog_digest,omitempty"`
	LedgerGeneration       uint64   `json:"ledger_generation,omitempty"`
	EstimatedDurationMS    int64    `json:"estimated_duration_ms,omitempty"`
	GateIDs                []GateID `json:"gate_ids"`
}

// ContainerShardSet is the canonical group reservation required before any shard starts.
type ContainerShardSet struct {
	Profile                Profile
	PlanDigest             string
	SourceTreeSHA          string
	AcceptedManifestDigest string
	AcceptedConfigDigest   string
	ShardsPerJob           int
	WorkloadPlanDigest     string
	CatalogDigest          string
	LedgerGeneration       uint64
	WorkloadPlan           WorkloadExecutionPlan
	Shards                 []ContainerShard
}

// containerShardPrerequisites 从完整 plan 剥离只能由聚合器拥有的 release gate。
func containerShardPrerequisites(plan GatePlan, manifestDigest string, configDigest string) ([]GateID, error) {
	if err := plan.Validate(); err != nil {
		return nil, err
	}
	if !digestPattern.MatchString(manifestDigest) || !digestPattern.MatchString(configDigest) {
		return nil, errors.New("accepted image manifest and config digests are required")
	}
	prerequisites, release, err := planExecutionPrerequisites(executorPlanRequest{profile: plan.Profile, planDigest: plan.PlanDigest, gateIDs: gateIDsForPlan(plan)})
	if err != nil {
		return nil, err
	}
	if release && slices.Contains(prerequisites, GateIDReleaseLayeredCheck) {
		return nil, errors.New("release authority leaked into worker shard prerequisites")
	}
	return prerequisites, nil
}

func gateIDsForPlan(plan GatePlan) []GateID {
	ids := make([]GateID, len(plan.Gates))
	for index, spec := range plan.Gates {
		ids[index] = spec.ID
	}
	return ids
}

// Validate 拒绝伪造身份、重复 gate 覆盖、worker 越权 release 和来源或镜像漂移。
func (set ContainerShardSet) Validate() error {
	if err := validateContainerShardSetHeader(set); err != nil {
		return err
	}
	if err := validateCanonicalContainerShardGroups(set); err != nil {
		return err
	}
	seen := make(map[GateID]struct{})
	for index, shard := range set.Shards {
		if err := validateContainerShard(set, shard, index); err != nil {
			return err
		}
		if err := claimContainerShardGates(seen, shard.GateIDs); err != nil {
			return err
		}
	}
	return nil
}

// validateCanonicalContainerShardGroups 拒绝遗漏、重排或把 gate 塞进错误 worker 的自洽伪造集合。
func validateCanonicalContainerShardGroups(set ContainerShardSet) error {
	expected := workloadContainerShardGroups(set.WorkloadPlan)
	if len(set.Shards) != len(expected) {
		return errors.New("container shard set does not have canonical shard count")
	}
	for index, shard := range set.Shards {
		if !slices.Equal(shard.GateIDs, expected[index]) {
			return fmt.Errorf("container shard %d does not have canonical exact gate coverage", index)
		}
	}
	return nil
}

// validateContainerShardSetHeader 验证所有 shard 共用的 profile、来源和镜像绑定。
func validateContainerShardSetHeader(set ContainerShardSet) error {
	if err := set.Profile.Validate(); err != nil {
		return err
	}
	for _, value := range []string{set.PlanDigest, set.AcceptedManifestDigest, set.AcceptedConfigDigest} {
		if !digestPattern.MatchString(value) {
			return errors.New("container shard set contains an invalid digest")
		}
	}
	if set.SourceTreeSHA == "" {
		return errors.New("container shard set source tree SHA is required")
	}
	if set.ShardsPerJob <= 0 || set.ShardsPerJob > len(set.WorkloadPlan.ExecutionWorkloadIDs) {
		return errors.New("workload container shard count is invalid")
	}
	if len(set.Shards) != set.ShardsPerJob {
		return errors.New("container shard count does not match shards_per_job")
	}
	return validateWorkloadContainerShardSetHeader(set)
}

// validateContainerShard 验证单 shard 不能改变 group 绑定或把 release gate 交给 worker。
func validateContainerShard(set ContainerShardSet, shard ContainerShard, index int) error {
	if shard.Index != index || shard.SchemaVersion != workloadContainerShardSchemaVersion || shard.ShardsPerJob != set.ShardsPerJob {
		return errors.New("container shard identity binding drifted")
	}
	if err := validateContainerShardBinding(set, shard); err != nil {
		return err
	}
	identity, err := workloadContainerShardIdentityDigest(shard)
	if err != nil || shard.IdentityDigest != identity {
		return errors.New("container shard identity digest mismatch")
	}
	if len(shard.GateIDs) == 0 || slices.Contains(shard.GateIDs, GateIDReleaseLayeredCheck) {
		return errors.New("container shard gate set is invalid")
	}
	return nil
}

// validateContainerShardBinding 将 shard 的 profile、plan、source 和 image identity 绑定回 invocation。
func validateContainerShardBinding(set ContainerShardSet, shard ContainerShard) error {
	if err := validateContainerShardCoreBinding(set, shard); err != nil {
		return err
	}
	return validateWorkloadContainerShardBinding(set, shard)
}

func claimContainerShardGates(seen map[GateID]struct{}, gateIDs []GateID) error {
	for _, id := range gateIDs {
		if _, exists := seen[id]; exists {
			return fmt.Errorf("gate %q appears in more than one container shard", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// executorPlanLanes 将 exact gate 集合映射到固定、互不共享可写目录的 lane DAG。
