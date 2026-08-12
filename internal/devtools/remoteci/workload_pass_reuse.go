package remoteci

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math/bits"
	"slices"
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
	identities := make([]gate.WorkloadPassIdentity, 0, len(workloads))
	for _, workload := range workloads {
		goFlags, err := remoteWorkloadGoFlags(workload.ID)
		if err != nil {
			return nil, fmt.Errorf("derive remote workload %q GoFlags: %w", workload.ID, err)
		}
		environmentDigest, err := remoteWorkloadEnvironmentDigestForGoFlags(input, workerTimeout, resourcePolicy, goFlags)
		if err != nil {
			return nil, fmt.Errorf("digest remote workload %q environment: %w", workload.ID, err)
		}
		identity, err := remoteWorkloadPassIdentity(workload, inputDigests, environmentDigest)
		if err != nil {
			return nil, fmt.Errorf("digest remote workload %q pass identity: %w", workload.ID, err)
		}
		identities = append(identities, identity)
	}
	return identities, nil
}

// remoteWorkloadGoFlags 从 gate 的 canonical executor 映射投影 PASS 身份所需的 Go profile。
// 非 Go workload 不携带 GoFlags；未知的未展开 synthetic gate 保留旧的非 Go 语义，
// 但任何已展开 Go target 都必须由同一 canonical producer 给出 profile。
func remoteWorkloadGoFlags(id string) (string, error) {
	_, kind, _, targeted, err := gate.ParseWorkloadID(string(id))
	if err != nil {
		return "", err
	}
	if targeted {
		switch kind {
		case gate.WorkloadTargetGoGuard, gate.WorkloadTargetGoPackage, gate.WorkloadTargetGoTest, gate.WorkloadTargetGoBenchmark:
			return gate.WorkloadExecutionGoFlags(string(id))
		default:
			return "", nil
		}
	}
	if _, _, err := gate.ParseExecutorCommand([]string{"run", "--gate", string(id)}); err == nil {
		return gate.WorkloadExecutionGoFlags(string(id))
	}
	return "", nil
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
	SchemaVersion                 string   `json:"schema_version"`
	Platform                      string   `json:"platform"`
	PolicyDigest                  string   `json:"policy_digest"`
	ToolchainDigest               string   `json:"toolchain_digest"`
	RuntimeSeedSHA256             string   `json:"runtime_seed_sha256"`
	CGOEnabled                    string   `json:"cgo_enabled"`
	GOOS                          string   `json:"goos"`
	GOARCH                        string   `json:"goarch"`
	GoFlags                       string   `json:"go_flags"`
	SemanticEnvironmentSchema     string   `json:"semantic_environment_schema"`
	SemanticEnvironment           []string `json:"semantic_environment"`
	WorkerExecutionProvenance     string   `json:"worker_execution_provenance"`
	WorkerExecutionSemanticDigest string   `json:"worker_execution_semantic_digest"`
}

// remoteWorkloadEnvironmentDigestForGoFlags 计算绑定 workload 执行 profile 的语义环境摘要。
// GOFLAGS 与 command digest 使用同一 gate producer；normal/race 因而不会共享旧 PASS。
func remoteWorkloadEnvironmentDigestForGoFlags(input RunInput, workerTimeout time.Duration, resourcePolicy shardresource.Policy, goFlags string) (string, error) {
	if err := gate.ValidateExecutorWorkloadTimeout(workerTimeout); err != nil {
		return "", fmt.Errorf("validate remote workload environment timeout: %w", err)
	}
	if err := resourcePolicy.Validate(); err != nil {
		return "", fmt.Errorf("validate remote workload resource policy: %w", err)
	}
	if err := gate.ValidateCanonicalGoFlags(goFlags); err != nil {
		return "", fmt.Errorf("validate remote workload GoFlags: %w", err)
	}
	acceptedWorkerDigest := strings.TrimSpace(input.WorkerExecutionSemanticDigest)
	if !strings.HasPrefix(acceptedWorkerDigest, "sha256:") || len(acceptedWorkerDigest) != len("sha256:")+64 {
		return "", errors.New("remote workload worker execution semantic digest is required and invalid")
	}
	if acceptedWorkerDigest != strings.ToLower(acceptedWorkerDigest) {
		return "", errors.New("remote workload worker execution semantic digest is not canonical")
	}
	if _, err := hex.DecodeString(strings.TrimPrefix(acceptedWorkerDigest, "sha256:")); err != nil {
		return "", errors.New("remote workload worker execution semantic digest is not hexadecimal")
	}
	semanticEnvironment := cicontract.CanonicalWorkerExecutionEnvironment()
	semanticEnvironment = append([]string(nil), semanticEnvironment...)
	sort.Strings(semanticEnvironment)
	semanticValues, err := remoteWorkerSemanticEnvironmentValues(semanticEnvironment)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(remoteWorkloadEnvironment{
		SchemaVersion:                 cicontract.WorkloadPassEnvironmentSchemaVersion,
		Platform:                      input.Platform,
		PolicyDigest:                  input.PolicyDigest,
		ToolchainDigest:               input.ToolchainDigest,
		RuntimeSeedSHA256:             input.RuntimeSeedSHA256,
		CGOEnabled:                    semanticValues["CGO_ENABLED"],
		GOOS:                          semanticValues["GOOS"],
		GOARCH:                        semanticValues["GOARCH"],
		GoFlags:                       goFlags,
		SemanticEnvironmentSchema:     cicontract.WorkerExecutionEnvironmentSchemaVersion,
		SemanticEnvironment:           semanticEnvironment,
		WorkerExecutionProvenance:     cicontract.WorkerExecutionProvenanceID,
		WorkerExecutionSemanticDigest: acceptedWorkerDigest,
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
	reused                     map[string]gate.WorkloadPassEvidence
	environmentReplayProofs    map[string]string
	missConfirmations          remoteReuseMissConfirmations
	identities                 []gate.WorkloadPassIdentity
	reusedWorkloads            []gate.WorkloadPassEvidence
	reexecutedWorkloadResults  []gate.RemoteCIWorkloadResult
	cacheMisses                []gate.GateID
	directHits                 int
	sourceReplayHits           int
	environmentReplayHits      int
	exactHits                  int
	directMisses               int
	replayMisses               int
	calibrationDurationDemoted int
	forced                     bool
	replayDiagnostic           ReuseReplayDiagnostic
	diagnosticGroups           []ReuseDiagnosticGroup
}

// prepareRemoteWorkloadReuse 在临时目录、OSS 或 ECI 操作之前完成 PASS 查询与 miss 投影。
func prepareRemoteWorkloadReuse(
	ctx context.Context,
	input RunInput,
	catalog gate.WorkloadCatalog,
	workerTimeout time.Duration,
	resourcePolicy shardresource.Policy,
	fingerprintSnapshot *remoteGitTreeSnapshot,
) (remoteWorkloadReusePreparation, error) {
	preparation := remoteWorkloadReusePreparation{
		reused:                  make(map[string]gate.WorkloadPassEvidence),
		environmentReplayProofs: make(map[string]string),
		forced:                  input.Force,
	}
	identities, err := remoteWorkloadPassIdentities(ctx, input, catalog, workerTimeout, resourcePolicy)
	if err != nil {
		return remoteWorkloadReusePreparation{}, err
	}
	replayCache, err := newRemoteReplayCache(input.RepositoryRoot, input.Tree, fingerprintSnapshot)
	if err != nil {
		return remoteWorkloadReusePreparation{}, err
	}
	reused, err := prepareRemoteWorkloadReuseReplays(ctx, input, catalog, identities, workerTimeout, resourcePolicy, replayCache, &preparation)
	if err != nil {
		return remoteWorkloadReusePreparation{}, err
	}
	preparation.calibrationDurationDemoted, err = demoteCalibrationReuseWithoutDuration(input, catalog, reused, preparation.environmentReplayProofs)
	if err != nil {
		return remoteWorkloadReusePreparation{}, err
	}
	exactHits := len(reused)
	directMisses := len(identities) - preparation.directHits
	replayMisses := len(identities) - exactHits
	reusedWorkloads, cacheMisses, err := classifyRemoteWorkloadPassesStrict(identities, reused)
	if err != nil {
		return remoteWorkloadReusePreparation{}, fmt.Errorf("classify remote workload PASS reuse: %w", err)
	}
	effectiveReused, err := indexRemoteWorkloadPassEvidence(reusedWorkloads)
	if err != nil {
		return remoteWorkloadReusePreparation{}, fmt.Errorf("index effective remote workload PASS reuse: %w", err)
	}
	reexecuted, diagnosticGroups, err := projectRemoteWorkloadReuseOutcome(identities, reused, effectiveReused, preparation.environmentReplayProofs)
	if err != nil {
		return remoteWorkloadReusePreparation{}, err
	}
	preparation.reused = effectiveReused
	preparation.identities = identities
	preparation.reusedWorkloads = reusedWorkloads
	preparation.reexecutedWorkloadResults = reexecuted
	preparation.cacheMisses = cacheMisses
	preparation.exactHits = exactHits
	preparation.directMisses = directMisses
	preparation.replayMisses = replayMisses
	preparation.diagnosticGroups = diagnosticGroups
	return preparation, nil
}

// prepareRemoteWorkloadReuseReplays 依次运行 direct、source 与 environment 三条独立证明路径。
func prepareRemoteWorkloadReuseReplays(ctx context.Context, input RunInput, catalog gate.WorkloadCatalog, identities []gate.WorkloadPassIdentity, workerTimeout time.Duration, resourcePolicy shardresource.Policy, replayCache *remoteReplayCache, preparation *remoteWorkloadReusePreparation) (map[string]gate.WorkloadPassEvidence, error) {
	reused := make(map[string]gate.WorkloadPassEvidence)
	if input.Force {
		return reused, nil
	}
	var err error
	if reused, err = lookupRemoteWorkloadPasses(input.LedgerStore, identities); err != nil {
		return nil, err
	}
	preparation.directHits = len(reused)
	preparation.missConfirmations = newRemoteReuseMissConfirmations(identities, reused)
	if err := replayRemoteWorkloadPassMisses(ctx, input, catalog, identities, reused, replayCache, preparation.missConfirmations, &preparation.replayDiagnostic); err != nil {
		return nil, fmt.Errorf("replay remote workload PASS sources: %w", err)
	}
	preparation.sourceReplayHits = len(reused) - preparation.directHits
	afterSourceReplay := len(reused)
	if err := replayRemoteWorkloadPassEnvironment(ctx, input, catalog, identities, workerTimeout, resourcePolicy, reused, preparation.environmentReplayProofs, replayCache, preparation.missConfirmations, &preparation.replayDiagnostic); err != nil {
		return nil, fmt.Errorf("replay remote workload PASS environments: %w", err)
	}
	preparation.environmentReplayHits = len(reused) - afterSourceReplay
	return reused, validateRemoteReuseMissConsensus(identities, reused, preparation.missConfirmations)
}

// projectRemoteWorkloadReuseOutcome 同时冻结 package-atomic 重跑 proof 与对应
// 聚合诊断，避免两条投影在后续阶段观察到不同分类结果。
func projectRemoteWorkloadReuseOutcome(identities []gate.WorkloadPassIdentity, queried, effective map[string]gate.WorkloadPassEvidence, replayProofs map[string]string) ([]gate.RemoteCIWorkloadResult, []ReuseDiagnosticGroup, error) {
	reexecuted, err := remoteReexecutedWorkloadResults(identities, queried, effective, replayProofs)
	if err != nil {
		return nil, nil, fmt.Errorf("project package-atomic remote workload PASS consumers: %w", err)
	}
	groups, err := remoteReuseDiagnosticGroups(identities, queried, effective)
	if err != nil {
		return nil, nil, fmt.Errorf("group remote workload PASS reuse diagnostics: %w", err)
	}
	return reexecuted, groups, nil
}

// remoteReexecutedWorkloadResults 投影 lookup 命中但因 package 原子边界实际重跑的
// workload；结果保留旧 proof authority，fresh execution 由独立执行投影记录。
func remoteReexecutedWorkloadResults(
	identities []gate.WorkloadPassIdentity,
	queried, effective map[string]gate.WorkloadPassEvidence,
	environmentReplayProofs map[string]string,
) ([]gate.RemoteCIWorkloadResult, error) {
	results := make([]gate.RemoteCIWorkloadResult, 0, len(queried)-len(effective))
	for _, identity := range identities {
		workloadID := string(identity.WorkloadID)
		evidence, queriedHit := queried[workloadID]
		_, effectiveHit := effective[workloadID]
		if !queriedHit || effectiveHit {
			continue
		}
		result, err := remoteWorkloadProofResult(identity, evidence, environmentReplayProofs[workloadID])
		if err != nil {
			return nil, err
		}
		if err := result.Validate(); err != nil {
			return nil, fmt.Errorf("validate package-atomic workload %q proof result: %w", identity.WorkloadID, err)
		}
		results = append(results, result)
	}
	return results, nil
}

// diagnostic 返回直接查询、交叉 replay 与最终逐 workload 分类的非权威聚合差异。
func (preparation remoteWorkloadReusePreparation) diagnostic() ReuseDiagnostic {
	effectiveHits := len(preparation.reusedWorkloads)
	effectiveMisses := len(preparation.cacheMisses)
	confirmationThreshold := remoteReuseMissConfirmationThreshold
	if preparation.forced {
		confirmationThreshold = 0
	}
	return ReuseDiagnostic{
		Forced: preparation.forced, MissConfirmationThreshold: confirmationThreshold,
		DirectHits: preparation.directHits, SourceReplayHits: preparation.sourceReplayHits,
		EnvironmentReplayHits: preparation.environmentReplayHits,
		ExactHits:             preparation.exactHits, DirectMisses: preparation.directMisses,
		RecoveredDirectMisses:      preparation.directMisses - preparation.replayMisses,
		ReplayMisses:               preparation.replayMisses,
		AtomicDemoted:              preparation.exactHits - effectiveHits,
		CalibrationDurationDemoted: preparation.calibrationDurationDemoted,
		EffectiveHits:              effectiveHits, EffectiveMisses: effectiveMisses,
		Replay:     preparation.replayDiagnostic,
		MissGroups: slices.Clone(preparation.diagnosticGroups),
	}
}

// demoteCalibrationReuseWithoutDuration 只在校准运行中把缺可比较耗时样本的
// correctness PASS 降为 MISS；已有样本的命中继续复用，避免整批重跑。
func demoteCalibrationReuseWithoutDuration(input RunInput, catalog gate.WorkloadCatalog, reused map[string]gate.WorkloadPassEvidence, environmentProofs map[string]string) (int, error) {
	if !input.Calibration || input.Force || len(reused) == 0 {
		return 0, nil
	}
	if input.LedgerSnapshot.SampleIndex == nil {
		return 0, errors.New("calibration PASS reuse requires a duration sample index")
	}
	workloads := make(map[string]gate.Workload, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		workloads[workload.ID] = workload
	}
	demoted := 0
	for workloadID := range reused {
		workload, ok := workloads[workloadID]
		if !ok {
			return 0, fmt.Errorf("calibration reused workload %q is absent from catalog", workloadID)
		}
		if input.LedgerSnapshot.SampleIndex.HasSuccessfulCalibrationDurationEvidence(workload) {
			continue
		}
		delete(reused, workloadID)
		delete(environmentProofs, workloadID)
		demoted++
	}
	return demoted, nil
}

const remoteReuseMissConfirmationThreshold = 2

type remoteReuseMissSignal uint8

const (
	remoteReuseDirectMiss remoteReuseMissSignal = 1 << iota
	remoteReuseSourceMiss
	remoteReuseEnvironmentMiss
)

type remoteReuseMissConfirmations map[string]remoteReuseMissSignal

func newRemoteReuseMissConfirmations(identities []gate.WorkloadPassIdentity, reused map[string]gate.WorkloadPassEvidence) remoteReuseMissConfirmations {
	confirmations := make(remoteReuseMissConfirmations)
	for _, identity := range identities {
		workloadID := string(identity.WorkloadID)
		if _, hit := reused[workloadID]; !hit {
			confirmations[workloadID] = remoteReuseDirectMiss
		}
	}
	return confirmations
}

func (confirmations remoteReuseMissConfirmations) confirm(workloadID gate.GateID, signal remoteReuseMissSignal) {
	confirmations[string(workloadID)] |= signal
}

// validateRemoteReuseMissConsensus 只允许至少两条独立查询路径都未能证明 PASS 的 workload 进入远程分片。
func validateRemoteReuseMissConsensus(identities []gate.WorkloadPassIdentity, reused map[string]gate.WorkloadPassEvidence, confirmations remoteReuseMissConfirmations) error {
	for _, identity := range identities {
		workloadID := string(identity.WorkloadID)
		if _, hit := reused[workloadID]; hit {
			continue
		}
		count := bits.OnesCount8(uint8(confirmations[workloadID]))
		if count < remoteReuseMissConfirmationThreshold {
			return fmt.Errorf("remote workload PASS MISS %q has %d independent confirmations, want at least %d", identity.WorkloadID, count, remoteReuseMissConfirmationThreshold)
		}
	}
	return nil
}

const remoteReuseDiagnosticGroupLimit = 12

// remoteReuseDiagnosticGroups 以包/目标粒度解释直接与交叉确认后的 MISS；
// 排序和截断固定，避免数千 selector 污染进度旁路。
func remoteReuseDiagnosticGroups(identities []gate.WorkloadPassIdentity, queried, effective map[string]gate.WorkloadPassEvidence) ([]ReuseDiagnosticGroup, error) {
	groups := make(map[string]*ReuseDiagnosticGroup)
	for _, identity := range identities {
		kind, target, err := remoteReuseDiagnosticTarget(identity.WorkloadID)
		if err != nil {
			return nil, err
		}
		key := kind + "\x00" + target
		group := groups[key]
		if group == nil {
			group = &ReuseDiagnosticGroup{TargetKind: kind, TargetGroup: target}
			groups[key] = group
		}
		addRemoteReuseDiagnosticCounts(group, string(identity.WorkloadID), queried, effective)
	}
	return topRemoteReuseDiagnosticGroups(groups), nil
}

func addRemoteReuseDiagnosticCounts(group *ReuseDiagnosticGroup, workloadID string, queried, effective map[string]gate.WorkloadPassEvidence) {
	_, exactHit := queried[workloadID]
	_, effectiveHit := effective[workloadID]
	if exactHit {
		group.ExactHits++
	} else {
		group.DirectMisses++
	}
	if effectiveHit {
		group.EffectiveHits++
		return
	}
	group.EffectiveMisses++
	if exactHit {
		group.AtomicDemoted++
	}
}

func remoteReuseDiagnosticTarget(workloadID gate.GateID) (string, string, error) {
	parent, targetKind, target, targeted, err := gate.ParseWorkloadID(string(workloadID))
	if err != nil {
		return "", "", err
	}
	if !targeted {
		return "gate", string(parent), nil
	}
	switch targetKind {
	case gate.WorkloadTargetGoTest, gate.WorkloadTargetGoBenchmark:
		parsed, err := gate.ParseGoTestTarget(target)
		if err != nil {
			return "", "", err
		}
		return string(targetKind), parsed.Package, nil
	default:
		return string(targetKind), target, nil
	}
}

// topRemoteReuseDiagnosticGroups 只保留有效 MISS 最大的固定数量分组；完整
// authoritative workload 集合仍由 RunResult/SQLite 持有，旁路不得变成第二真相源。
func topRemoteReuseDiagnosticGroups(groups map[string]*ReuseDiagnosticGroup) []ReuseDiagnosticGroup {
	result := make([]ReuseDiagnosticGroup, 0, len(groups))
	for _, group := range groups {
		if group.EffectiveMisses != 0 {
			result = append(result, *group)
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].EffectiveMisses != result[right].EffectiveMisses {
			return result[left].EffectiveMisses > result[right].EffectiveMisses
		}
		if result[left].TargetKind != result[right].TargetKind {
			return result[left].TargetKind < result[right].TargetKind
		}
		return result[left].TargetGroup < result[right].TargetGroup
	})
	if len(result) > remoteReuseDiagnosticGroupLimit {
		result = result[:remoteReuseDiagnosticGroupLimit]
	}
	return result
}

// indexRemoteWorkloadPassEvidence 只索引分类后仍然有效的复用证据。
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

// classifyRemoteWorkloadPassesStrict 以身份顺序逐 workload 投影已交叉验证的复用证据。
// package 编译与测试 batch 仅是执行优化边界，不得把 sibling MISS 传播给独立 PASS 身份。
func classifyRemoteWorkloadPassesStrict(
	identities []gate.WorkloadPassIdentity,
	reused map[string]gate.WorkloadPassEvidence,
) ([]gate.WorkloadPassEvidence, []gate.GateID, error) {
	reusedWorkloads := make([]gate.WorkloadPassEvidence, 0, len(reused))
	cacheMisses := make([]gate.GateID, 0, len(identities)-len(reused))
	for _, identity := range identities {
		workloadID := string(identity.WorkloadID)
		if evidence, ok := reused[workloadID]; ok {
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
	result.ReexecutedWorkloadResults = slices.Clone(preparation.reexecutedWorkloadResults)
	result.CacheMissWorkloads = preparation.cacheMisses
	result.environmentReplayProofs = maps.Clone(preparation.environmentReplayProofs)
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

// remoteCIWorkloadResults 将当前四段身份与直接或来源重放的 PASS 证明投影为持久化结果。
func remoteCIWorkloadResults(result RunResult) ([]gate.RemoteCIWorkloadResult, error) {
	results := make([]gate.RemoteCIWorkloadResult, 0, len(result.ReusedWorkloads)+len(result.FreshWorkloadExecutions))
	identities := make(map[gate.GateID]gate.WorkloadPassIdentity, len(result.WorkloadPassIdentities))
	for _, identity := range result.WorkloadPassIdentities {
		identities[identity.WorkloadID] = identity
	}
	for _, evidence := range result.ReusedWorkloads {
		identity, ok := identities[evidence.Identity.WorkloadID]
		if !ok {
			return nil, fmt.Errorf("reused workload %q is missing current WorkloadPassIdentity", evidence.Identity.WorkloadID)
		}
		workloadResult, err := remoteWorkloadProofResult(identity, evidence, result.environmentReplayProofs[string(identity.WorkloadID)])
		if err != nil {
			return nil, err
		}
		results = append(results, workloadResult)
	}
	reexecuted, err := indexRemoteReexecutedWorkloadResults(result.ReexecutedWorkloadResults)
	if err != nil {
		return nil, err
	}
	return appendRemoteFreshWorkloadResults(results, result, identities, reexecuted)
}

// CanonicalRemoteCIWorkloadResults 返回 coordinator 用于 provisional 写入的唯一
// workload result 投影，finalizer 必须复用它而不得重建 replay proof 语义。
func CanonicalRemoteCIWorkloadResults(result RunResult) ([]gate.RemoteCIWorkloadResult, error) {
	return remoteCIWorkloadResults(result)
}

// appendRemoteFreshWorkloadResults 追加普通 MISS 或 package 原子重跑的结果，
// 并要求每个冻结的旧 proof consumer 都有本次 fresh execution。
func appendRemoteFreshWorkloadResults(results []gate.RemoteCIWorkloadResult, result RunResult, identities map[gate.GateID]gate.WorkloadPassIdentity, reexecuted map[gate.GateID]gate.RemoteCIWorkloadResult) ([]gate.RemoteCIWorkloadResult, error) {
	var identityErr error
	for _, execution := range result.FreshWorkloadExecutions {
		identity, ok := identities[execution.GateID]
		if !ok {
			identityErr = errors.Join(identityErr, fmt.Errorf("fresh workload execution %q is missing WorkloadPassIdentity", execution.GateID))
			continue
		}
		if proofResult, ok := reexecuted[execution.GateID]; ok {
			if proofResult.Identity != identity {
				identityErr = errors.Join(identityErr, fmt.Errorf("package-atomic workload execution %q proof identity drifted", execution.GateID))
				continue
			}
			results = append(results, proofResult)
			delete(reexecuted, execution.GateID)
			continue
		}
		results = append(results, gate.RemoteCIWorkloadResult{Identity: identity, Disposition: gate.WorkloadDispositionExecuted, OriginJobID: result.JobID, OriginAcceptedGeneration: result.AcceptedGeneration})
	}
	for workloadID := range reexecuted {
		identityErr = errors.Join(identityErr, fmt.Errorf("package-atomic workload %q lacks fresh execution", workloadID))
	}
	return results, identityErr
}

// remoteWorkloadProofResult 以当前 identity 和已验证 origin evidence 构造规范 consumer 结果。
func remoteWorkloadProofResult(identity gate.WorkloadPassIdentity, evidence gate.WorkloadPassEvidence, environmentReplayProof string) (gate.RemoteCIWorkloadResult, error) {
	evidenceSHA := evidence.EvidenceSHA256
	if identity != evidence.Identity {
		if environmentReplayProof != "" {
			evidenceSHA = environmentReplayProof
		} else {
			var err error
			evidenceSHA, err = gate.WorkloadPassSourceReplaySHA256(identity, evidence)
			if err != nil {
				return gate.RemoteCIWorkloadResult{}, err
			}
		}
	}
	return gate.RemoteCIWorkloadResult{Identity: identity, Disposition: gate.WorkloadDispositionReused, OriginJobID: evidence.OriginJobID, OriginAcceptedGeneration: evidence.OriginAcceptedGeneration, EvidenceSHA256: evidenceSHA}, nil
}

// indexRemoteReexecutedWorkloadResults 校验冻结的 package 原子重跑 proof 投影无重复。
func indexRemoteReexecutedWorkloadResults(results []gate.RemoteCIWorkloadResult) (map[gate.GateID]gate.RemoteCIWorkloadResult, error) {
	indexed := make(map[gate.GateID]gate.RemoteCIWorkloadResult, len(results))
	for _, result := range results {
		if err := result.Validate(); err != nil {
			return nil, fmt.Errorf("validate package-atomic workload %q proof result: %w", result.Identity.WorkloadID, err)
		}
		if result.Disposition != gate.WorkloadDispositionReused {
			return nil, fmt.Errorf("package-atomic workload %q must retain reused proof disposition", result.Identity.WorkloadID)
		}
		if _, duplicate := indexed[result.Identity.WorkloadID]; duplicate {
			return nil, fmt.Errorf("package-atomic workload %q is duplicated", result.Identity.WorkloadID)
		}
		indexed[result.Identity.WorkloadID] = result
	}
	return indexed, nil
}
