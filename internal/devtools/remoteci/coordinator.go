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
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
	"golang.org/x/sync/errgroup"
)

// RunResultSchemaVersion is the current wire schema for remote run observations.
const RunResultSchemaVersion uint32 = 10

const remoteShardDiagnosticMaxBytes = 4 << 10

// ErrGateFailed marks a valid remote report whose gate outcome is not green.
var ErrGateFailed = errors.New("remote CI gate execution failed")

// ObjectStore owns remote objects. Create is atomic create-only; callers must
// never use it as an overwrite operation.
type ObjectStore interface {
	Create(context.Context, string, string) error
	DownloadIfExists(context.Context, string, string) (bool, error)
	List(context.Context, string) ([]string, error)
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
}

// RunInput binds one remote run to exact Git objects and one accepted runner identity.
type RunInput struct {
	AcceptedGeneration           uint64 `json:"accepted_generation"`
	RepositoryRoot               string
	RemoteName                   string
	RemoteURL                    string
	AgentTokenDigest             string
	Commit                       string
	Tree                         string
	Base                         string
	RunnerBaseCommit             string
	RunnerBaseTree               string
	Source                       gate.SourceSpec
	Profile                      gate.Profile
	Entrypoint                   gate.CIEntrypointID
	Platform                     string
	PolicyDigest                 string
	ToolchainDigest              string
	LedgerSnapshot               gate.DurationLedgerSnapshot
	LedgerStore                  *gate.DurationLedgerStore
	Inventory                    gate.WorkloadInventory
	SelectedTests                bool
	Calibration                  bool
	RunnerImage                  string
	ImageCacheSnapshotID         string
	RunnerIdentityDigest         string
	BaselineManifestDigest       string
	RunnerConfigDigest           string
	GateBinarySHA256             string
	CandidateGateSourceSHA256    string
	CandidateGateToolchainSHA256 string
	RuntimeSeedSHA256            string
	OCIProjectCache              *BaselineOCIProjectCache
	CalibrationResource          shardresource.Class
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
	ECIWaitStartedAt      time.Time                       `json:"eci_wait_started_at"`
	ECIWaitCompletedAt    time.Time                       `json:"eci_wait_completed_at"`
	ECITerminalAt         time.Time                       `json:"eci_terminal_at"`
	workerDiagnostic      string
}

// RunResult is an unsigned remote execution observation. Authority receipts are minted separately.
type RunResult struct {
	SchemaVersion                uint32                       `json:"schema_version"`
	AcceptedGeneration           uint64                       `json:"accepted_generation"`
	ImageCacheSnapshotID         string                       `json:"image_cache_snapshot_id"`
	JobID                        string                       `json:"job_id"`
	RemoteName                   string                       `json:"remote_name,omitempty"`
	RemoteURL                    string                       `json:"remote_url,omitempty"`
	AgentTokenDigest             string                       `json:"agent_token_digest"`
	Entrypoint                   gate.CIEntrypointID          `json:"entrypoint"`
	Profile                      gate.Profile                 `json:"profile"`
	PlanDigest                   string                       `json:"plan_digest"`
	CatalogDigest                string                       `json:"catalog_digest"`
	SourceTreeSHA                string                       `json:"source_tree_sha"`
	CandidateGateSourceSHA256    string                       `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string                       `json:"candidate_gate_toolchain_sha256"`
	RunnerImage                  string                       `json:"runner_image"`
	Status                       gate.ResultStatus            `json:"status"`
	Authoritative                bool                         `json:"authoritative"`
	StartedAt                    time.Time                    `json:"started_at"`
	CompletedAt                  time.Time                    `json:"completed_at"`
	Shards                       []ShardResult                `json:"shards"`
	GateExecutions               []gate.PlanGateExecution     `json:"gate_executions"`
	WorkloadExecutions           []gate.PlanGateExecution     `json:"workload_executions,omitempty"`
	FreshWorkloadExecutions      []gate.PlanGateExecution     `json:"-"`
	ReusedWorkloads              []gate.WorkloadPassEvidence  `json:"reused_workloads,omitempty"`
	CacheMissWorkloads           []gate.GateID                `json:"cache_miss_workloads,omitempty"`
	WorkloadPassIdentities       []gate.WorkloadPassIdentity  `json:"-"`
	DurationSamples              []gate.DurationSample        `json:"duration_samples"`
	OptimizationWarnings         []string                     `json:"optimization_warnings,omitempty"`
	TimingWarnings               []gate.RemoteCITimingWarning `json:"timing_warnings,omitempty"`
	CleanupComplete              bool                         `json:"cleanup_complete"`
}

// Coordinator executes exact Git sources with one ECI container per canonical shard.
type Coordinator struct {
	config             CoordinatorConfig
	store              ObjectStore
	runtime            Runtime
	observationTimeout time.Duration
	now                func() time.Time
	newID              func() (string, error)
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
	return &Coordinator{
		config:             config,
		store:              store,
		runtime:            runtime,
		observationTimeout: eci.MaxControlPlaneRetryDuration() + time.Minute,
		now:                time.Now,
		newID:              randomJobID,
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

// Run 保持单一执行路径：先无副作用地准备，再消费冻结结果执行。
func (coordinator *Coordinator) Run(ctx context.Context, input RunInput) (RunResult, error) {
	prepared, err := coordinator.Prepare(ctx, input)
	if err != nil {
		return RunResult{}, err
	}
	return coordinator.RunPrepared(ctx, prepared)
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

// validateRunImageCacheAuthority keeps every shard on the exact accepted ECI
// image snapshot; OCIProjectCache is evidence for the same immutable runtime image.
func validateRunImageCacheAuthority(config CoordinatorConfig, input RunInput) error {
	if !validImageCacheIdentifier(input.ImageCacheSnapshotID) || input.ImageCacheSnapshotID != config.ImageCacheSnapshotID {
		return errors.New("remote CI run ImageCacheSnapshotID must equal the accepted coordinator ImageCacheSnapshotID")
	}
	if input.OCIProjectCache == nil {
		return errors.New("remote CI run OCI project cache is required for image snapshot binding")
	}
	if err := input.OCIProjectCache.ValidateForBaseline(input.RunnerBaseTree, input.ToolchainDigest, input.Platform, input.RunnerImage); err != nil {
		return fmt.Errorf("remote CI run ImageCache OCI project cache binding: %w", err)
	}
	return nil
}

// remoteWorkloadMissInputs 保存当前未命中 workload 的冻结执行输入。
//
// 源差分和分片请求由 prepareRemoteShardRequests 作为同一资产边界构造。
// 每个分片随后只在物化后的候选源码内增量构建其 worker CLI。
// 上传、创建、等待和结果汇总仍在这里顺序编排，以保留可观测 phase。
// 任一步失败都会保留已获得的执行证据并由调用方统一清理临时对象。
type remoteWorkloadMissInputs struct {
	set         gate.ContainerShardSet
	resources   []shardresource.Class
	requests    []ShardRequest
	requestKeys []string
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
	*objectKeys = make([]string, 0, 2+len(executionShards))
	*createdGroups = make([]string, 0, len(executionShards))
	resources, err := remoteExecutionShardResources(
		coordinator.config.ResourcePolicy,
		coordinator.config.ResourceObservations,
		remoteExecutionCatalog(set.WorkloadPlan),
		executionShards,
		input,
	)
	if err != nil {
		return remoteWorkloadMissInputs{}, err
	}
	assets, err := coordinator.prepareRemoteAssets(ctx, input, jobID, tempRoot)
	if err != nil {
		return remoteWorkloadMissInputs{}, err
	}
	if err := coordinator.uploadSourceAssets(ctx, assets, objectKeys); err != nil {
		return remoteWorkloadMissInputs{}, err
	}
	requests, requestKeys, err := buildShardRequests(
		coordinator.config.SourcePrefix,
		jobID,
		executionShards,
		resources,
		assets.artifact,
		assets.patchKey,
		assets.manifestKey,
		assets.manifestDigest,
		input,
	)
	if err != nil {
		return remoteWorkloadMissInputs{}, err
	}
	return remoteWorkloadMissInputs{
		set:         set,
		resources:   resources,
		requests:    requests,
		requestKeys: requestKeys,
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
	observed := make(map[string]gate.PlanGateExecution, len(freshObserved)+len(reused))
	maps.Copy(observed, freshObserved)
	for workloadID, evidence := range reused {
		if _, duplicate := observed[workloadID]; duplicate {
			mergeErr = errors.Join(mergeErr, fmt.Errorf("remote workload %q is both fresh and reused", workloadID))
			continue
		}
		observed[workloadID] = evidence.OriginExecution
	}
	if mergeErr == nil {
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
		mergeErr = errors.Join(durationErr, mergeErr)
	}
	return result, freshErr, mergeErr
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
	if err != nil {
		return result, err
	}
	executionShards := prepared.set.Shards
	groupIDs, createErr := coordinator.uploadAndCreateRemoteGroups(
		ctx, tempRoot, jobID, executionShards,
		prepared.resources, prepared.requests, prepared.requestKeys,
		input, objectKeys, createdGroups,
	)
	executed, timingWarnings, waitErr := coordinator.waitShards(ctx, executionShards, groupIDs, remoteTimingWarningRun{
		jobID: jobID, agentTokenDigest: input.AgentTokenDigest,
		acceptedGeneration: input.AcceptedGeneration, store: input.LedgerStore,
	})
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

// completeRemoteRun 保留本次实际执行样本并汇总 ECI 的完整 workload 结果。
func (coordinator *Coordinator) completeRemoteRun(
	catalog gate.WorkloadCatalog,
	input RunInput,
	shards []ShardResult,
	observed map[string]gate.PlanGateExecution,
	result RunResult,
) (RunResult, error) {
	return coordinator.completeRemoteRunWithExecutionCatalog(catalog, catalog, input, shards, observed, observed, result)
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
	result, err = appendRemoteWorkloadTargetWarnings(result)
	if err != nil {
		return result, err
	}
	executions, workloadExecutions, status, err := aggregateRemoteReports(catalog, observed, shards)
	if err != nil {
		return result, err
	}
	parentSamples, err := remoteCalibrationParentDurationSamples(catalog, observed, input)
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
	samples, err := remoteDurationSamples(catalog, shards, input)
	result.DurationSamples = samples
	return result, err
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
	return RunResult{SchemaVersion: RunResultSchemaVersion, AcceptedGeneration: input.AcceptedGeneration, ImageCacheSnapshotID: input.ImageCacheSnapshotID, JobID: jobID, RemoteName: input.RemoteName, RemoteURL: input.RemoteURL, AgentTokenDigest: input.AgentTokenDigest, Entrypoint: entrypoint.ID, Profile: plan.Profile, PlanDigest: plan.PlanDigest, CatalogDigest: catalogDigest, SourceTreeSHA: plan.Source.SourceTreeSHA, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, RunnerImage: input.RunnerImage, Status: gate.ResultStatusFailed, Authoritative: false, StartedAt: coordinator.now().UTC()}
}

// createRemoteTempRoot 创建每次远程运行独占的临时源目录。
func createRemoteTempRoot() (string, error) {
	root, err := os.MkdirTemp("", "super-dolphin-remote-ci-*")
	if err != nil {
		return "", fmt.Errorf("create remote CI source staging root: %w", err)
	}
	return root, nil
}

// uploadAndCreateRemoteGroups 将每个缓存未命中分片的请求上传和 ECI 创建放入同一个可取消 worker。
func (coordinator *Coordinator) uploadAndCreateRemoteGroups(ctx context.Context, tempRoot string, jobID string, shards []gate.ContainerShard, resources []shardresource.Class, requests []ShardRequest, keys []string, input RunInput, objectKeys *[]string, created *[]string) ([]string, error) {
	if len(resources) != len(shards) || len(requests) != len(shards) || len(keys) != len(shards) {
		return nil, errors.New("remote CI shard resources or requests are incomplete")
	}
	ids := make([]string, len(shards))
	failures := make([]error, len(shards))
	workers, workerCtx := errgroup.WithContext(ctx)
	var mu sync.Mutex
	for index := range shards {
		workers.Go(func() error {
			groupID, requestUploaded, err := coordinator.uploadAndCreateRemoteGroup(
				workerCtx,
				tempRoot,
				jobID,
				index,
				shards[index],
				resources[index],
				requests[index],
				keys[index],
				input,
			)
			ids[index] = groupID
			failures[index] = err
			mu.Lock()
			if requestUploaded {
				*objectKeys = append(*objectKeys, keys[index])
			}
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

func (coordinator *Coordinator) uploadAndCreateRemoteGroup(ctx context.Context, tempRoot string, jobID string, index int, shard gate.ContainerShard, resources shardresource.Class, request ShardRequest, objectKey string, input RunInput) (string, bool, error) {
	path := filepath.Join(tempRoot, fmt.Sprintf("shard-%02d.request.json", index))
	data, digest, err := EncodeShardRequest(request)
	if err != nil {
		return "", false, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", false, fmt.Errorf("write remote shard request: %w", err)
	}
	if err := coordinator.store.Create(ctx, path, objectKey); err != nil {
		return "", false, fmt.Errorf("upload remote shard request: %w", err)
	}
	group, err := coordinator.runtime.CreateContainerGroup(ctx, coordinator.createRequest(jobID, shard, resources, objectKey, digest, input))
	if err != nil {
		return "", true, fmt.Errorf("create remote CI shard %d: %w", index, err)
	}
	return group.ID, true, nil
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

// buildRemoteExecutionShardSet 绑定完整权威 catalog，并仅为严格 miss 投影创建分片。
func buildRemoteExecutionShardSetForWorkloads(
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	executionIDs []gate.GateID,
	input RunInput,
) (gate.ContainerShardSet, error) {
	context := remotePlanningContext(input)
	workloadPlan, err := gate.BuildWorkloadExecutionPlanForWorkloads(plan, catalog, input.LedgerSnapshot, context, executionIDs)
	if err != nil {
		return gate.ContainerShardSet{}, err
	}
	set, err := gate.BuildContainerShardSetFromWorkloadPlan(
		plan, workloadPlan, input.RunnerIdentityDigest, input.RunnerConfigDigest,
	)
	return set, err
}

func buildRemoteExecutionShardSet(plan gate.GatePlan, catalog gate.WorkloadCatalog, input RunInput) (gate.ContainerShardSet, error) {
	ids := make([]gate.GateID, 0, len(catalog.Workloads))
	for _, workload := range catalog.Workloads {
		if workload.Shardable {
			ids = append(ids, gate.GateID(workload.ID))
		}
	}
	return buildRemoteExecutionShardSetForWorkloads(plan, catalog, ids, input)
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

// cleanup 在受限超时内并行回收本次 job 创建的 ECI 分组和同前缀 OSS 临时对象。
// 对象键先受 job 前缀约束；所有回收失败会汇总返回，不能因并发清理而丢失。
func (coordinator *Coordinator) cleanup(jobID string, groupIDs []string, objectKeys []string) error {
	ctx, cancel := gateprivate.WithTimeout(context.Background(), coordinator.config.CleanupTimeout)
	defer cancel()
	objectCleanup := 0
	if len(objectKeys) != 0 {
		objectCleanup = 1
	}
	failures := make([]error, len(groupIDs)+objectCleanup)
	var workers errgroup.Group
	for index, groupID := range groupIDs {
		workers.Go(func() error {
			if err := coordinator.runtime.DeleteContainerGroup(ctx, groupID); err != nil {
				failures[index] = fmt.Errorf("delete ECI container group %s: %w", groupID, err)
			}
			return nil
		})
	}
	if len(objectKeys) != 0 {
		workers.Go(func() error {
			prefix := coordinator.config.SourcePrefix + jobID + "/"
			if err := validateRemoteTemporaryObjectKeys(prefix, objectKeys); err != nil {
				failures[len(groupIDs)] = err
				return nil
			}
			if err := coordinator.store.DeletePrefix(ctx, prefix); err != nil {
				failures[len(groupIDs)] = fmt.Errorf("delete OSS job prefix %s: %w", prefix, err)
			}
			return nil
		})
	}
	_ = workers.Wait()
	return errors.Join(failures...)
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
