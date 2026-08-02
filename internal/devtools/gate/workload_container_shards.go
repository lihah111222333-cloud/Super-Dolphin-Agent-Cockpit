package gate

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
		identity, err := containerShardIdentityDigest(shard)
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

// NarrowContainerShard 为未命中的 workload 派生一次执行身份，同时保留 canonical 计划绑定。
func NarrowContainerShard(shard ContainerShard, gateIDs []GateID) (ContainerShard, error) {
	if len(gateIDs) == 0 {
		return ContainerShard{}, errors.New("narrowed container shard must contain at least one workload")
	}
	allowed := make(map[GateID]int, len(shard.GateIDs))
	for index, id := range shard.GateIDs {
		allowed[id] = index
	}
	last := -1
	seen := make(map[GateID]struct{}, len(gateIDs))
	for _, id := range gateIDs {
		index, ok := allowed[id]
		if !ok || index <= last {
			return ContainerShard{}, errors.New("narrowed container shard workloads must preserve canonical order")
		}
		if _, duplicate := seen[id]; duplicate {
			return ContainerShard{}, errors.New("narrowed container shard contains a duplicate workload")
		}
		seen[id], last = struct{}{}, index
	}
	narrowed := shard
	narrowed.GateIDs = slices.Clone(gateIDs)
	narrowed.IdentityDigest = ""
	identity, err := containerShardIdentityDigest(narrowed)
	if err != nil {
		return ContainerShard{}, err
	}
	narrowed.IdentityDigest = identity
	return narrowed, nil
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

func isWorkloadContainerShardSet(set ContainerShardSet) bool {
	return set.WorkloadPlanDigest != "" || set.WorkloadPlan.PlanDigest != "" ||
		(len(set.Shards) > 0 && set.Shards[0].SchemaVersion == workloadContainerShardSchemaVersion)
}

func validateStaticContainerShardSetHeader(set ContainerShardSet) error {
	if set.WorkloadPlanDigest != "" || set.CatalogDigest != "" || set.LedgerGeneration != 0 ||
		set.WorkloadPlan.PlanDigest != "" {
		return errors.New("static container shard set contains workload plan fields")
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

func validateStaticContainerShardBinding(shard ContainerShard) error {
	if shard.WorkloadPlanDigest != "" || shard.CatalogDigest != "" ||
		shard.LedgerGeneration != 0 || shard.EstimatedDurationMS != 0 {
		return errors.New("static container shard contains workload plan fields")
	}
	return nil
}

func containerShardIdentityDigest(shard ContainerShard) (string, error) {
	if shard.SchemaVersion == workloadContainerShardSchemaVersion {
		return workloadContainerShardIdentityDigest(shard)
	}
	material := struct {
		SchemaVersion          uint32
		Index                  int
		Profile                Profile
		PlanDigest             string
		SourceTreeSHA          string
		AcceptedManifestDigest string
		AcceptedConfigDigest   string
		ShardsPerJob           int
		GateIDs                []GateID
	}{shard.SchemaVersion, shard.Index, shard.Profile, shard.PlanDigest, shard.SourceTreeSHA,
		shard.AcceptedManifestDigest, shard.AcceptedConfigDigest, shard.ShardsPerJob, shard.GateIDs}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
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

// equalContainerShardCore 比较所有 schema 共用的稳定 shard 身份字段。
func equalContainerShardCore(left, right ContainerShard) bool {
	return left.SchemaVersion == right.SchemaVersion && left.Index == right.Index &&
		left.IdentityDigest == right.IdentityDigest && left.Profile == right.Profile &&
		left.PlanDigest == right.PlanDigest && left.SourceTreeSHA == right.SourceTreeSHA &&
		left.AcceptedManifestDigest == right.AcceptedManifestDigest &&
		left.AcceptedConfigDigest == right.AcceptedConfigDigest &&
		left.ShardsPerJob == right.ShardsPerJob
}

func equalContainerShardWorkloadBinding(left, right ContainerShard) bool {
	return left.WorkloadPlanDigest == right.WorkloadPlanDigest &&
		left.CatalogDigest == right.CatalogDigest &&
		left.LedgerGeneration == right.LedgerGeneration &&
		left.EstimatedDurationMS == right.EstimatedDurationMS
}

// equalContainerShard 比较回执中的完整 canonical shard 绑定，而非只信任 digest 字段。
func equalContainerShard(left, right ContainerShard) bool {
	return equalContainerShardCore(left, right) &&
		equalContainerShardWorkloadBinding(left, right) &&
		slices.Equal(left.GateIDs, right.GateIDs)
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
