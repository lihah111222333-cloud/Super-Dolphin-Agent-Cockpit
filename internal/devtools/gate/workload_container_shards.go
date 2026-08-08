package gate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

// BuildContainerShardSetFromWorkloadPlan 从已冻结 LPT 计划投影 schema v3 容器分片。
func BuildContainerShardSetFromWorkloadPlan(
	plan GatePlan,
	workloadPlan WorkloadExecutionPlan,
	acceptedManifestDigest string,
	acceptedConfigDigest string,
) (ContainerShardSet, error) {
	if _, err := containerShardPrerequisites(plan, acceptedManifestDigest, acceptedConfigDigest); err != nil {
		return ContainerShardSet{}, err
	}
	if err := workloadPlan.ValidateStored(plan); err != nil {
		return ContainerShardSet{}, err
	}
	if len(workloadPlan.Shards) == 0 {
		return ContainerShardSet{}, errors.New("workload plan shard count is invalid")
	}
	clonedPlan, err := cloneWorkloadExecutionPlan(workloadPlan)
	if err != nil {
		return ContainerShardSet{}, err
	}
	set := ContainerShardSet{
		Profile: plan.Profile, PlanDigest: plan.PlanDigest, SourceTreeSHA: plan.Source.SourceTreeSHA,
		AcceptedManifestDigest: acceptedManifestDigest, AcceptedConfigDigest: acceptedConfigDigest,
		ShardsPerJob: len(workloadPlan.Shards), WorkloadPlanDigest: workloadPlan.PlanDigest,
		CatalogDigest: workloadPlan.CatalogDigest, LedgerGeneration: workloadPlan.LedgerGeneration,
		WorkloadPlan: clonedPlan,
	}
	if err := appendWorkloadContainerShards(&set); err != nil {
		return ContainerShardSet{}, err
	}
	if err := set.Validate(); err != nil {
		return ContainerShardSet{}, err
	}
	return set, nil
}

// appendWorkloadContainerShards 将冻结 LPT 分组逐项绑定到 v3 shard identity。
func appendWorkloadContainerShards(set *ContainerShardSet) error {
	for _, workloadShard := range set.WorkloadPlan.Shards {
		gateIDs, err := workloadShardGateIDs(workloadShard)
		if err != nil {
			return err
		}
		shard := ContainerShard{
			SchemaVersion: workloadContainerShardSchemaVersion, Index: workloadShard.Index,
			Profile: set.Profile, PlanDigest: set.PlanDigest, SourceTreeSHA: set.SourceTreeSHA,
			AcceptedManifestDigest: set.AcceptedManifestDigest, AcceptedConfigDigest: set.AcceptedConfigDigest,
			ShardsPerJob: set.ShardsPerJob, WorkloadPlanDigest: set.WorkloadPlanDigest,
			CatalogDigest: set.CatalogDigest, LedgerGeneration: set.LedgerGeneration,
			EstimatedDurationMS: workloadShard.EstimatedDurationMS, GateIDs: gateIDs,
		}
		identity, err := workloadContainerShardIdentityDigest(shard)
		if err != nil {
			return err
		}
		shard.IdentityDigest = identity
		set.Shards = append(set.Shards, shard)
	}
	return nil
}

func workloadShardGateIDs(shard ShardPlan) ([]GateID, error) {
	if len(shard.Workloads) == 0 {
		return nil, errors.New("workload shard gate projection is empty")
	}
	ids := make([]GateID, len(shard.Workloads))
	for index, workload := range shard.Workloads {
		if workload.Workload.ID == "" {
			return nil, errors.New("workload shard gate identity is empty")
		}
		ids[index] = GateID(workload.Workload.ID)
	}
	return ids, nil
}

func workloadContainerShardGroups(plan WorkloadExecutionPlan) [][]GateID {
	groups := make([][]GateID, 0, len(plan.Shards))
	for _, shard := range plan.Shards {
		ids, err := workloadShardGateIDs(shard)
		if err != nil {
			return nil
		}
		groups = append(groups, ids)
	}
	return groups
}

// validateWorkloadContainerShardSetHeader 校验 v3 set 与完整冻结计划的身份绑定。
func validateWorkloadContainerShardSetHeader(set ContainerShardSet) error {
	if err := set.WorkloadPlan.validateStoredPayload(); err != nil {
		return err
	}
	if set.WorkloadPlanDigest != set.WorkloadPlan.PlanDigest ||
		set.CatalogDigest != set.WorkloadPlan.CatalogDigest ||
		set.LedgerGeneration != set.WorkloadPlan.LedgerGeneration ||
		set.PlanDigest != set.WorkloadPlan.GatePlanDigest {
		return errors.New("workload container shard set identity drifted")
	}
	if len(set.WorkloadPlan.Shards) != int(set.ShardsPerJob) {
		return errors.New("workload container shard count drifted from execution plan")
	}
	return nil
}

// validateContainerShardCoreBinding 校验所有 schema 共享的 plan、source 与镜像身份。
func validateContainerShardCoreBinding(set ContainerShardSet, shard ContainerShard) error {
	if shard.Profile != set.Profile || shard.PlanDigest != set.PlanDigest ||
		shard.SourceTreeSHA != set.SourceTreeSHA {
		return errors.New("container shard identity binding drifted")
	}
	if shard.AcceptedManifestDigest != set.AcceptedManifestDigest ||
		shard.AcceptedConfigDigest != set.AcceptedConfigDigest {
		return errors.New("container shard identity binding drifted")
	}
	return nil
}

func validateWorkloadContainerShardBinding(set ContainerShardSet, shard ContainerShard) error {
	if shard.WorkloadPlanDigest != set.WorkloadPlanDigest || shard.CatalogDigest != set.CatalogDigest ||
		shard.LedgerGeneration != set.LedgerGeneration ||
		shard.EstimatedDurationMS != set.WorkloadPlan.Shards[shard.Index].EstimatedDurationMS {
		return errors.New("container shard workload plan binding drifted")
	}
	return nil
}

// workloadContainerShardIdentityDigest 将 v3 shard 绑定到冻结 LPT 快照和估时。
func workloadContainerShardIdentityDigest(shard ContainerShard) (string, error) {
	material := struct {
		SchemaVersion          uint32
		Index                  int
		Profile                Profile
		PlanDigest             string
		SourceTreeSHA          string
		AcceptedManifestDigest string
		AcceptedConfigDigest   string
		ShardsPerJob           int
		WorkloadPlanDigest     string
		CatalogDigest          string
		LedgerGeneration       uint64
		EstimatedDurationMS    int64
		GateIDs                []GateID
	}{
		shard.SchemaVersion, shard.Index, shard.Profile, shard.PlanDigest, shard.SourceTreeSHA,
		shard.AcceptedManifestDigest, shard.AcceptedConfigDigest, shard.ShardsPerJob,
		shard.WorkloadPlanDigest, shard.CatalogDigest, shard.LedgerGeneration,
		shard.EstimatedDurationMS, shard.GateIDs,
	}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

func cloneWorkloadExecutionPlan(plan WorkloadExecutionPlan) (WorkloadExecutionPlan, error) {
	encoded, err := json.Marshal(plan)
	if err != nil {
		return WorkloadExecutionPlan{}, fmt.Errorf("marshal workload execution plan clone: %w", err)
	}
	var cloned WorkloadExecutionPlan
	if err := decodeStrictJSON(bytes.NewReader(encoded), &cloned); err != nil {
		return WorkloadExecutionPlan{}, fmt.Errorf("decode workload execution plan clone: %w", err)
	}
	return cloned, nil
}
