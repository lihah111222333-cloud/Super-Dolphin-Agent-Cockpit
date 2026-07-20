package gate

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

const (
	// MaxContainerShards is both the canonical invocation cap and the scheduler gang size.
	MaxContainerShards                 = 3
	containerShardSchemaVersion uint32 = 1
	containerShardCPUNanos      int64  = 4_000_000_000
	containerShardMemoryBytes   int64  = 8 << 30
	containerShardPIDs          int64  = 512
)

// ContainerShard binds one disposable container to an exact subset of a canonical plan.
// It deliberately excludes release:ci-l3: only the trusted owner may aggregate it.
type ContainerShard struct {
	SchemaVersion          uint32   `json:"schema_version"`
	Index                  uint8    `json:"index"`
	IdentityDigest         string   `json:"identity_digest"`
	Profile                Profile  `json:"profile"`
	PlanDigest             string   `json:"plan_digest"`
	SourceTreeSHA          string   `json:"source_tree_sha"`
	AcceptedManifestDigest string   `json:"accepted_manifest_digest"`
	AcceptedConfigDigest   string   `json:"accepted_config_digest"`
	GateIDs                []GateID `json:"gate_ids"`
}

// ContainerShardSet is the canonical group reservation required before any shard starts.
type ContainerShardSet struct {
	Profile                Profile
	PlanDigest             string
	SourceTreeSHA          string
	AcceptedManifestDigest string
	AcceptedConfigDigest   string
	Shards                 []ContainerShard
}

// ContainerShardReceipt is unsigned, directly observed worker evidence for trusted aggregation.
type ContainerShardReceipt struct {
	Shard                 ContainerShard           `json:"shard"`
	Status                ResultStatus             `json:"status"`
	GateExecutions        []PlanGateExecution      `json:"gate_executions"`
	ContainerID           string                   `json:"container_id"`
	Container             ContainerEvidence        `json:"container"`
	ResourceWitness       ContainerResourceWitness `json:"resource_witness"`
	ResourceWitnessDigest string                   `json:"resource_witness_digest"`
	Removed               bool                     `json:"removed"`
	RemovalProofDigest    string                   `json:"removal_proof_digest"`
	StartedAt             time.Time                `json:"started_at"`
	ExitedAt              time.Time                `json:"exited_at"`
	CompletedAt           time.Time                `json:"completed_at"`
	Deadline              time.Time                `json:"deadline"`
}

// ContainerShardRunner executes exactly one already-validated disposable-container shard.
type ContainerShardRunner func(context.Context, ContainerShard) (ContainerShardReceipt, error)

// BuildContainerShardSet 将单次 canonical invocation 固定切成至多三个容器工作组。
func BuildContainerShardSet(plan GatePlan, acceptedManifestDigest string, acceptedConfigDigest string) (ContainerShardSet, error) {
	prerequisites, err := containerShardPrerequisites(plan, acceptedManifestDigest, acceptedConfigDigest)
	if err != nil {
		return ContainerShardSet{}, err
	}
	set := ContainerShardSet{
		Profile: plan.Profile, PlanDigest: plan.PlanDigest, SourceTreeSHA: plan.Source.SourceTreeSHA,
		AcceptedManifestDigest: acceptedManifestDigest, AcceptedConfigDigest: acceptedConfigDigest,
	}
	if err := appendContainerShards(&set, partitionContainerShardGates(prerequisites)); err != nil {
		return ContainerShardSet{}, err
	}
	if err := set.Validate(); err != nil {
		return ContainerShardSet{}, err
	}
	return set, nil
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

func appendContainerShards(set *ContainerShardSet, groups [][]GateID) error {
	for _, gates := range groups {
		if len(gates) == 0 {
			continue
		}
		shard := ContainerShard{SchemaVersion: containerShardSchemaVersion, Index: uint8(len(set.Shards)), Profile: set.Profile, PlanDigest: set.PlanDigest, SourceTreeSHA: set.SourceTreeSHA, AcceptedManifestDigest: set.AcceptedManifestDigest, AcceptedConfigDigest: set.AcceptedConfigDigest, GateIDs: slices.Clone(gates)}
		identity, err := containerShardIdentityDigest(shard)
		if err != nil {
			return err
		}
		shard.IdentityDigest = identity
		set.Shards = append(set.Shards, shard)
	}
	return nil
}

func gateIDsForPlan(plan GatePlan) []GateID {
	ids := make([]GateID, len(plan.Gates))
	for index, spec := range plan.Gates {
		ids[index] = spec.ID
	}
	return ids
}

// partitionContainerShardGates 根据实测长尾把 normal 与 release 固定映射到三个容器。
// Frontend 与受限并发的 Go/LSP 分开承担长尾，治理检查使用第三个容器。
func partitionContainerShardGates(ids []GateID) [][]GateID {
	groups := make([][]GateID, MaxContainerShards)
	for _, id := range ids {
		switch id {
		case GateIDAIMaintenanceSelfTest, GateIDFrontendLint, GateIDFrontendTest, GateIDFrontendBuild, GateIDFrontendEmbedVerify:
			groups[0] = append(groups[0], id)
		case GateIDBackendTestWithGuard, GateIDLSPChangedDiagnostics, GateIDBackendTestGuardWithRace, GateIDBackendNilness:
			groups[1] = append(groups[1], id)
		default:
			groups[2] = append(groups[2], id)
		}
	}
	return groups
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
	expected := canonicalContainerShardGroups(set.Profile)
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
	if len(set.Shards) == 0 || len(set.Shards) > MaxContainerShards {
		return fmt.Errorf("container shard count %d is outside 1..%d", len(set.Shards), MaxContainerShards)
	}
	return nil
}

// validateContainerShard 验证单 shard 不能改变 group 绑定或把 release gate 交给 worker。
func validateContainerShard(set ContainerShardSet, shard ContainerShard, index int) error {
	if shard.Index != uint8(index) || shard.SchemaVersion != containerShardSchemaVersion {
		return errors.New("container shard identity binding drifted")
	}
	if err := validateContainerShardBinding(set, shard); err != nil {
		return err
	}
	identity, err := containerShardIdentityDigest(shard)
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
	if shard.Profile != set.Profile || shard.PlanDigest != set.PlanDigest || shard.SourceTreeSHA != set.SourceTreeSHA {
		return errors.New("container shard identity binding drifted")
	}
	if shard.AcceptedManifestDigest != set.AcceptedManifestDigest || shard.AcceptedConfigDigest != set.AcceptedConfigDigest {
		return errors.New("container shard identity binding drifted")
	}
	return nil
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

func containerShardIdentityDigest(shard ContainerShard) (string, error) {
	material := struct {
		SchemaVersion          uint32
		Index                  uint8
		Profile                Profile
		PlanDigest             string
		SourceTreeSHA          string
		AcceptedManifestDigest string
		AcceptedConfigDigest   string
		GateIDs                []GateID
	}{shard.SchemaVersion, shard.Index, shard.Profile, shard.PlanDigest, shard.SourceTreeSHA,
		shard.AcceptedManifestDigest, shard.AcceptedConfigDigest, shard.GateIDs}
	encoded, err := json.Marshal(material)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// RunContainerShards 在已完整 reserve 的 gang 内并发启动，任一失败立即取消同伴。
func RunContainerShards(ctx context.Context, set ContainerShardSet, run ContainerShardRunner) ([]ContainerShardReceipt, error) {
	if ctx == nil || run == nil {
		return nil, errors.New("container shard context and runner are required")
	}
	if err := set.Validate(); err != nil {
		return nil, err
	}
	group, shardCtx := errgroup.WithContext(ctx)
	receipts := make([]ContainerShardReceipt, len(set.Shards))
	var receiptsMu sync.Mutex
	for index, shard := range set.Shards {
		group.Go(func() error {
			receipt, err := run(shardCtx, shard)
			receiptsMu.Lock()
			receipts[index] = receipt
			receiptsMu.Unlock()
			return err
		})
	}
	return receipts, group.Wait()
}

// AggregateContainerShards 只在可信 owner 检查到全部前序 shard 后生成 release:ci-l3。
func AggregateContainerShards(set ContainerShardSet, receipts []ContainerShardReceipt) ([]PlanGateExecution, error) {
	return aggregateContainerShardsWithClock(set, receipts, time.Now)
}

// AggregateContainerShardFailureEvidence preserves exact worker evidence in canonical plan order without minting release authority.
// 按规范计划顺序汇总各分片的终态失败证据；缺少、伪造、重复或跨分片时限不一致即失败。
func AggregateContainerShardFailureEvidence(set ContainerShardSet, receipts []ContainerShardReceipt) ([]PlanGateExecution, error) {
	indexed, err := indexContainerShardReceipts(set, receipts)
	if err != nil {
		return nil, err
	}
	results := make(map[GateID]PlanGateExecution)
	var deadline time.Time
	for _, shard := range set.Shards {
		receipt, exists := indexed[shard.IdentityDigest]
		if !exists || !equalContainerShard(receipt.Shard, shard) {
			return nil, errors.New("container shard failure receipt identity is missing or forged")
		}
		if err := validateShardTerminalEvidence(shard, receipt); err != nil {
			return nil, err
		}
		if err := claimShardDeadline(&deadline, receipt.Deadline); err != nil {
			return nil, err
		}
		for _, execution := range receipt.GateExecutions {
			if _, duplicate := results[execution.GateID]; duplicate {
				return nil, fmt.Errorf("gate %q has duplicate failure receipt coverage", execution.GateID)
			}
			results[execution.GateID] = execution
		}
	}
	return orderedContainerShardResults(set, results)
}

func aggregateContainerShardsWithClock(set ContainerShardSet, receipts []ContainerShardReceipt, now func() time.Time) ([]PlanGateExecution, error) {
	if now == nil {
		return nil, errors.New("container shard aggregation clock is required")
	}
	byIdentity, err := indexContainerShardReceipts(set, receipts)
	if err != nil {
		return nil, err
	}
	results, err := collectContainerShardResults(set, byIdentity)
	if err != nil {
		return nil, err
	}
	ordered, err := orderedContainerShardResults(set, results)
	if err != nil {
		return nil, err
	}
	return appendReleaseShardAggregation(set, ordered, results, now)
}

func indexContainerShardReceipts(set ContainerShardSet, receipts []ContainerShardReceipt) (map[string]ContainerShardReceipt, error) {
	if err := set.Validate(); err != nil {
		return nil, err
	}
	if len(receipts) != len(set.Shards) {
		return nil, errors.New("container shard receipt count is incomplete")
	}
	indexed := make(map[string]ContainerShardReceipt, len(receipts))
	for _, receipt := range receipts {
		if _, exists := indexed[receipt.Shard.IdentityDigest]; exists {
			return nil, errors.New("container shard receipt is duplicated")
		}
		indexed[receipt.Shard.IdentityDigest] = receipt
	}
	return indexed, nil
}

// collectContainerShardResults 校验每个预期 shard，并建立唯一 gate 结果集合。
func collectContainerShardResults(set ContainerShardSet, indexed map[string]ContainerShardReceipt) (map[GateID]PlanGateExecution, error) {
	results := make(map[GateID]PlanGateExecution)
	var deadline time.Time
	for _, shard := range set.Shards {
		receipt, exists := indexed[shard.IdentityDigest]
		if !exists || !equalContainerShard(receipt.Shard, shard) {
			return nil, errors.New("container shard receipt identity is missing or forged")
		}
		if err := validateContainerShardReceipt(shard, receipt); err != nil {
			return nil, err
		}
		if err := claimShardDeadline(&deadline, receipt.Deadline); err != nil {
			return nil, err
		}
		for _, execution := range receipt.GateExecutions {
			if _, exists := results[execution.GateID]; exists {
				return nil, fmt.Errorf("gate %q has duplicate worker receipt coverage", execution.GateID)
			}
			results[execution.GateID] = execution
		}
	}
	return results, nil
}

func claimShardDeadline(deadline *time.Time, candidate time.Time) error {
	if deadline.IsZero() {
		*deadline = candidate
		return nil
	}
	if !deadline.Equal(candidate) {
		return errors.New("container shard deadlines differ")
	}
	return nil
}

func orderedContainerShardResults(set ContainerShardSet, results map[GateID]PlanGateExecution) ([]PlanGateExecution, error) {
	gateIDs, _, err := planExecutionPrerequisites(executorPlanRequest{
		profile: set.Profile, planDigest: set.PlanDigest, gateIDs: requiredGateIDs(set.Profile),
	})
	if err != nil {
		return nil, err
	}
	if len(results) != len(gateIDs) {
		return nil, errors.New("container shard aggregate gate coverage is not exact")
	}
	ordered := make([]PlanGateExecution, 0, len(results)+1)
	for _, id := range gateIDs {
		result, exists := results[id]
		if !exists {
			return nil, fmt.Errorf("container shard aggregate gate %q is missing", id)
		}
		ordered = append(ordered, result)
	}
	return ordered, nil
}

func appendReleaseShardAggregation(
	set ContainerShardSet,
	ordered []PlanGateExecution,
	observed map[GateID]PlanGateExecution,
	now func() time.Time,
) ([]PlanGateExecution, error) {
	if set.Profile != ProfileRelease {
		return ordered, nil
	}
	request := executorPlanRequest{profile: set.Profile, planDigest: set.PlanDigest, gateIDs: requiredGateIDs(set.Profile)}
	attestation, err := executeReleaseLayerAttestation(request, observed, now)
	if err != nil {
		return nil, fmt.Errorf("aggregate release container shards: %w", err)
	}
	return append(ordered, attestation), nil
}

// equalContainerShard 比较回执中的完整 canonical shard 绑定，而非只信任 digest 字段。
func equalContainerShard(left, right ContainerShard) bool {
	return left.SchemaVersion == right.SchemaVersion && left.Index == right.Index && left.IdentityDigest == right.IdentityDigest &&
		left.Profile == right.Profile && left.PlanDigest == right.PlanDigest && left.SourceTreeSHA == right.SourceTreeSHA &&
		left.AcceptedManifestDigest == right.AcceptedManifestDigest && left.AcceptedConfigDigest == right.AcceptedConfigDigest &&
		slices.Equal(left.GateIDs, right.GateIDs)
}

// validateContainerShardReceipt 校验单 shard 的移除、时钟、资源和精确 gate 证明。
func validateContainerShardReceipt(shard ContainerShard, receipt ContainerShardReceipt) error {
	if err := validateShardRemoval(receipt); err != nil {
		return err
	}
	if err := validateShardTimeline(receipt); err != nil {
		return err
	}
	if err := validateShardResources(receipt); err != nil {
		return err
	}
	return validateShardGateExecutions(shard, receipt.GateExecutions)
}

func validateShardTerminalEvidence(shard ContainerShard, receipt ContainerShardReceipt) error {
	if err := validateShardRemovalEvidence(receipt); err != nil {
		return err
	}
	if err := validateShardTimeline(receipt); err != nil {
		return err
	}
	if err := validateShardResources(receipt); err != nil {
		return err
	}
	return validateShardFailureGateExecutions(shard, receipt.GateExecutions)
}

// validateShardRemovalEvidence 校验失败回执仍提供可核验的容器移除证明。
func validateShardRemovalEvidence(receipt ContainerShardReceipt) error {
	if receipt.ContainerID == "" || !receipt.Removed || !digestPattern.MatchString(receipt.RemovalProofDigest) {
		return errors.New("container shard failure receipt did not prove removal")
	}
	if receipt.Container.ContainerID != receipt.ContainerID || !receipt.Container.Removed || !receipt.Container.NetworkRemoved {
		return errors.New("container shard failure container evidence is incomplete")
	}
	return nil
}

// validateShardFailureGateExecutions 校验失败回执完整保留每个分片 gate 的终态证据。
func validateShardFailureGateExecutions(shard ContainerShard, executions []PlanGateExecution) error {
	if len(executions) != len(shard.GateIDs) {
		return errors.New("container shard failure gate coverage is incomplete")
	}
	for index, id := range shard.GateIDs {
		execution := executions[index]
		if execution.GateID != id || !validPlanGateExit(execution.Status, execution.ExitCode) ||
			execution.StartedAt.IsZero() || execution.CompletedAt.Before(execution.StartedAt) ||
			execution.LogDigest != digestPlanLog(execution.Log) {
			return fmt.Errorf("container shard failure gate %q is invalid", id)
		}
	}
	return nil
}

// 校验通过分片的回执同时证明容器和网络已删除；删除状态、摘要格式或容器证据缺失即失败。
func validateShardRemoval(receipt ContainerShardReceipt) error {
	if receipt.Status != ResultStatusPassed || receipt.ContainerID == "" || !receipt.Removed || !digestPattern.MatchString(receipt.RemovalProofDigest) {
		return errors.New("container shard receipt did not prove successful removal")
	}
	if receipt.Container.ContainerID != receipt.ContainerID || !receipt.Container.Removed || !receipt.Container.NetworkRemoved {
		return errors.New("container shard receipt container evidence is incomplete")
	}
	return nil
}

// validateShardTimeline 区分容器退出时刻与证据收尾时刻，并按终态约束 deadline 关系。
func validateShardTimeline(receipt ContainerShardReceipt) error {
	if err := validateShardClockOrder(receipt); err != nil {
		return err
	}
	return validateShardDeadlineRelation(receipt)
}

// validateShardClockOrder 拒绝零值及执行、退出、证据收尾的时钟倒序。
func validateShardClockOrder(receipt ContainerShardReceipt) error {
	if receipt.StartedAt.IsZero() || receipt.ExitedAt.IsZero() || receipt.CompletedAt.IsZero() ||
		!receipt.Deadline.After(receipt.StartedAt) || receipt.ExitedAt.Before(receipt.StartedAt) || receipt.CompletedAt.Before(receipt.ExitedAt) {
		return errors.New("container shard receipt timing is invalid")
	}
	return nil
}

// validateShardDeadlineRelation 按终态校验受信退出时刻与执行期限的关系。
func validateShardDeadlineRelation(receipt ContainerShardReceipt) error {
	switch receipt.Status {
	case ResultStatusPassed:
		if receipt.ExitedAt.After(receipt.Deadline) {
			return errors.New("passed container shard exited after deadline")
		}
	case ResultStatusTimeout:
		if receipt.ExitedAt.Before(receipt.Deadline) {
			return errors.New("timed out container shard exited before deadline")
		}
	case ResultStatusFailed:
		if receipt.ExitedAt.After(receipt.Deadline) {
			return errors.New("failed container shard exited after deadline")
		}
	case ResultStatusCancelled, ResultStatusInfraFailed:
		// 取消与基础设施失败可能发生在执行期限的任一侧。
	default:
		return errors.New("container shard receipt status is not terminal")
	}
	return nil
}

// validateShardResources 固定每个 disposable container 的 4 CPU、8 GiB 和 512 PIDs witness。
func validateShardResources(receipt ContainerShardReceipt) error {
	witness := receipt.ResourceWitness
	if witness.SchemaVersion != ContainerResourceWitnessSchemaVersion || witness.NanoCPUs != containerShardCPUNanos || witness.MemoryBytes != containerShardMemoryBytes || witness.PidsLimit != containerShardPIDs || receipt.ResourceWitnessDigest != containerShardWitnessDigest(witness) {
		return errors.New("container shard receipt resource witness is invalid")
	}
	return nil
}

// validateShardGateExecutions 要求 worker 回执恰好覆盖其 canonical gate 序列。
func validateShardGateExecutions(shard ContainerShard, executions []PlanGateExecution) error {
	if len(executions) != len(shard.GateIDs) {
		return errors.New("container shard receipt gate coverage is incomplete")
	}
	for index, id := range shard.GateIDs {
		if err := validateShardGateExecution(shard.Profile, id, executions[index]); err != nil {
			return err
		}
	}
	return nil
}

// validateShardGateExecution 校验单个 passed gate 的观测结果与 canonical argv 绑定。
func validateShardGateExecution(profile Profile, id GateID, execution PlanGateExecution) error {
	argvDigest, err := canonicalGateArgvDigest(profile, id)
	if err != nil {
		return err
	}
	if !validPassedShardGateExecution(id, execution) {
		return fmt.Errorf("container shard receipt gate %q is invalid", id)
	}
	if execution.ArgvDigest != argvDigest {
		return fmt.Errorf("container shard receipt gate %q argv digest drifted", id)
	}
	return nil
}

// validPassedShardGateExecution 校验 passed gate 的身份、终态时钟与原始日志摘要。
func validPassedShardGateExecution(id GateID, execution PlanGateExecution) bool {
	return execution.GateID == id && execution.Status == ResultStatusPassed && execution.ExitCode == 0 &&
		!execution.StartedAt.IsZero() && !execution.CompletedAt.Before(execution.StartedAt) &&
		execution.LogDigest == digestPlanLog(execution.Log)
}

func containerShardWitnessDigest(witness ContainerResourceWitness) string {
	encoded, _ := json.Marshal(witness)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", sum)
}

// executorPlanLanes 将 exact gate 集合映射到固定、互不共享可写目录的 lane DAG。
