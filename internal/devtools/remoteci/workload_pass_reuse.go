package remoteci

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
)

// remoteWorkloadPassIdentities 为当前 catalog 建立可跨 agent 查询的 PASS 身份。
func remoteWorkloadPassIdentities(ctx context.Context, input RunInput, catalog gate.WorkloadCatalog, workerTimeout time.Duration, resourcePolicy shardresource.Policy) ([]gate.WorkloadPassIdentity, error) {
	workloads := remoteShardableWorkloads(catalog)
	inputDigests, err := remoteWorkloadPassInputDigests(ctx, input, workloads)
	if err != nil {
		return nil, err
	}
	environmentDigest, err := remoteWorkloadEnvironmentDigest(input, workerTimeout, resourcePolicy)
	if err != nil {
		return nil, err
	}
	identities := make([]gate.WorkloadPassIdentity, 0, len(workloads))
	for _, workload := range workloads {
		identity, err := remoteWorkloadPassIdentity(workload, inputDigests, environmentDigest)
		if err != nil {
			return nil, fmt.Errorf("digest remote workload %q pass identity: %w", workload.ID, err)
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

// remoteWorkloadPassInputDigests 复用 catalog 已绑定的输入摘要；缺失时才读取 exact tree 计算。
func remoteWorkloadPassInputDigests(ctx context.Context, input RunInput, workloads []gate.Workload) (map[string]string, error) {
	inputDigests := cloneRemoteWorkloadInputDigests(input.WorkloadInputDigests)
	missing := false
	for _, workload := range workloads {
		if strings.TrimSpace(workload.InputDigest) == "" && strings.TrimSpace(inputDigests[workload.ID]) == "" {
			missing = true
			break
		}
	}
	if !missing {
		return inputDigests, nil
	}
	computed, err := remoteWorkloadInputDigests(ctx, input.RepositoryRoot, input.Tree, workloads)
	if err != nil {
		return nil, err
	}
	if inputDigests == nil {
		inputDigests = make(map[string]string, len(computed))
	}
	for workloadID, digest := range computed {
		if inputDigests[workloadID] == "" {
			inputDigests[workloadID] = digest
		}
	}
	return inputDigests, nil
}

// remoteWorkloadPassIdentity 绑定单个 workload 的执行、输入和正确性环境摘要。
func remoteWorkloadPassIdentity(workload gate.Workload, inputDigests map[string]string, environmentDigest string) (gate.WorkloadPassIdentity, error) {
	inputDigest := workload.InputDigest
	if inputDigest == "" {
		inputDigest = inputDigests[workload.ID]
	}
	if strings.TrimSpace(inputDigest) == "" {
		return gate.WorkloadPassIdentity{}, errors.New("workload input digest is required")
	}
	identity := gate.WorkloadPassIdentity{
		WorkloadID: gate.GateID(workload.ID), ExecutionDigest: gate.WorkloadPassExecutionDigest(workload),
		InputDigest: inputDigest, EnvironmentDigest: environmentDigest,
	}
	digest, err := gate.WorkloadPassIdentitySHA256(identity)
	if err != nil {
		return gate.WorkloadPassIdentity{}, err
	}
	identity.IdentityDigest = digest
	return identity, nil
}

// bindRemoteWorkloadInputDigests 将 Prepare 计算的生产输入摘要绑定进唯一 workload catalog。
func bindRemoteWorkloadInputDigests(catalog gate.WorkloadCatalog, inputDigests map[string]string) (gate.WorkloadCatalog, error) {
	bound := catalog
	bound.Workloads = append([]gate.Workload(nil), catalog.Workloads...)
	for index := range bound.Workloads {
		workload := &bound.Workloads[index]
		if !workload.Shardable {
			continue
		}
		digest := strings.TrimSpace(inputDigests[workload.ID])
		if digest == "" {
			return gate.WorkloadCatalog{}, fmt.Errorf("remote workload %q input digest is required before planning", workload.ID)
		}
		if workload.InputDigest != "" && workload.InputDigest != digest {
			return gate.WorkloadCatalog{}, fmt.Errorf("remote workload %q input digest drifted", workload.ID)
		}
		workload.InputDigest = digest
	}
	if err := gate.ValidateWorkloadCatalog(bound); err != nil {
		return gate.WorkloadCatalog{}, fmt.Errorf("validate workload input digest binding: %w", err)
	}
	return bound, nil
}

func cloneRemoteWorkloadInputDigests(inputDigests map[string]string) map[string]string {
	if len(inputDigests) == 0 {
		return nil
	}
	clone := make(map[string]string, len(inputDigests))
	maps.Copy(clone, inputDigests)
	return clone
}

type remoteWorkloadEnvironment struct {
	SchemaVersion     string `json:"schema_version"`
	Platform          string `json:"platform"`
	PolicyDigest      string `json:"policy_digest"`
	ToolchainDigest   string `json:"toolchain_digest"`
	RuntimeSeedSHA256 string `json:"runtime_seed_sha256"`
}

// remoteWorkloadEnvironmentDigest 只绑定会改变 workload 正确性的执行语义；模式、资源档位、资源策略、终止预算和协调器源码仅影响本次运行，不能阻断 PASS 复用。
func remoteWorkloadEnvironmentDigest(input RunInput, workerTimeout time.Duration, resourcePolicy shardresource.Policy) (string, error) {
	if err := gate.ValidateExecutorWorkloadTimeout(workerTimeout); err != nil {
		return "", fmt.Errorf("validate remote workload environment timeout: %w", err)
	}
	if err := resourcePolicy.Validate(); err != nil {
		return "", fmt.Errorf("validate remote workload resource policy: %w", err)
	}
	payload, err := json.Marshal(remoteWorkloadEnvironment{
		SchemaVersion: "remote-workload-pass-environment/v7", Platform: input.Platform,
		PolicyDigest: input.PolicyDigest, ToolchainDigest: input.ToolchainDigest,
		RuntimeSeedSHA256: input.RuntimeSeedSHA256,
	})
	if err != nil {
		return "", fmt.Errorf("encode remote workload environment: %w", err)
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum), nil
}

// lookupRemoteWorkloadPasses 严格投影已提升证据；缺项是正常 cache miss，伪造或漂移证据立即阻断。
func lookupRemoteWorkloadPasses(store *gate.DurationLedgerStore, identities []gate.WorkloadPassIdentity) (map[string]gate.WorkloadPassEvidence, error) {
	evidences, err := store.LookupWorkloadPassEvidence(identities)
	if err != nil {
		return nil, err
	}
	wanted := remoteWorkloadPassIdentityIndex(identities)
	reused := make(map[string]gate.WorkloadPassEvidence, len(evidences))
	for _, evidence := range evidences {
		if err := validateRemoteWorkloadPassEvidence(evidence, wanted, reused); err != nil {
			return nil, err
		}
		reused[string(evidence.Identity.WorkloadID)] = evidence
	}
	return reused, nil
}

// remoteWorkloadPassIdentityIndex 按 workload 标识索引本次查询允许的 PASS 身份。
func remoteWorkloadPassIdentityIndex(identities []gate.WorkloadPassIdentity) map[string]gate.WorkloadPassIdentity {
	wanted := make(map[string]gate.WorkloadPassIdentity, len(identities))
	for _, identity := range identities {
		wanted[string(identity.WorkloadID)] = identity
	}
	return wanted
}

// validateRemoteWorkloadPassEvidence 确认证据身份、执行结果及摘要均可审计。
func validateRemoteWorkloadPassEvidence(
	evidence gate.WorkloadPassEvidence,
	wanted map[string]gate.WorkloadPassIdentity,
	reused map[string]gate.WorkloadPassEvidence,
) error {
	workloadID := string(evidence.Identity.WorkloadID)
	identity, ok := wanted[workloadID]
	if !ok || evidence.Identity != identity {
		return errors.New("remote workload PASS evidence identity is not requested")
	}
	if _, duplicate := reused[workloadID]; duplicate {
		return fmt.Errorf("remote workload PASS evidence %q is duplicated", evidence.Identity.WorkloadID)
	}
	if err := validateRemoteWorkloadPassExecution(evidence); err != nil {
		return err
	}
	expected, err := gate.WorkloadPassEvidenceSHA256(evidence)
	if err != nil {
		return err
	}
	if evidence.EvidenceSHA256 != expected {
		return fmt.Errorf("remote workload PASS evidence %q digest mismatch", evidence.Identity.WorkloadID)
	}
	return nil
}

// validateRemoteWorkloadPassExecution 拒绝缺失终态或非 PASS 的原始 workload 执行。
func validateRemoteWorkloadPassExecution(evidence gate.WorkloadPassEvidence) error {
	execution := evidence.OriginExecution
	if execution.GateID == evidence.Identity.WorkloadID &&
		execution.Status == gate.ResultStatusPassed &&
		execution.ExitCode == 0 &&
		!execution.StartedAt.IsZero() &&
		execution.CompletedAt.After(execution.StartedAt) {
		return nil
	}
	return fmt.Errorf("remote workload PASS evidence %q execution is not an auditable PASS", evidence.Identity.WorkloadID)
}

// remoteWorkloadReusePreparation 保留本次复用决策与严格 miss 标识投影。
type remoteWorkloadReusePreparation struct {
	reused          map[string]gate.WorkloadPassEvidence
	identities      []gate.WorkloadPassIdentity
	reusedWorkloads []gate.WorkloadPassEvidence
	cacheMisses     []gate.GateID
}

// prepareRemoteWorkloadReuse 在临时目录、OSS 或 ECI 操作之前完成 PASS 查询与 miss 投影。
func prepareRemoteWorkloadReuse(
	ctx context.Context,
	input RunInput,
	catalog gate.WorkloadCatalog,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
) (remoteWorkloadReusePreparation, error) {
	preparation := remoteWorkloadReusePreparation{
		reused: make(map[string]gate.WorkloadPassEvidence),
	}
	identities, err := remoteWorkloadPassIdentities(ctx, input, catalog, workerTimeout, resourcePolicy)
	if err != nil {
		return remoteWorkloadReusePreparation{}, err
	}
	reused := make(map[string]gate.WorkloadPassEvidence)
	if !input.Force {
		reused, err = lookupRemoteWorkloadPassesForInput(ctx, input, identities)
		if err != nil {
			return remoteWorkloadReusePreparation{}, err
		}
	}
	reusedWorkloads, cacheMisses := classifyRemoteWorkloadPasses(identities, reused)
	preparation.reused = reused
	preparation.identities = identities
	preparation.reusedWorkloads = reusedWorkloads
	preparation.cacheMisses = cacheMisses
	return preparation, nil
}

// classifyRemoteWorkloadPasses 以身份顺序投影已复用证据与需要新建分片的 workload。
func classifyRemoteWorkloadPasses(
	identities []gate.WorkloadPassIdentity,
	reused map[string]gate.WorkloadPassEvidence,
) ([]gate.WorkloadPassEvidence, []gate.GateID) {
	reusedWorkloads := make([]gate.WorkloadPassEvidence, 0, len(reused))
	cacheMisses := make([]gate.GateID, 0, len(identities)-len(reused))
	for _, identity := range identities {
		if evidence, ok := reused[string(identity.WorkloadID)]; ok {
			reusedWorkloads = append(reusedWorkloads, evidence)
			continue
		}
		cacheMisses = append(cacheMisses, identity.WorkloadID)
	}
	return reusedWorkloads, cacheMisses
}

// apply 将复用决策绑定到本次独立 job 的结果投影。
func (preparation remoteWorkloadReusePreparation) apply(result *RunResult) {
	result.WorkloadPassIdentities = preparation.identities
	result.ReusedWorkloads = preparation.reusedWorkloads
	result.CacheMissWorkloads = preparation.cacheMisses
}

// allReused 仅在 workload 全部命中时允许在远程资源创建前结束运行。
func (preparation remoteWorkloadReusePreparation) allReused() bool {
	return len(preparation.cacheMisses) == 0
}

// completeRemoteReuse 将已严格验证的 origin PASS 投影为当前独立 job；不创建临时目录、OSS 对象或 ECI 分片。
func completeRemoteReuse(catalog gate.WorkloadCatalog, reused map[string]gate.WorkloadPassEvidence, result RunResult, now func() time.Time) (RunResult, error) {
	observed := make(map[string]gate.PlanGateExecution, len(reused))
	for _, workload := range remoteShardableWorkloads(catalog) {
		evidence, ok := reused[workload.ID]
		if !ok {
			return result, fmt.Errorf("remote workload %q lacks PASS evidence", workload.ID)
		}
		observed[workload.ID] = evidence.OriginExecution
	}
	workloads, err := remoteWorkloadExecutions(catalog, observed)
	if err != nil {
		return result, err
	}
	executions, status, err := aggregateCatalogWorkloads(catalog, observed)
	if err != nil {
		return result, err
	}
	executions, status, err = appendRemoteOwnerAttestation(catalog, executions, status, result.PlanDigest, now)
	if err != nil {
		return result, err
	}
	if status != gate.ResultStatusPassed {
		return result, errors.New("reused remote workload evidence does not aggregate to PASS")
	}
	result.GateExecutions = executions
	result.WorkloadExecutions = workloads
	result.FreshWorkloadExecutions = nil
	result.Status = gate.ResultStatusPassed
	result.CleanupComplete = true
	return result, nil
}

func remoteCIWorkloadResults(result RunResult) []gate.RemoteCIWorkloadResult {
	results := make([]gate.RemoteCIWorkloadResult, 0, len(result.ReusedWorkloads)+len(result.FreshWorkloadExecutions))
	identities := make(map[gate.GateID]gate.WorkloadPassIdentity, len(result.WorkloadPassIdentities))
	for _, identity := range result.WorkloadPassIdentities {
		identities[identity.WorkloadID] = identity
	}
	for _, evidence := range result.ReusedWorkloads {
		results = append(results, gate.RemoteCIWorkloadResult{Identity: evidence.Identity, Disposition: gate.WorkloadDispositionReused, OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: evidence.OriginAcceptedGeneration, EvidenceSHA256: evidence.EvidenceSHA256})
	}
	for _, execution := range result.FreshWorkloadExecutions {
		identity, ok := identities[execution.GateID]
		if !ok {
			continue
		}
		results = append(results, gate.RemoteCIWorkloadResult{Identity: identity, Disposition: gate.WorkloadDispositionExecuted, OriginJobID: result.JobID, OriginAcceptedGeneration: result.AcceptedGeneration})
	}
	return results
}
