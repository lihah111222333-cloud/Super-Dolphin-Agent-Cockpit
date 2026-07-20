package gate

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// validatePassedShardReceipt 校验 v2 passed receipt 的三分片签名闭包。
func (r ResultReceipt) validatePassedShardReceipt() error {
	set, err := resultReceiptShardSet(r.ShardReceipts)
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

// resultReceiptShardSet 从签名回执重建唯一 canonical shard set。
func resultReceiptShardSet(receipts []ContainerShardReceipt) (ContainerShardSet, error) {
	if len(receipts) != MaxContainerShards {
		return ContainerShardSet{}, errors.New("result receipt requires exactly three shard receipts")
	}
	first := receipts[0].Shard
	set := ContainerShardSet{
		Profile: first.Profile, PlanDigest: first.PlanDigest, SourceTreeSHA: first.SourceTreeSHA,
		AcceptedManifestDigest: first.AcceptedManifestDigest, AcceptedConfigDigest: first.AcceptedConfigDigest,
		Shards: make([]ContainerShard, len(receipts)),
	}
	for index, receipt := range receipts {
		set.Shards[index] = receipt.Shard
		set.Shards[index].GateIDs = slices.Clone(receipt.Shard.GateIDs)
	}
	if err := set.Validate(); err != nil {
		return ContainerShardSet{}, fmt.Errorf("result receipt shard set: %w", err)
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

// validateResultReceiptShardTimeline 绑定 receipt 顶层时钟与三分片观测边界。
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

// aggregateResultReceiptContainer 复算 owner 使用的三容器规范摘要证据。
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
