package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// validatePassedShardReceipt 校验当前动态数量分片的完整签名闭包。
func (r ResultReceipt) validatePassedShardReceipt() error {
	set, err := resultReceiptShardSet(r.WorkloadPlan, r.ShardReceipts)
	if err != nil {
		return err
	}
	if err := validateResultReceiptShardBinding(r, set); err != nil {
		return err
	}
	aggregated, err := aggregateResultReceiptShards(r, set)
	if err != nil {
		return fmt.Errorf("aggregate result receipt shards: %w", err)
	}
	if err := validateResultReceiptGateAggregate(r.GateResults, aggregated); err != nil {
		return err
	}
	if err := validateResultReceiptShardTimeline(r, r.ShardReceipts); err != nil {
		return err
	}
	container, err := aggregateResultReceiptContainer(r.ShardReceipts)
	if err != nil {
		return err
	}
	if r.Container != container {
		return errors.New("result receipt aggregate container evidence drifted")
	}
	return nil
}

// validateStoredPassedShardReceipt 使用回执绑定的冻结 LPT 计划校验终态分片闭包。
func (r ResultReceipt) validateStoredPassedShardReceipt(plan GatePlan) error {
	set, err := storedResultReceiptShardSet(r.WorkloadPlan, r.ShardReceipts, plan)
	if err != nil {
		return err
	}
	if err := validateStoredResultReceiptShardBinding(r, set, plan); err != nil {
		return err
	}
	aggregated, err := aggregateStoredResultReceiptShards(r, set, plan)
	if err != nil {
		return fmt.Errorf("aggregate stored result receipt shards: %w", err)
	}
	if err := validateResultReceiptGateAggregate(r.GateResults, aggregated); err != nil {
		return err
	}
	if err := validateResultReceiptShardTimeline(r, r.ShardReceipts); err != nil {
		return err
	}
	container, err := aggregateResultReceiptContainer(r.ShardReceipts)
	if err != nil {
		return err
	}
	if r.Container != container {
		return errors.New("stored result receipt aggregate container evidence drifted")
	}
	return nil
}

// resultReceiptShardSet 从签名回执重建唯一 canonical shard set。
func resultReceiptShardSet(workloadPlan WorkloadExecutionPlan, receipts []ContainerShardReceipt) (ContainerShardSet, error) {
	set, err := uncheckedResultReceiptShardSet(workloadPlan, receipts)
	if err != nil {
		return ContainerShardSet{}, err
	}
	if err := set.Validate(); err != nil {
		return ContainerShardSet{}, fmt.Errorf("result receipt shard set: %w", err)
	}
	return set, nil
}

func storedResultReceiptShardSet(workloadPlan WorkloadExecutionPlan, receipts []ContainerShardReceipt, plan GatePlan) (ContainerShardSet, error) {
	set, err := uncheckedResultReceiptShardSet(workloadPlan, receipts)
	if err != nil {
		return ContainerShardSet{}, err
	}
	if err := set.ValidateStored(plan); err != nil {
		return ContainerShardSet{}, fmt.Errorf("stored result receipt shard set: %w", err)
	}
	return set, nil
}

// uncheckedResultReceiptShardSet 将回执携带的冻结 workload plan 与观测分片绑定，尚未执行 canonical 校验。
func uncheckedResultReceiptShardSet(workloadPlan WorkloadExecutionPlan, receipts []ContainerShardReceipt) (ContainerShardSet, error) {
	if len(receipts) == 0 {
		return ContainerShardSet{}, errors.New("result receipt shard receipt count is invalid")
	}
	if len(workloadPlan.ExecutionWorkloadIDs) == 0 {
		return ContainerShardSet{}, errors.New("result receipt workload plan is required")
	}
	first := receipts[0].Shard
	if first.SchemaVersion != workloadContainerShardSchemaVersion || first.ShardsPerJob != len(receipts) {
		return ContainerShardSet{}, errors.New("result receipt shard receipt count does not match shards_per_job")
	}
	set := ContainerShardSet{
		Profile: first.Profile, PlanDigest: first.PlanDigest, SourceTreeSHA: first.SourceTreeSHA,
		AcceptedManifestDigest: first.AcceptedManifestDigest, AcceptedConfigDigest: first.AcceptedConfigDigest,
		ShardsPerJob: first.ShardsPerJob, WorkloadPlanDigest: workloadPlan.PlanDigest,
		CatalogDigest: workloadPlan.CatalogDigest, LedgerGeneration: workloadPlan.LedgerGeneration,
		WorkloadPlan: workloadPlan, Shards: make([]ContainerShard, len(receipts)),
	}
	for index, receipt := range receipts {
		set.Shards[index] = receipt.Shard
		set.Shards[index].GateIDs = slices.Clone(receipt.Shard.GateIDs)
	}
	return set, nil
}

// validateResultReceiptShardBinding 绑定 receipt、source、plan、image 与 shard profile。
func validateResultReceiptShardBinding(receipt ResultReceipt, set ContainerShardSet) error {
	if set.PlanDigest != receipt.PlanDigest || set.SourceTreeSHA != receipt.Source.SourceTreeSHA {
		return errors.New("result receipt shard plan or source identity drifted")
	}
	plan, err := BuildGatePlan(set.Profile, receipt.Source)
	if err != nil {
		return fmt.Errorf("rebuild result receipt plan: %w", err)
	}
	if plan.PlanDigest != receipt.PlanDigest || plan.PolicyDigest != receipt.PolicyDigest {
		return errors.New("result receipt canonical plan or policy digest drifted")
	}
	if set.AcceptedManifestDigest != receipt.Image.PlatformManifestDigest || set.AcceptedConfigDigest != receipt.Image.ConfigDigest {
		return errors.New("result receipt shard image identity drifted")
	}
	if receipt.Entrypoint == CIEntrypointRelease && set.Profile != ProfileRelease {
		return errors.New("release result receipt requires release shard profile")
	}
	return nil
}

// validateStoredResultReceiptShardBinding 将历史回执绑定到其原始计划、来源和镜像。
func validateStoredResultReceiptShardBinding(receipt ResultReceipt, set ContainerShardSet, plan GatePlan) error {
	if set.PlanDigest != receipt.PlanDigest || set.SourceTreeSHA != receipt.Source.SourceTreeSHA ||
		plan.PlanDigest != receipt.PlanDigest || plan.PolicyDigest != receipt.PolicyDigest {
		return errors.New("stored result receipt shard plan or source identity drifted")
	}
	if set.AcceptedManifestDigest != receipt.Image.PlatformManifestDigest || set.AcceptedConfigDigest != receipt.Image.ConfigDigest {
		return errors.New("stored result receipt shard image identity drifted")
	}
	if receipt.Entrypoint == CIEntrypointRelease && set.Profile != ProfileRelease {
		return errors.New("stored release result receipt requires release shard profile")
	}
	return nil
}

// aggregateStoredResultReceiptShards 按历史计划顺序聚合分片结果并保留 release 证据。
func aggregateStoredResultReceiptShards(
	receipt ResultReceipt,
	set ContainerShardSet,
	plan GatePlan,
) ([]PlanGateExecution, error) {
	indexed := make(map[string]ContainerShardReceipt, len(receipt.ShardReceipts))
	for _, shardReceipt := range receipt.ShardReceipts {
		if _, exists := indexed[shardReceipt.Shard.IdentityDigest]; exists {
			return nil, errors.New("stored container shard receipt is duplicated")
		}
		indexed[shardReceipt.Shard.IdentityDigest] = shardReceipt
	}
	results, err := collectStoredContainerShardResults(set, indexed, plan)
	if err != nil {
		return nil, err
	}
	ordered := make([]PlanGateExecution, 0, len(plan.Gates))
	for _, spec := range plan.Gates {
		if spec.ID == GateIDReleaseLayeredCheck {
			continue
		}
		execution, ok := results[spec.ID]
		if !ok {
			return nil, fmt.Errorf("stored container shard aggregate gate %q is missing", spec.ID)
		}
		ordered = append(ordered, execution)
	}
	if len(results) != len(ordered) {
		return nil, errors.New("stored container shard aggregate gate coverage is not exact")
	}
	if set.Profile == ProfileRelease {
		release := receipt.GateResults[len(receipt.GateResults)-1]
		ordered = append(ordered, PlanGateExecution{
			GateID: GateID(release.GateID), Status: ResultStatusPassed, ExitCode: release.ExitCode,
			StartedAt: release.StartedAt, CompletedAt: release.CompletedAt,
			ArgvDigest: release.ArgvDigest, LogDigest: release.LogDigest,
		})
	}
	return ordered, nil
}

// aggregateResultReceiptShards 以已签 release gate 时钟重放 canonical 聚合。
func aggregateResultReceiptShards(receipt ResultReceipt, set ContainerShardSet) ([]PlanGateExecution, error) {
	clock := func() time.Time { return time.Time{} }
	if set.Profile == ProfileRelease {
		if len(receipt.GateResults) == 0 || receipt.GateResults[len(receipt.GateResults)-1].GateID != string(GateIDReleaseLayeredCheck) {
			return nil, errors.New("release result receipt is missing final release attestation")
		}
		release := receipt.GateResults[len(receipt.GateResults)-1]
		calls := 0
		clock = func() time.Time {
			calls++
			if calls == 1 {
				return release.StartedAt
			}
			return release.CompletedAt
		}
	}
	return aggregateContainerShardsWithClock(set, receipt.ShardReceipts, clock)
}

// validateResultReceiptGateAggregate 要求签名 GateResults 是 shard 聚合的精确投影。
func validateResultReceiptGateAggregate(results []GateResult, aggregated []PlanGateExecution) error {
	if len(results) != len(aggregated) {
		return errors.New("result receipt gate aggregate count drifted")
	}
	for index, execution := range aggregated {
		expected := GateResult{
			GateID: string(execution.GateID), Status: GateStatusPassed, ExitCode: execution.ExitCode,
			StartedAt: execution.StartedAt, CompletedAt: execution.CompletedAt,
			ArgvDigest: execution.ArgvDigest, LogDigest: execution.LogDigest,
		}
		if execution.Status != ResultStatusPassed || results[index] != expected {
			return fmt.Errorf("result receipt gate aggregate %d drifted", index)
		}
	}
	return nil
}

// validateResultReceiptShardTimeline 绑定 receipt 顶层时钟与全部分片观测边界。
func validateResultReceiptShardTimeline(receipt ResultReceipt, shards []ContainerShardReceipt) error {
	var startedAt, completedAt, deadline time.Time
	for _, shard := range shards {
		if startedAt.IsZero() || shard.StartedAt.Before(startedAt) {
			startedAt = shard.StartedAt
		}
		if shard.CompletedAt.After(completedAt) {
			completedAt = shard.CompletedAt
		}
		if err := claimShardDeadline(&deadline, shard.Deadline); err != nil {
			return err
		}
	}
	if !receipt.StartedAt.Equal(startedAt) || !receipt.CompletedAt.Equal(completedAt) || !receipt.Deadline.Equal(deadline) {
		return errors.New("result receipt shard timeline drifted")
	}
	return nil
}

// aggregateResultReceiptContainer 复算 owner 使用的全部容器规范摘要证据。
func aggregateResultReceiptContainer(receipts []ContainerShardReceipt) (ContainerEvidence, error) {
	containers := make([]ContainerEvidence, len(receipts))
	for index, receipt := range receipts {
		containers[index] = receipt.Container
		if err := containers[index].Validate(); err != nil {
			return ContainerEvidence{}, fmt.Errorf("result receipt shard container %d: %w", index, err)
		}
		if index > 0 && (containers[index].ResourceWitness != containers[0].ResourceWitness ||
			containers[index].ResourceWitnessDigest != containers[0].ResourceWitnessDigest) {
			return ContainerEvidence{}, errors.New("result receipt shard resource witnesses differ")
		}
	}
	allDigest, err := resultReceiptEvidenceDigest(containers)
	if err != nil {
		return ContainerEvidence{}, err
	}
	hostDigests := make([]string, len(containers))
	networkDigests := make([]string, len(containers))
	for index, container := range containers {
		hostDigests[index] = container.HostConfigDigest
		networkDigests[index] = container.NetworkPolicyDigest
	}
	hostDigest, err := resultReceiptEvidenceDigest(hostDigests)
	if err != nil {
		return ContainerEvidence{}, err
	}
	networkDigest, err := resultReceiptEvidenceDigest(networkDigests)
	if err != nil {
		return ContainerEvidence{}, err
	}
	return ContainerEvidence{
		ContainerID: "aggregate:" + allDigest, NetworkID: "aggregate:" + allDigest,
		HostConfigDigest: hostDigest,
		ResourceWitness:  containers[0].ResourceWitness, ResourceWitnessDigest: containers[0].ResourceWitnessDigest,
		NetworkPolicyDigest: networkDigest, Removed: true, NetworkRemoved: true,
	}, nil
}

func resultReceiptEvidenceDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("marshal result receipt evidence: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}
