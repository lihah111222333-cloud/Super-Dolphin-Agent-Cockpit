package remoteci

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// remoteWorkloadPassIdentities 为当前非校准 catalog 建立可跨 agent 查询的 PASS 身份。
func remoteWorkloadPassIdentities(ctx context.Context, input RunInput, catalog gate.WorkloadCatalog, workerTimeout time.Duration) ([]gate.WorkloadPassIdentity, error) {
	workloads := remoteShardableWorkloads(catalog)
	inputDigests, err := remoteWorkloadInputDigests(ctx, input.RepositoryRoot, input.Tree, workloads)
	if err != nil {
		return nil, err
	}
	environmentDigest, err := remoteWorkloadEnvironmentDigest(input, workerTimeout)
	if err != nil {
		return nil, err
	}
	identities := make([]gate.WorkloadPassIdentity, 0, len(workloads))
	for _, workload := range workloads {
		inputDigest, ok := inputDigests[workload.ID]
		if !ok || strings.TrimSpace(inputDigest) == "" {
			return nil, fmt.Errorf("remote workload %q input digest is required", workload.ID)
		}
		identity := gate.WorkloadPassIdentity{
			WorkloadID:        gate.GateID(workload.ID),
			ExecutionDigest:   remoteWorkloadExecutionDigest(workload),
			InputDigest:       inputDigest,
			EnvironmentDigest: environmentDigest,
		}
		identity.IdentityDigest, err = gate.WorkloadPassIdentitySHA256(identity)
		if err != nil {
			return nil, fmt.Errorf("digest remote workload %q pass identity: %w", workload.ID, err)
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

type remoteWorkloadEnvironment struct {
	SchemaVersion                string `json:"schema_version"`
	Platform                     string `json:"platform"`
	PolicyDigest                 string `json:"policy_digest"`
	ToolchainDigest              string `json:"toolchain_digest"`
	CandidateGateSourceSHA256    string `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string `json:"candidate_gate_toolchain_sha256"`
	RuntimeSeedSHA256            string `json:"runtime_seed_sha256"`
	WorkerTimeout                string `json:"worker_timeout"`
}

// remoteWorkloadEnvironmentDigest 只绑定执行语义；job、agent、accepted generation 与 snapshot 均为本次审计字段，不能阻断跨 agent 复用。
func remoteWorkloadEnvironmentDigest(input RunInput, workerTimeout time.Duration) (string, error) {
	if err := gate.ValidateExecutorWorkloadTimeout(workerTimeout); err != nil {
		return "", fmt.Errorf("validate remote workload environment timeout: %w", err)
	}
	payload, err := json.Marshal(remoteWorkloadEnvironment{
		SchemaVersion: "remote-workload-pass-environment/v2", Platform: input.Platform,
		PolicyDigest: input.PolicyDigest, ToolchainDigest: input.ToolchainDigest,
		CandidateGateSourceSHA256:    input.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, RuntimeSeedSHA256: input.RuntimeSeedSHA256,
		WorkerTimeout: workerTimeout.String(),
	})
	if err != nil {
		return "", fmt.Errorf("encode remote workload environment: %w", err)
	}
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum), nil
}

func remoteWorkloadExecutionDigest(workload gate.Workload) string {
	sum := sha256.Sum256([]byte("remote-workload-execution/v1\x00" + workload.ID + "\x00" + workload.CommandDigest))
	return fmt.Sprintf("sha256:%x", sum)
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
	enabled         bool
}

// prepareRemoteWorkloadReuse 在临时目录、OSS 或 ECI 操作之前完成 PASS 查询与 miss 投影。
// 校准运行始终使用完整 catalog，绝不复用任何既有 PASS。
func prepareRemoteWorkloadReuse(
	ctx context.Context,
	input RunInput,
	catalog gate.WorkloadCatalog,
	workerTimeout time.Duration,
) (remoteWorkloadReusePreparation, error) {
	preparation := remoteWorkloadReusePreparation{
		reused:  make(map[string]gate.WorkloadPassEvidence),
		enabled: !input.Calibration,
	}
	if input.Calibration {
		identities, err := remoteWorkloadPassIdentities(ctx, input, catalog, workerTimeout)
		if err != nil {
			return remoteWorkloadReusePreparation{}, err
		}
		preparation.identities = identities
		preparation.cacheMisses = remoteShardableWorkloadIDs(catalog)
		return preparation, nil
	}
	identities, err := remoteWorkloadPassIdentities(ctx, input, catalog, workerTimeout)
	if err != nil {
		return remoteWorkloadReusePreparation{}, err
	}
	reused, err := lookupRemoteWorkloadPasses(input.LedgerStore, identities)
	if err != nil {
		return remoteWorkloadReusePreparation{}, err
	}
	reusedWorkloads, cacheMisses := classifyRemoteWorkloadPasses(identities, reused)
	preparation.reused = reused
	preparation.identities = identities
	preparation.reusedWorkloads = reusedWorkloads
	preparation.cacheMisses = cacheMisses
	return preparation, nil
}

// remoteShardableWorkloadIDs 保持校准的完整执行投影，同时不为它建立可复用身份。
func remoteShardableWorkloadIDs(catalog gate.WorkloadCatalog) []gate.GateID {
	workloads := remoteShardableWorkloads(catalog)
	identifiers := make([]gate.GateID, 0, len(workloads))
	for _, workload := range workloads {
		identifiers = append(identifiers, gate.GateID(workload.ID))
	}
	return identifiers
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

// allReused 仅在非校准 workload 全部命中时允许在远程资源创建前结束运行。
func (preparation remoteWorkloadReusePreparation) allReused() bool {
	return preparation.enabled && len(preparation.cacheMisses) == 0
}

// completeRemoteReuse 将已严格验证的 origin PASS 投影为当前独立 job；不创建临时目录、OSS 对象或 ECI 分片。
func completeRemoteReuse(catalog gate.WorkloadCatalog, reused map[string]gate.WorkloadPassEvidence, result RunResult) (RunResult, error) {
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
