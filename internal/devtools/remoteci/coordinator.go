package remoteci

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
	"golang.org/x/sync/errgroup"
)

// RunResultSchemaVersion is the current wire schema for remote run observations.
const RunResultSchemaVersion uint32 = 12

const remoteShardDiagnosticMaxBytes = 4 << 10

// ErrGateFailed marks a valid remote report whose gate outcome is not green.
var ErrGateFailed = errors.New("remote CI gate execution failed")

// ObjectStore owns remote objects. Create is atomic create-only and must return
// only after the real object's metadata, size, digest and read-back have been
// verified; callers must never use it as an overwrite operation.
type ObjectStore interface {
	Create(context.Context, string, string) error
	DeletePrefix(context.Context, string) error
}

// Runtime owns the complete lifecycle of one ECI container group.
type Runtime interface {
	CreateContainerGroup(context.Context, eci.CreateRequest) (eci.ContainerGroup, error)
	DescribeContainerGroups(context.Context, ...string) ([]eci.ContainerGroup, error)
	DescribeContainerLog(context.Context, string, string) (string, error)
	DeleteContainerGroup(context.Context, string) error
}

// CoordinatorConfig contains only fixed non-secret remote execution settings.
type CoordinatorConfig struct {
	Bucket               string
	SourcePrefix         string
	InternalOSSEndpoint  string
	WorkerRoleName       string
	ImageCacheSnapshotID string
	WorkerTimeout        time.Duration
	PollInterval         time.Duration
	CleanupTimeout       time.Duration
	ResourcePolicy       shardresource.Policy
	ResourceObservations []shardresource.Observation
	ProgressObserver     ProgressObserver
}

// RunInput binds one remote run to exact Git objects and one accepted runner identity.
type RunInput struct {
	AcceptedGeneration            uint64 `json:"accepted_generation"`
	RepositoryRoot                string
	RemoteName                    string
	RemoteURL                     string
	AgentTokenDigest              string
	Commit                        string
	Tree                          string
	Base                          string
	RunnerBaseCommit              string
	RunnerBaseTree                string
	Source                        gate.SourceSpec
	Profile                       gate.Profile
	Entrypoint                    gate.CIEntrypointID
	Platform                      string
	PolicyDigest                  string
	ToolchainDigest               string
	LedgerSnapshot                gate.DurationLedgerSnapshot
	LedgerStore                   *gate.DurationLedgerStore
	Inventory                     gate.WorkloadInventory
	SelectedTests                 bool
	Calibration                   bool
	Force                         bool
	RunnerImage                   string
	ImageCacheSnapshotID          string
	ExecutionRunnerImage          string
	ExecutionImageCacheSnapshotID string
	ImageCacheOnly                bool
	RunnerIdentityDigest          string
	BaselineManifestDigest        string
	RunnerConfigDigest            string
	GateBinarySHA256              string
	CandidateGateSourceSHA256     string
	CandidateGateToolchainSHA256  string
	RuntimeSeedSHA256             string
	// WorkloadInputDigests 从 accepted source tree 计算一次，并由 planning 与
	// duration-sample producer 复用；它不改变 PASS reuse identity 语义。
	WorkloadInputDigests map[string]string
	// WorkloadCompileGroupInputs 从与 WorkloadInputDigests 相同的 accepted source snapshot
	// 计算，仅由 compile-aware planner 消费。它绝不参与 correctness PASS identity 或 selector runtime input。
	WorkloadCompileGroupInputs map[string]gate.CompileGroupInput
	OCIProjectCache            *BaselineOCIProjectCache
	CalibrationResource        shardresource.Class
}

// ShardResult records directly observed ECI identity, terminal state, and worker report.
type ShardResult struct {
	ShardIdentity         string                          `json:"shard_identity"`
	ContainerGroup        string                          `json:"container_group_id"`
	ContainerStatus       string                          `json:"container_status"`
	ExecutedWorkloads     []gate.GateID                   `json:"executed_workloads,omitempty"`
	Report                gate.PlanExecutionReport        `json:"report"`
	MaterializationTiming gate.ShardMaterializationTiming `json:"materialization_timing"`
	Resources             eci.Resources                   `json:"resources"`
	ResourceClass         string                          `json:"resource_class,omitempty"`
	TerminalEvidence      *gate.RemoteCITerminalEvidence  `json:"terminal_evidence,omitempty"`
	ECIWaitStartedAt      time.Time                       `json:"eci_wait_started_at"`
	ECIWaitCompletedAt    time.Time                       `json:"eci_wait_completed_at"`
	ECITerminalAt         time.Time                       `json:"eci_terminal_at"`
	workerDiagnostic      string
}

// RunResult is an unsigned remote execution observation. Authority receipts are minted separately.
type RunResult struct {
	SchemaVersion                uint32              `json:"schema_version"`
	AcceptedGeneration           uint64              `json:"accepted_generation"`
	ImageCacheSnapshotID         string              `json:"image_cache_snapshot_id"`
	JobID                        string              `json:"job_id"`
	RemoteName                   string              `json:"remote_name,omitempty"`
	RemoteURL                    string              `json:"remote_url,omitempty"`
	AgentTokenDigest             string              `json:"agent_token_digest"`
	Force                        bool                `json:"force"`
	Entrypoint                   gate.CIEntrypointID `json:"entrypoint"`
	Profile                      gate.Profile        `json:"profile"`
	PlanDigest                   string              `json:"plan_digest"`
	CatalogDigest                string              `json:"catalog_digest"`
	SourceTreeSHA                string              `json:"source_tree_sha"`
	CandidateGateSourceSHA256    string              `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string              `json:"candidate_gate_toolchain_sha256"`
	RunnerImage                  string              `json:"runner_image"`
	Platform                     string              `json:"platform"`
	RunnerIdentityDigest         string              `json:"runner_identity_digest"`
	ToolchainDigest              string              `json:"toolchain_digest"`
	CalibrationResourceClassID   string              `json:"calibration_resource_class_id,omitempty"`
	CalibrationResourceCPU       float64             `json:"calibration_resource_cpu,omitempty"`
	CalibrationResourceMemoryGiB float64             `json:"calibration_resource_memory_gib,omitempty"`
	ExecutionMode                string              `json:"execution_mode"`
	Status                       gate.ResultStatus   `json:"status"`
	Authoritative                bool                `json:"authoritative"`
	StartedAt                    time.Time           `json:"started_at"`
	CompletedAt                  time.Time           `json:"completed_at"`
	Shards                       []ShardResult       `json:"shards"`
	// CompileGroupExecutions 是 fresh 分片报告的派生平铺副本；报告仍是来源，
	// 此字段仅用于结果与回执检查。
	CompileGroupExecutions  []gate.CompileGroupExecution `json:"compile_group_executions,omitempty"`
	GateExecutions          []gate.PlanGateExecution     `json:"gate_executions"`
	WorkloadExecutions      []gate.PlanGateExecution     `json:"workload_executions,omitempty"`
	FreshWorkloadExecutions []gate.PlanGateExecution     `json:"-"`
	ReusedWorkloads         []gate.WorkloadPassEvidence  `json:"reused_workloads,omitempty"`
	CacheMissWorkloads      []gate.GateID                `json:"cache_miss_workloads,omitempty"`
	WorkloadPassIdentities  []gate.WorkloadPassIdentity  `json:"-"`
	DurationSamples         []gate.DurationSample        `json:"duration_samples"`
	OptimizationWarnings    []string                     `json:"optimization_warnings,omitempty"`
	TimingWarnings          []gate.RemoteCITimingWarning `json:"timing_warnings,omitempty"`
	CleanupComplete         bool                         `json:"cleanup_complete"`
}

// Coordinator executes exact Git sources with one ECI container per canonical shard.
type Coordinator struct {
	config             CoordinatorConfig
	store              ObjectStore
	runtime            Runtime
	observationTimeout time.Duration
	now                func() time.Time
	newID              func() (string, error)
	progress           *progressTracker
}

// NewCoordinator 校验固定的 OSS 与 ECI 边界并构造协调器。
func NewCoordinator(
	config CoordinatorConfig,
	store ObjectStore,
	runtime Runtime,
) (*Coordinator, error) {
	if store == nil || runtime == nil {
		return nil, errors.New("remote CI object store and runtime are required")
	}
	if err := validateCoordinatorConfig(config); err != nil {
		return nil, err
	}
	clock := time.Now
	progress := newProgressTracker(config.ProgressObserver, clock)
	return &Coordinator{
		config:             config,
		store:              store,
		runtime:            newProgressRuntime(runtime, progress),
		observationTimeout: eci.MaxControlPlaneRetryDuration() + time.Minute,
		now:                clock,
		newID:              randomJobID,
		progress:           progress,
	}, nil
}

// validateCoordinatorConfig 校验协调器的对象存储、时限和资源策略配置。
func validateCoordinatorConfig(config CoordinatorConfig) error {
	if !validCoordinatorObjectConfig(config) {
		return errors.New("remote CI bucket, object prefixes, internal endpoint, and worker role are required")
	}
	if err := gate.ValidateExecutorWorkloadTimeout(config.WorkerTimeout); err != nil {
		return err
	}
	if config.PollInterval <= 0 || config.CleanupTimeout <= 0 {
		return errors.New("remote CI poll interval and cleanup timeout must be positive")
	}
	if err := config.ResourcePolicy.Validate(); err != nil {
		return fmt.Errorf("remote CI resource policy: %w", err)
	}
	if !validImageCacheIdentifier(config.ImageCacheSnapshotID) {
		return errors.New("remote CI accepted ImageCacheSnapshotID is required")
	}
	return nil
}

// validCoordinatorObjectConfig 判断 OSS 前缀、内网端点和 worker 角色是否完整且相互约束。
func validCoordinatorObjectConfig(config CoordinatorConfig) bool {
	return strings.TrimSpace(config.Bucket) != "" &&
		validObjectPrefix(config.SourcePrefix) &&
		strings.TrimSpace(config.InternalOSSEndpoint) != "" &&
		strings.TrimSpace(config.WorkerRoleName) != ""
}

// validateCoordinatorRunInput 在生成 job 标识前校验调用上下文、SQLite 权威账本与已接受镜像绑定。
func validateCoordinatorRunInput(ctx context.Context, config CoordinatorConfig, input RunInput) error {
	if ctx == nil {
		return errors.New("remote CI context is required")
	}
	if input.LedgerStore == nil {
		return errors.New("remote CI duration ledger SQLite authority is required")
	}
	return validateRunImageCacheAuthority(config, input)
}

// validateRunImageCacheAuthority 分离 SQLite correctness snapshot 与实时验证的 ECI execution snapshot。
func validateRunImageCacheAuthority(config CoordinatorConfig, input RunInput) error {
	if !validImageCacheIdentifier(input.ImageCacheSnapshotID) {
		return errors.New("remote CI accepted ImageCacheSnapshotID is required")
	}
	if input.OCIProjectCache == nil {
		return errors.New("remote CI run OCI project cache is required for image snapshot binding")
	}
	if !validImageCacheIdentifier(input.ExecutionImageCacheSnapshotID) || input.ExecutionImageCacheSnapshotID != config.ImageCacheSnapshotID {
		return errors.New("remote CI execution ImageCacheSnapshotID must equal the live-verified coordinator ImageCacheSnapshotID")
	}
	if strings.TrimSpace(input.ExecutionRunnerImage) == "" || !input.ImageCacheOnly {
		return errors.New("remote CI execution ImageCache runtime is incomplete")
	}
	if err := input.OCIProjectCache.ValidateForBaseline(input.RunnerBaseTree, input.ToolchainDigest, input.Platform, input.RunnerImage); err != nil {
		return fmt.Errorf("remote CI run ImageCache OCI project cache binding: %w", err)
	}
	return nil
}

// remoteWorkloadMissInputs 保存当前未命中 workload 的冻结执行输入。
//
// source bundle 和分片请求由 prepareRemoteShardRequests 作为同一资产边界构造。
// 每个分片随后只在物化后的候选源码内增量构建其 worker CLI。
// 上传、创建、等待和结果汇总仍在这里顺序编排，以保留可观测 phase。
// 任一步失败都会保留已获得的执行证据并由调用方统一清理临时对象。
type remoteWorkloadMissInputs struct {
	set                  gate.ContainerShardSet
	resources            []shardresource.Class
	requests             []ShardRequest
	requestKeys          []string
	bootstrapRequestKeys []string
}

// prepareRemoteWorkloadMissInputs 固定 miss 分片的资产、资源和请求，不改变唯一 ECI 执行路径。
func (coordinator *Coordinator) prepareRemoteWorkloadMissInputs(
	ctx context.Context,
	input RunInput,
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	executionIDs []gate.GateID,
	jobID string,
	tempRoot string,
	objectKeys *[]string,
	createdGroups *[]string,
) (remoteWorkloadMissInputs, error) {
	set, err := buildRemoteExecutionShardSetForWorkloads(plan, catalog, executionIDs, input)
	if err != nil {
		return remoteWorkloadMissInputs{}, err
	}
	executionShards := set.Shards
	coordinator.progress.planUpdated(len(executionShards))
	*objectKeys = make([]string, 0, 2+len(executionShards))
	*createdGroups = make([]string, 0, len(executionShards))
	resources, err := remoteExecutionShardResources(
		coordinator.config.ResourcePolicy,
		coordinator.config.ResourceObservations,
		set.WorkloadPlan,
		executionShards,
		input,
	)
	if err != nil {
		return remoteWorkloadMissInputs{set: set}, err
	}
	assets, err := buildRemoteAssets(ctx, input, jobID, tempRoot, coordinator.config.SourcePrefix)
	if err != nil {
		return remoteWorkloadMissInputs{set: set, resources: resources}, err
	}
	coordinator.progress.uploadStarted()
	if err := coordinator.uploadSourceAssets(ctx, assets, objectKeys); err != nil {
		coordinator.progress.uploadFinished(err)
		return remoteWorkloadMissInputs{set: set, resources: resources}, err
	}
	coordinator.progress.uploadFinished(nil)
	requests, requestKeys, bootstrapRequestKeys, err := buildShardRequestsWithCompileGroups(
		coordinator.config.SourcePrefix,
		jobID,
		executionShards,
		resources,
		assets.materialization,
		assets.bundleKey,
		assets.bundleDigest,
		assets.bundleSize,
		assets.manifestKey,
		assets.manifestDigest,
		input,
		set.WorkloadPlan.CompileGroups,
	)
	if err != nil {
		return remoteWorkloadMissInputs{set: set, resources: resources}, err
	}
	return remoteWorkloadMissInputs{
		set:                  set,
		resources:            resources,
		requests:             requests,
		requestKeys:          requestKeys,
		bootstrapRequestKeys: bootstrapRequestKeys,
	}, nil
}

// mergeRemoteWorkloadMisses 合并实际执行与严格复用 evidence，并保留失败时的观测写入。
func (coordinator *Coordinator) mergeRemoteWorkloadMisses(
	catalog gate.WorkloadCatalog,
	input RunInput,
	prepared remoteWorkloadMissInputs,
	reused map[string]gate.WorkloadPassEvidence,
	executed []ShardResult,
	result RunResult,
) (RunResult, error, error) {
	executedWorkloads := remoteExecutionWorkloads(prepared.set.WorkloadPlan)
	fresh, freshErr := remoteFreshWorkloadExecutions(executedWorkloads, executed)
	freshObserved, mergeErr := collectFreshRemoteWorkloadExecutions(executedWorkloads, fresh)
	partialFresh, partialErr := canonicalPartialRemoteWorkloadExecutions(executedWorkloads, fresh)
	result.FreshWorkloadExecutions = partialFresh
	result.WorkloadExecutions = append([]gate.PlanGateExecution(nil), partialFresh...)
	freshErr = errors.Join(freshErr, partialErr)
	observed := make(map[string]gate.PlanGateExecution, len(freshObserved)+len(reused))
	maps.Copy(observed, freshObserved)
	for workloadID, evidence := range reused {
		if _, duplicate := observed[workloadID]; duplicate {
			mergeErr = errors.Join(mergeErr, fmt.Errorf("remote workload %q is both fresh and reused", workloadID))
			continue
		}
		observed[workloadID] = evidence.OriginExecution
	}
	if freshErr == nil && mergeErr == nil {
		result, mergeErr = coordinator.completeRemoteRunWithExecutionCatalog(
			catalog,
			remoteExecutionCatalog(prepared.set.WorkloadPlan),
			input,
			executed,
			observed,
			freshObserved,
			result,
		)
	} else {
		var durationErr error
		result, durationErr = coordinator.recordRemoteRunObservations(
			remoteExecutionCatalog(prepared.set.WorkloadPlan),
			input,
			executed,
			result,
		)
		partialObservations, timingErr := remoteFailedTimingObservations(result)
		var warningErr error
		result, warningErr = appendRemotePartialWorkloadTargetWarnings(result, partialObservations)
		mergeErr = errors.Join(durationErr, timingErr, warningErr, mergeErr)
	}
	return result, freshErr, mergeErr
}

// canonicalPartialRemoteWorkloadExecutions 保留已严格校验的 workload，并把缺失或失真的条目标记为错误。
func canonicalPartialRemoteWorkloadExecutions(
	workloads []gate.Workload,
	fresh map[string]gate.PlanGateExecution,
) ([]gate.PlanGateExecution, error) {
	if len(fresh) == 0 {
		return nil, nil
	}
	expected := make(map[string]struct{}, len(workloads))
	partial := make([]gate.PlanGateExecution, 0, len(fresh))
	var partialErr error
	for _, workload := range workloads {
		expected[workload.ID] = struct{}{}
		execution, ok := fresh[workload.ID]
		if !ok {
			continue
		}
		canonical, err := gate.CanonicalizePlanGateExecutionTiming(execution)
		if err != nil {
			partialErr = errors.Join(partialErr, fmt.Errorf("remote workload %q timing: %w", workload.ID, err))
			continue
		}
		partial = append(partial, canonical)
	}
	for workloadID := range fresh {
		if _, ok := expected[workloadID]; !ok {
			partialErr = errors.Join(partialErr, fmt.Errorf("remote workload %q is outside the current catalog", workloadID))
		}
	}
	return partial, partialErr
}

// runRemoteWorkloadMisses 为当前未命中 workload 规划远程分片，并与严格复用证据聚合。
func (coordinator *Coordinator) runRemoteWorkloadMisses(
	ctx context.Context,
	input RunInput,
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	executionIDs []gate.GateID,
	reused map[string]gate.WorkloadPassEvidence,
	jobID string,
	tempRoot string,
	objectKeys *[]string,
	createdGroups *[]string,
	result RunResult,
) (RunResult, error) {
	if err := validateRemoteWorkloadMissIDs(executionIDs, reused); err != nil {
		return result, err
	}
	prepared, err := coordinator.prepareRemoteWorkloadMissInputs(
		ctx,
		input,
		plan,
		catalog,
		executionIDs,
		jobID,
		tempRoot,
		objectKeys,
		createdGroups,
	)
	result.OptimizationWarnings = appendUniqueRemoteWarnings(result.OptimizationWarnings, compileGroupPlanWarnings(prepared.set.WorkloadPlan.CompileGroups))
	if err != nil {
		return result, err
	}
	executionShards := prepared.set.Shards
	coordinator.progress.createStarted()
	groupIDs, createErr := coordinator.uploadAndCreateRemoteGroups(
		ctx, tempRoot, jobID, executionShards,
		prepared.resources, prepared.requests, prepared.requestKeys, prepared.bootstrapRequestKeys,
		input, objectKeys, createdGroups,
	)
	coordinator.progress.createFinished(createErr)
	executed, timingWarnings, waitErr := coordinator.waitShards(ctx, executionShards, groupIDs, remoteTimingWarningRun{
		jobID: jobID, agentTokenDigest: input.AgentTokenDigest,
		acceptedGeneration: input.AcceptedGeneration, store: input.LedgerStore,
	})
	coordinator.progress.runFinished(executed, errors.Join(createErr, waitErr))
	result.TimingWarnings = append(result.TimingWarnings, timingWarnings...)
	for _, warning := range timingWarnings {
		result.OptimizationWarnings = appendUniqueRemoteWarnings(result.OptimizationWarnings, []string{warning.WarningText})
	}
	if bindErr := bindRemoteShardResources(executed, prepared.resources, prepared.requests); bindErr != nil {
		waitErr = errors.Join(waitErr, bindErr)
	}
	result, freshErr, mergeErr := coordinator.mergeRemoteWorkloadMisses(catalog, input, prepared, reused, executed, result)
	return result, errors.Join(createErr, waitErr, freshErr, mergeErr)
}

// validateRemoteWorkloadMissIDs 在进入 LPT splitter 前拒绝空、重复或已复用 workload。
// 这样调用链即使未来被错误地传入完整 catalog，也不会把 PASS workload 重新规划或执行。
func validateRemoteWorkloadMissIDs(executionIDs []gate.GateID, reused map[string]gate.WorkloadPassEvidence) error {
	if len(executionIDs) == 0 {
		return errors.New("remote CI workload miss list is required before shard planning")
	}
	seen := make(map[gate.GateID]struct{}, len(executionIDs))
	for _, workloadID := range executionIDs {
		if workloadID == "" {
			return errors.New("remote CI workload miss identity is required before shard planning")
		}
		if _, duplicate := seen[workloadID]; duplicate {
			return fmt.Errorf("remote CI workload miss %q is duplicated", workloadID)
		}
		seen[workloadID] = struct{}{}
		if _, reusedHit := reused[string(workloadID)]; reusedHit {
			return fmt.Errorf("remote CI workload %q is both reused and a miss", workloadID)
		}
	}
	return nil
}

// completeRemoteRunWithExecutionCatalog 合并完整 catalog 与 miss 执行分片，保留 fresh 与复用边界。
func (coordinator *Coordinator) completeRemoteRunWithExecutionCatalog(
	catalog gate.WorkloadCatalog,
	executionCatalog gate.WorkloadCatalog,
	input RunInput,
	shards []ShardResult,
	observed map[string]gate.PlanGateExecution,
	freshObserved map[string]gate.PlanGateExecution,
	result RunResult,
) (RunResult, error) {
	result, err := coordinator.recordRemoteRunObservations(executionCatalog, input, shards, result)
	if err != nil {
		return result, err
	}
	result.FreshWorkloadExecutions, err = remoteWorkloadExecutions(executionCatalog, freshObserved)
	if err != nil {
		return result, err
	}
	if workloadErr := failedObservedWorkloadError(observed); workloadErr != nil {
		partialObservations, timingErr := remoteFailedTimingObservations(result)
		var warningErr error
		result, warningErr = appendRemotePartialWorkloadTargetWarnings(result, partialObservations)
		return result, errors.Join(workloadErr, failedRemoteGateError(shards), timingErr, warningErr)
	}
	result, err = appendRemoteWorkloadTargetWarnings(result)
	if err != nil {
		return result, err
	}
	executions, workloadExecutions, status, err := aggregateRemoteReports(catalog, observed, shards, result.PlanDigest)
	if err != nil {
		if gateErr := failedRemoteGateError(shards); gateErr != ErrGateFailed {
			return result, errors.Join(err, gateErr)
		}
		return result, err
	}
	parentSamples, err := remoteCalibrationParentDurationSamples(catalog, freshObserved, input, input.WorkloadInputDigests)
	if err != nil {
		return result, err
	}
	result.DurationSamples = append(result.DurationSamples, parentSamples...)
	result.GateExecutions, result.WorkloadExecutions, result.Status = executions, workloadExecutions, status
	if status != gate.ResultStatusPassed {
		return result, failedRemoteGateError(shards)
	}
	return result, nil
}

// recordRemoteRunObservations 即使整批失败也保留已取得的分片终态和可比较时长样本。
func (coordinator *Coordinator) recordRemoteRunObservations(
	catalog gate.WorkloadCatalog,
	input RunInput,
	shards []ShardResult,
	result RunResult,
) (RunResult, error) {
	result.Shards, result.CompletedAt = shards, coordinator.now().UTC()
	compileExecutions, compileErr := remoteCompileGroupExecutions(result)
	result.CompileGroupExecutions = compileExecutions
	inputDigests := cloneRemoteWorkloadInputDigests(input.WorkloadInputDigests)
	if inputDigests == nil && len(result.WorkloadPassIdentities) > 0 {
		inputDigests = make(map[string]string, len(result.WorkloadPassIdentities))
	}
	for _, identity := range result.WorkloadPassIdentities {
		if identity.InputDigest != "" {
			inputDigests[string(identity.WorkloadID)] = identity.InputDigest
		}
	}
	samples, err := remoteDurationSamples(catalog, shards, input, inputDigests)
	result.DurationSamples = samples
	return result, errors.Join(compileErr, err)
}

// newRunResult 初始化失败默认值的可审计远程运行结果。
func (coordinator *Coordinator) newRunResult(
	plan gate.GatePlan,
	catalogDigest string,
	entrypoint gate.CIEntrypoint,
	input RunInput,
	jobID string,
) RunResult {
	// 协调器只持久化待定结果；命令边界必须在同一 SQLite 事务内重载完整回执，
	// 并完成 fresh evidence 提升后，才能把本行提升为权威结果。
	mode := gate.DurationExecutionModeNormal
	if input.Calibration {
		mode = gate.DurationExecutionModeCalibration
	}
	result := RunResult{SchemaVersion: RunResultSchemaVersion, AcceptedGeneration: input.AcceptedGeneration, ImageCacheSnapshotID: input.ImageCacheSnapshotID, JobID: jobID, RemoteName: input.RemoteName, RemoteURL: input.RemoteURL, AgentTokenDigest: input.AgentTokenDigest, Force: input.Force, Entrypoint: entrypoint.ID, Profile: plan.Profile, PlanDigest: plan.PlanDigest, CatalogDigest: catalogDigest, SourceTreeSHA: plan.Source.SourceTreeSHA, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, RunnerImage: input.RunnerImage, Platform: input.Platform, RunnerIdentityDigest: input.RunnerIdentityDigest, ToolchainDigest: input.ToolchainDigest, ExecutionMode: mode, Status: gate.ResultStatusFailed, Authoritative: false, StartedAt: coordinator.now().UTC()}
	if input.Calibration {
		result.CalibrationResourceClassID = input.CalibrationResource.ID
		result.CalibrationResourceCPU = input.CalibrationResource.VCPU
		result.CalibrationResourceMemoryGiB = input.CalibrationResource.MemoryGiB
	}
	return result
}

// createRemoteTempRoot 创建每次远程运行独占的临时源目录。
func createRemoteTempRoot() (string, error) {
	root, err := os.MkdirTemp("", "super-dolphin-remote-ci-*")
	if err != nil {
		return "", fmt.Errorf("create remote CI source staging root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", errors.Join(fmt.Errorf("canonicalize remote CI source staging root: %w", err), os.RemoveAll(root))
	}
	return canonical, nil
}

// uploadAndCreateRemoteGroups 并行上传并创建每个缓存未命中分片，worker 只继承调用方取消边界。
func (coordinator *Coordinator) uploadAndCreateRemoteGroups(ctx context.Context, tempRoot string, jobID string, shards []gate.ContainerShard, resources []shardresource.Class, requests []ShardRequest, requestKeys []string, bootstrapRequestKeys []string, input RunInput, objectKeys *[]string, created *[]string) ([]string, error) {
	if len(resources) != len(shards) || len(requests) != len(shards) || len(requestKeys) != len(shards) || len(bootstrapRequestKeys) != len(shards) {
		return nil, errors.New("remote CI shard resources or requests are incomplete")
	}
	ids := make([]string, len(shards))
	failures := make([]error, len(shards))
	// Worker 仅共享 caller 的 cancellation boundary。create failure 记录在所属 shard，
	// 但不得取消 sibling upload 或 create：这些 request 可能已经生成 cleanup 必须看到的 resource。
	var workers errgroup.Group
	var mu sync.Mutex
	for index := range shards {
		workers.Go(func() error {
			groupID, uploadedKeys, err := coordinator.uploadAndCreateRemoteGroup(
				ctx,
				tempRoot,
				jobID,
				index,
				shards[index],
				resources[index],
				requests[index],
				requestKeys[index],
				bootstrapRequestKeys[index],
				input,
			)
			ids[index] = groupID
			failures[index] = err
			mu.Lock()
			*objectKeys = append(*objectKeys, uploadedKeys...)
			if groupID != "" {
				*created = append(*created, groupID)
			}
			mu.Unlock()
			return err
		})
	}
	_ = workers.Wait()
	return ids, errors.Join(failures...)
}

// uploadAndCreateRemoteGroup 先上传同一分片的冻结 bootstrap 与完整请求，再创建唯一 ECI worker。
func (coordinator *Coordinator) uploadAndCreateRemoteGroup(ctx context.Context, tempRoot string, jobID string, index int, shard gate.ContainerShard, resources shardresource.Class, request ShardRequest, requestKey string, bootstrapRequestKey string, input RunInput) (string, []string, error) {
	fullPath := filepath.Join(tempRoot, fmt.Sprintf("shard-%02d.request.json", index))
	bootstrapPath := filepath.Join(tempRoot, fmt.Sprintf("shard-%02d.bootstrap.request.json", index))
	fullData, fullDigest, err := EncodeShardRequest(request)
	if err != nil {
		return "", nil, err
	}
	bootstrapData, bootstrapDigest, err := EncodeBootstrapShardRequest(request)
	if err != nil {
		return "", nil, err
	}
	if err := validateContentAddressedShardRequestKey(jobID, requestKey, fullDigest, ".request.json"); err != nil {
		return "", nil, err
	}
	if err := validateContentAddressedShardRequestKey(jobID, bootstrapRequestKey, bootstrapDigest, ".bootstrap.request.json"); err != nil {
		return "", nil, err
	}
	if err := os.WriteFile(bootstrapPath, bootstrapData, 0o600); err != nil {
		return "", nil, fmt.Errorf("write remote bootstrap shard request: %w", err)
	}
	if err := os.WriteFile(fullPath, fullData, 0o600); err != nil {
		return "", nil, fmt.Errorf("write remote shard request: %w", err)
	}
	uploadedKeys := make([]string, 0, 2)
	// create-only 上传的错误不等于远端未落对象（例如 ACK/409 不确定成功）。
	// 在每次调用前登记键，确保后续受限 job prefix cleanup 不遗漏可能副作用。
	uploadedKeys = append(uploadedKeys, bootstrapRequestKey)
	if err := coordinator.store.Create(ctx, bootstrapPath, bootstrapRequestKey); err != nil {
		return "", uploadedKeys, fmt.Errorf("upload remote bootstrap shard request: %w", err)
	}
	uploadedKeys = append(uploadedKeys, requestKey)
	if err := coordinator.store.Create(ctx, fullPath, requestKey); err != nil {
		return "", uploadedKeys, fmt.Errorf("upload remote shard request: %w", err)
	}
	group, err := coordinator.runtime.CreateContainerGroup(ctx, coordinator.createRequest(jobID, shard, resources, bootstrapRequestKey, bootstrapDigest, requestKey, fullDigest, request.ShardExecutionManifestDigest, input))
	if err != nil {
		return "", uploadedKeys, fmt.Errorf("create remote CI shard %d: %w", index, err)
	}
	return group.ID, uploadedKeys, nil
}

// buildRemotePlan 校验输入并构造权威 catalog 对应的分片执行计划。
func buildRemotePlan(
	input RunInput,
) (gate.GatePlan, gate.WorkloadCatalog, gate.CIEntrypoint, error) {
	if err := validateRemotePlanInput(input); err != nil {
		return gate.GatePlan{}, gate.WorkloadCatalog{}, gate.CIEntrypoint{}, err
	}
	entrypoint, err := gate.ResolveCIEntrypoint(input.Entrypoint, input.Source.Kind, input.Profile)
	if err != nil {
		return gate.GatePlan{}, gate.WorkloadCatalog{}, gate.CIEntrypoint{}, err
	}
	plan, err := gate.BuildGatePlan(input.Profile, input.Source)
	if err != nil {
		return gate.GatePlan{}, gate.WorkloadCatalog{}, gate.CIEntrypoint{}, err
	}
	catalog, err := remoteWorkloadCatalog(plan, input)
	if err != nil {
		return gate.GatePlan{}, gate.WorkloadCatalog{}, gate.CIEntrypoint{}, err
	}
	return plan, catalog, entrypoint, nil
}

// buildRemoteExecutionShardSetForWorkloads 绑定完整权威 catalog，并仅为显式 MISS 投影创建分片。
func buildRemoteExecutionShardSetForWorkloads(
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	executionIDs []gate.GateID,
	input RunInput,
) (gate.ContainerShardSet, error) {
	context := remotePlanningContext(input)
	compileInputs, compileAware, err := remoteCompileGroupInputsForExecution(executionIDs, input.WorkloadCompileGroupInputs)
	if err != nil {
		return gate.ContainerShardSet{}, err
	}
	var workloadPlan gate.WorkloadExecutionPlan
	if !compileAware {
		workloadPlan, err = gate.BuildWorkloadExecutionPlanForWorkloads(plan, catalog, input.LedgerSnapshot, context, executionIDs)
	} else {
		workloadPlan, err = gate.BuildWorkloadExecutionPlanForWorkloadsWithCompileInputs(
			plan,
			catalog,
			input.LedgerSnapshot,
			context,
			executionIDs,
			compileInputs,
		)
	}
	if err != nil {
		return gate.ContainerShardSet{}, err
	}
	set, err := gate.BuildContainerShardSetFromWorkloadPlan(
		plan, workloadPlan, input.RunnerIdentityDigest, input.RunnerConfigDigest,
	)
	return set, err
}

// validateRemotePlanInput 在计划构建前冻结候选树、OCI 基线、平台与 agent 身份。
// 校准与选测互斥，任一绑定漂移或资源规格不合法都必须在创建远程资源前失败。
func validateRemotePlanInput(input RunInput) error {
	if !completeRemotePlanInput(input) {
		return errors.New("remote CI run input is incomplete")
	}
	if err := validateRemotePlanBindings(input); err != nil {
		return err
	}
	if err := validateRemotePlanMode(input); err != nil {
		return err
	}
	if err := cicontract.ValidateAgentTokenDigest(input.AgentTokenDigest); err != nil {
		return fmt.Errorf("remote CI agent token digest: %w", err)
	}
	return nil
}

// validateRemotePlanBindings 校验候选 gate、OCI 基线与源码树均绑定到同一远程运行。
func validateRemotePlanBindings(input RunInput) error {
	if !remoteDigestPattern.MatchString(input.CandidateGateSourceSHA256) ||
		!remoteDigestPattern.MatchString(input.CandidateGateToolchainSHA256) {
		return errors.New("remote CI candidate gate identity is invalid")
	}
	if input.OCIProjectCache == nil {
		return errors.New("remote CI OCI project cache is required")
	}
	if err := input.OCIProjectCache.ValidateForBaseline(input.RunnerBaseTree, input.ToolchainDigest, input.Platform, input.RunnerImage); err != nil {
		return err
	}
	if input.Source.SourceTreeSHA != input.Tree {
		return errors.New("remote CI source tree does not match bundle tree")
	}
	return nil
}

// validateRemotePlanMode 约束校准、选测、平台及固定规格资源的合法组合。
func validateRemotePlanMode(input RunInput) error {
	if input.Calibration && input.SelectedTests {
		return errors.New("remote CI calibration cannot use selected tests")
	}
	if err := cicontract.ValidateTargetPlatform(input.Platform); err != nil {
		return err
	}
	if !input.Calibration {
		return nil
	}
	return cicontract.ValidateCalibrationResources(
		input.CalibrationResource.ID,
		input.CalibrationResource.VCPU,
		input.CalibrationResource.MemoryGiB,
	)
}

// completeRemotePlanInput 判断计划所需的仓库、镜像、平台与 OCI 身份是否齐全。
func completeRemotePlanInput(input RunInput) bool {
	return input.AcceptedGeneration != 0 && input.OCIProjectCache != nil && allRemotePlanStrings(
		input.RepositoryRoot, input.Tree, input.Base, input.RunnerBaseCommit, input.RunnerBaseTree,
		input.RunnerImage, input.Platform, input.PolicyDigest, input.ToolchainDigest,
		input.RunnerIdentityDigest, input.BaselineManifestDigest, input.RunnerConfigDigest,
		input.GateBinarySHA256, input.CandidateGateSourceSHA256, input.CandidateGateToolchainSHA256,
		input.RuntimeSeedSHA256,
	)
}

// allRemotePlanStrings 判断所有必填计划字符串均为非空的规范值。
func allRemotePlanStrings(values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return false
		}
	}
	return true
}

// remoteWorkloadCatalog 按普通、校准或选测模式构造单一权威 workload catalog。
func remoteWorkloadCatalog(plan gate.GatePlan, input RunInput) (gate.WorkloadCatalog, error) {
	policy := gate.DefaultWorkloadBootstrapPolicy()
	if input.Calibration {
		return gate.BuildCalibrationWorkloadCatalog(plan, policy, input.Inventory)
	}
	if input.SelectedTests {
		return gate.BuildSelectedTestWorkloadCatalog(plan, input.Inventory)
	}
	return gate.BuildExpandedWorkloadCatalog(plan, policy, input.Inventory)
}

// validateRemoteTemporaryObjectKeys 确保批量清理不会越过本次 job 的唯一临时前缀。
func validateRemoteTemporaryObjectKeys(prefix string, objectKeys []string) error {
	for _, key := range objectKeys {
		if key == prefix || !strings.HasPrefix(key, prefix) {
			return fmt.Errorf("remote CI temporary object %q escapes job prefix %q", key, prefix)
		}
	}
	return nil
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read digest input: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func randomJobID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "job-" + hex.EncodeToString(value[:]), nil
}
