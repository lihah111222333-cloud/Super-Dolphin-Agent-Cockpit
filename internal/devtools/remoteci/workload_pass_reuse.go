package remoteci

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
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

// remoteWorkloadPassInputDigests 只读取 Prepare 已绑定的生产输入摘要；缺失摘要直接阻断，禁止回退到 exact tree 重算。
func remoteWorkloadPassInputDigests(_ context.Context, input RunInput, workloads []gate.Workload) (map[string]string, error) {
	inputDigests := cloneRemoteWorkloadInputDigests(input.WorkloadInputDigests)
	for _, workload := range workloads {
		catalogDigest := strings.TrimSpace(workload.InputDigest)
		boundDigest := strings.TrimSpace(inputDigests[workload.ID])
		if catalogDigest != "" && boundDigest != "" && catalogDigest != boundDigest {
			return nil, fmt.Errorf("remote workload %q input digest drifted between catalog and run input", workload.ID)
		}
		if catalogDigest != "" {
			if inputDigests == nil {
				inputDigests = make(map[string]string, len(workloads))
			}
			inputDigests[workload.ID] = catalogDigest
			continue
		}
		if boundDigest == "" {
			return nil, fmt.Errorf("remote workload %q input digest is missing from bound catalog", workload.ID)
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
	SchemaVersion             string   `json:"schema_version"`
	Platform                  string   `json:"platform"`
	PolicyDigest              string   `json:"policy_digest"`
	ToolchainDigest           string   `json:"toolchain_digest"`
	RuntimeSeedSHA256         string   `json:"runtime_seed_sha256"`
	CGOEnabled                string   `json:"cgo_enabled"`
	GOOS                      string   `json:"goos"`
	GOARCH                    string   `json:"goarch"`
	SemanticEnvironmentSchema string   `json:"semantic_environment_schema"`
	SemanticEnvironment       []string `json:"semantic_environment"`
	WorkerExecutionProvenance string   `json:"worker_execution_provenance"`
}

// remoteWorkloadEnvironmentDigest 计算 worker 的稳定语义环境摘要；worker
// contract/provenance 由 canonical cicontract owner 提供，candidate source、job、
// agent、资源与 cache 路径不进入 PASS identity。
func remoteWorkloadEnvironmentDigest(input RunInput, workerTimeout time.Duration, resourcePolicy shardresource.Policy) (string, error) {
	if err := gate.ValidateExecutorWorkloadTimeout(workerTimeout); err != nil {
		return "", fmt.Errorf("validate remote workload environment timeout: %w", err)
	}
	if err := resourcePolicy.Validate(); err != nil {
		return "", fmt.Errorf("validate remote workload resource policy: %w", err)
	}
	semanticEnvironment := cicontract.CanonicalWorkerExecutionEnvironment()
	semanticEnvironment = append([]string(nil), semanticEnvironment...)
	sort.Strings(semanticEnvironment)
	semanticValues, err := remoteWorkerSemanticEnvironmentValues(semanticEnvironment)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(remoteWorkloadEnvironment{
		SchemaVersion:             "remote-workload-pass-environment/v8",
		Platform:                  input.Platform,
		PolicyDigest:              input.PolicyDigest,
		ToolchainDigest:           input.ToolchainDigest,
		RuntimeSeedSHA256:         input.RuntimeSeedSHA256,
		CGOEnabled:                semanticValues["CGO_ENABLED"],
		GOOS:                      semanticValues["GOOS"],
		GOARCH:                    semanticValues["GOARCH"],
		SemanticEnvironmentSchema: cicontract.WorkerExecutionEnvironmentSchemaVersion,
		SemanticEnvironment:       semanticEnvironment,
		WorkerExecutionProvenance: cicontract.WorkerExecutionProvenanceID,
	})
	if err != nil {
		return "", fmt.Errorf("encode remote workload environment: %w", err)
	}
	return remoteWorkloadEnvironmentSHA256(payload), nil
}

func remoteWorkloadEnvironmentSHA256(payload []byte) string {
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", sum)
}

// remoteWorkerSemanticEnvironmentValues 将 canonical owner 的 one-hot env
// 投影为摘要中的显式平台字段；重复、缺失或 malformed assignment 均阻断。
func remoteWorkerSemanticEnvironmentValues(environment []string) (map[string]string, error) {
	values := make(map[string]string, len(environment))
	seen := make(map[string]struct{}, len(environment))
	for _, assignment := range environment {
		key, value, ok := strings.Cut(assignment, "=")
		if !ok || strings.TrimSpace(key) == "" || strings.Contains(key, "=") {
			return nil, fmt.Errorf("worker semantic environment assignment %q is malformed or duplicated", assignment)
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("worker semantic environment assignment %q is malformed or duplicated", assignment)
		}
		seen[key] = struct{}{}
		values[key] = value
	}
	for _, key := range []string{"CGO_ENABLED", "GOOS", "GOARCH"} {
		if strings.TrimSpace(values[key]) == "" {
			return nil, fmt.Errorf("worker semantic environment is missing %s", key)
		}
	}
	return values, nil
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

// validateRemoteWorkloadPassEvidence 只确认查询结果仍属于请求身份且没有重叠。
// LookupWorkloadPassEvidence 已在同一 SQLite 事务内完成 evidence 的完整
// Validate、来源 run、receipt、alias 与代际校验；这里不重复重算执行或摘要。
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
	return nil
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
		reused, err = lookupRemoteWorkloadPasses(input.LedgerStore, identities)
		if err != nil {
			return remoteWorkloadReusePreparation{}, err
		}
	}
	reusedWorkloads, cacheMisses, err := classifyRemoteWorkloadPassesStrict(identities, reused)
	if err != nil {
		return remoteWorkloadReusePreparation{}, fmt.Errorf("classify remote workload PASS reuse: %w", err)
	}
	effectiveReused, err := indexRemoteWorkloadPassEvidence(reusedWorkloads)
	if err != nil {
		return remoteWorkloadReusePreparation{}, fmt.Errorf("index effective remote workload PASS reuse: %w", err)
	}
	preparation.reused = effectiveReused
	preparation.identities = identities
	preparation.reusedWorkloads = reusedWorkloads
	preparation.cacheMisses = cacheMisses
	return preparation, nil
}

// indexRemoteWorkloadPassEvidence 只索引分类后仍然有效的复用证据。
// 同包出现 MISS 时，分类器会把该包全部降为 MISS，原始查询命中不得继续进入执行边界。
func indexRemoteWorkloadPassEvidence(evidences []gate.WorkloadPassEvidence) (map[string]gate.WorkloadPassEvidence, error) {
	indexed := make(map[string]gate.WorkloadPassEvidence, len(evidences))
	for _, evidence := range evidences {
		workloadID := string(evidence.Identity.WorkloadID)
		if workloadID == "" {
			return nil, errors.New("effective remote workload PASS identity is required")
		}
		if _, duplicate := indexed[workloadID]; duplicate {
			return nil, fmt.Errorf("effective remote workload PASS identity %q is duplicated", workloadID)
		}
		indexed[workloadID] = evidence
	}
	return indexed, nil
}

// remoteWorkloadPassPackageKey 返回同一测试二进制可能共享的包身份。
//
// 同包 selector 可能通过 TestMain/init、包级变量、fixture 或 t.Parallel
// 观察到其它 selector 的副作用；只有明确属于不同 parent（例如 normal/race）
// 或 target kind（例如 test/benchmark）的执行语义时才允许保持独立复用。任何无法由 canonical workload parser
// 闭合的目标都必须阻断，而不能退化为一个可复用的独立组。
func remoteWorkloadPassPackageKey(identity gate.WorkloadPassIdentity) (string, error) {
	parent, targetKind, target, targeted, err := gate.ParseWorkloadID(string(identity.WorkloadID))
	if err != nil {
		return "", fmt.Errorf("parse remote workload %q: %w", identity.WorkloadID, err)
	}
	if !targeted {
		return "", nil
	}
	var packageTarget string
	switch targetKind {
	case gate.WorkloadTargetGoTest:
		parsed, parseErr := gate.ParseGoTestTarget(target)
		if parseErr != nil {
			return "", fmt.Errorf("parse remote Go test workload %q: %w", identity.WorkloadID, parseErr)
		}
		packageTarget = parsed.Package
	case gate.WorkloadTargetGoBenchmark:
		parsed, parseErr := gate.ParseGoBenchmarkTarget(target)
		if parseErr != nil {
			return "", fmt.Errorf("parse remote Go benchmark workload %q: %w", identity.WorkloadID, parseErr)
		}
		packageTarget = parsed.Package
	case gate.WorkloadTargetGoPackage:
		if _, parseErr := gate.NewGoPackageWorkload(parent, target, 1); parseErr != nil {
			return "", fmt.Errorf("parse remote Go package workload %q: %w", identity.WorkloadID, parseErr)
		}
		packageTarget = target
	default:
		return "", nil
	}
	return string(parent) + "\x00" + string(targetKind) + "\x00" + packageTarget, nil
}

// classifyRemoteWorkloadPassesStrict 以身份顺序投影复用证据，并在同包命中/未命中
// 混合时将整个 package 降为 MISS，避免共享 test binary/batch 的状态副作用。
func classifyRemoteWorkloadPassesStrict(
	identities []gate.WorkloadPassIdentity,
	reused map[string]gate.WorkloadPassEvidence,
) ([]gate.WorkloadPassEvidence, []gate.GateID, error) {
	reusedWorkloads := make([]gate.WorkloadPassEvidence, 0, len(reused))
	cacheMisses := make([]gate.GateID, 0, len(identities)-len(reused))
	packageHits := make(map[string]struct{})
	packageMisses := make(map[string]struct{})
	packageKeys := make(map[string]string, len(identities))
	for _, identity := range identities {
		key, err := remoteWorkloadPassPackageKey(identity)
		if err != nil {
			return nil, nil, err
		}
		packageKeys[string(identity.WorkloadID)] = key
		if key == "" {
			continue
		}
		if _, ok := reused[string(identity.WorkloadID)]; ok {
			packageHits[key] = struct{}{}
		} else {
			packageMisses[key] = struct{}{}
		}
	}
	for _, identity := range identities {
		workloadID := string(identity.WorkloadID)
		packageKey := packageKeys[workloadID]
		_, mixedPackage := packageHits[packageKey]
		if mixedPackage {
			_, mixedPackage = packageMisses[packageKey]
		}
		if evidence, ok := reused[workloadID]; ok && !mixedPackage {
			reusedWorkloads = append(reusedWorkloads, evidence)
			continue
		}
		cacheMisses = append(cacheMisses, identity.WorkloadID)
	}
	return reusedWorkloads, cacheMisses, nil
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

func remoteCIWorkloadResults(result RunResult) ([]gate.RemoteCIWorkloadResult, error) {
	results := make([]gate.RemoteCIWorkloadResult, 0, len(result.ReusedWorkloads)+len(result.FreshWorkloadExecutions))
	identities := make(map[gate.GateID]gate.WorkloadPassIdentity, len(result.WorkloadPassIdentities))
	for _, identity := range result.WorkloadPassIdentities {
		identities[identity.WorkloadID] = identity
	}
	for _, evidence := range result.ReusedWorkloads {
		results = append(results, gate.RemoteCIWorkloadResult{Identity: evidence.Identity, Disposition: gate.WorkloadDispositionReused, OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: evidence.OriginAcceptedGeneration, EvidenceSHA256: evidence.EvidenceSHA256})
	}
	var identityErr error
	for _, execution := range result.FreshWorkloadExecutions {
		identity, ok := identities[execution.GateID]
		if !ok {
			identityErr = errors.Join(identityErr, fmt.Errorf("fresh workload execution %q is missing WorkloadPassIdentity", execution.GateID))
			continue
		}
		results = append(results, gate.RemoteCIWorkloadResult{Identity: identity, Disposition: gate.WorkloadDispositionExecuted, OriginJobID: result.JobID, OriginAcceptedGeneration: result.AcceptedGeneration})
	}
	return results, identityErr
}
