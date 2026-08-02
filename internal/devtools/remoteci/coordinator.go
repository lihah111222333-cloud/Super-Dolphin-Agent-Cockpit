package remoteci

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/alicloud/eci"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gateprivate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/shardresource"
	"golang.org/x/sync/errgroup"
)

// RunResultSchemaVersion is the current wire schema for remote run observations.
const RunResultSchemaVersion uint32 = 8

const remoteShardDiagnosticMaxBytes = 4 << 10

// ErrGateFailed marks a valid remote report whose gate outcome is not green.
var ErrGateFailed = errors.New("remote CI gate execution failed")

// ObjectStore owns remote objects. Create is atomic create-only; callers must
// never use it as an overwrite operation. UploadDirectory is reserved for the
// idempotent passed-workload cache publisher and must not carry job assets.
type ObjectStore interface {
	Create(context.Context, string, string) error
	UploadDirectory(context.Context, string, string, int) error
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
	Bucket                     string
	SourcePrefix               string
	WorkloadCachePrefix        string
	InternalOSSEndpoint        string
	WorkerRoleName             string
	WorkerTimeout              time.Duration
	PollInterval               time.Duration
	CleanupTimeout             time.Duration
	ResourcePolicy             shardresource.Policy
	ResourceObservations       []shardresource.Observation
	CandidateCLIBuilder        CandidateCLIBuilder
	CandidateTestBinaryBuilder CandidateTestBinaryBuilder
	RegistryAccess             eci.RegistryAccess
}

// RunInput binds one remote run to exact Git objects and one accepted runner identity.
type RunInput struct {
	RepositoryRoot               string
	RemoteName                   string
	RemoteURL                    string
	RequesterFingerprint         gate.RequesterFingerprint
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
	RunnerIdentityDigest         string
	BaselineManifestDigest       string
	RunnerConfigDigest           string
	GateBinarySHA256             string
	CandidateGateSourceSHA256    string
	CandidateGateToolchainSHA256 string
	ReuseBaselineGateCLI         bool
	RuntimeSeedSHA256            string
	OCIProjectCache              *BaselineOCIProjectCache
	RegistryAccess               eci.RegistryAccess
	ForceRerun                   bool
}

// ShardResult records directly observed ECI identity, terminal state, and worker report.
type ShardResult struct {
	ShardIdentity         string                          `json:"shard_identity"`
	ContainerGroup        string                          `json:"container_group_id"`
	ContainerStatus       string                          `json:"container_status"`
	ExecutedWorkloads     []gate.GateID                   `json:"executed_workloads,omitempty"`
	Report                gate.PlanExecutionReport        `json:"report"`
	MaterializationTiming gate.ShardMaterializationTiming `json:"materialization_timing"`
	workerDiagnostic      string
}

// RunResult is an unsigned remote execution observation. Authority receipts are minted separately.
type RunResult struct {
	SchemaVersion                           uint32                            `json:"schema_version"`
	JobID                                   string                            `json:"job_id"`
	RemoteName                              string                            `json:"remote_name,omitempty"`
	RemoteURL                               string                            `json:"remote_url,omitempty"`
	RequesterFingerprint                    gate.RequesterFingerprint         `json:"requester_fingerprint,omitempty"`
	Entrypoint                              gate.CIEntrypointID               `json:"entrypoint"`
	Profile                                 gate.Profile                      `json:"profile"`
	PlanDigest                              string                            `json:"plan_digest"`
	CatalogDigest                           string                            `json:"catalog_digest"`
	SourceTreeSHA                           string                            `json:"source_tree_sha"`
	CandidateCLIManifestSHA256              string                            `json:"candidate_cli_manifest_sha256"`
	CandidateTestBinaryBuilds               []CandidateTestBinaryBuilderBuild `json:"candidate_test_binary_builds,omitempty"`
	CandidateTestBinaryReceiptBindingDigest string                            `json:"candidate_test_binary_receipt_binding_digest"`
	RunnerImage                             string                            `json:"runner_image"`
	Status                                  gate.ResultStatus                 `json:"status"`
	Authoritative                           bool                              `json:"authoritative"`
	StartedAt                               time.Time                         `json:"started_at"`
	CompletedAt                             time.Time                         `json:"completed_at"`
	Shards                                  []ShardResult                     `json:"shards"`
	ReusedWorkloads                         []gate.GateID                     `json:"reused_workloads"`
	CacheMissWorkloads                      []gate.GateID                     `json:"cache_miss_workloads"`
	GateExecutions                          []gate.PlanGateExecution          `json:"gate_executions"`
	WorkloadExecutions                      []gate.PlanGateExecution          `json:"workload_executions,omitempty"`
	DurationSamples                         []gate.DurationSample             `json:"duration_samples"`
	PerformanceTimings                      []gate.RemoteCIPhaseTiming        `json:"performance_timings,omitempty"`
	OptimizationWarnings                    []string                          `json:"optimization_warnings,omitempty"`
	CleanupComplete                         bool                              `json:"cleanup_complete"`
}

// Coordinator executes exact Git sources with one ECI container per canonical shard.
type Coordinator struct {
	config             CoordinatorConfig
	store              ObjectStore
	runtime            Runtime
	observationTimeout time.Duration
	now                func() time.Time
	newID              func() (string, error)
	phaseObserver      PhaseObserver
}

// NewCoordinator 校验固定的 OSS 与 ECI 边界并构造协调器。
func NewCoordinator(
	config CoordinatorConfig,
	store ObjectStore,
	runtime Runtime,
	observers ...PhaseObserver,
) (*Coordinator, error) {
	if store == nil || runtime == nil {
		return nil, errors.New("remote CI object store and runtime are required")
	}
	if len(observers) > 1 {
		return nil, errors.New("remote CI coordinator accepts at most one phase observer")
	}
	if err := validateCoordinatorConfig(config); err != nil {
		return nil, err
	}
	var observer PhaseObserver
	if len(observers) == 1 {
		if observers[0] == nil {
			return nil, errors.New("remote CI phase observer is nil")
		}
		observer = observers[0]
	}
	return &Coordinator{
		config:             config,
		store:              store,
		runtime:            runtime,
		observationTimeout: eci.MaxControlPlaneRetryDuration() + time.Minute,
		now:                time.Now,
		newID:              randomJobID,
		phaseObserver:      observer,
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
	if config.CandidateCLIBuilder == nil {
		return errors.New("remote CI candidate CLI builder is required")
	}
	return nil
}

// validCoordinatorObjectConfig 判断 OSS 前缀、内网端点和 worker 角色是否完整且相互约束。
func validCoordinatorObjectConfig(config CoordinatorConfig) bool {
	return strings.TrimSpace(config.Bucket) != "" &&
		validObjectPrefix(config.SourcePrefix) &&
		validObjectPrefix(config.WorkloadCachePrefix) &&
		strings.HasPrefix(config.WorkloadCachePrefix, config.SourcePrefix) &&
		strings.TrimSpace(config.InternalOSSEndpoint) != "" &&
		strings.TrimSpace(config.WorkerRoleName) != ""
}

// Run 复用未变化 workload 的通过标记，只为缓存未命中项创建远程分片。
func (coordinator *Coordinator) Run(ctx context.Context, input RunInput) (result RunResult, returnErr error) {
	if ctx == nil {
		return result, errors.New("remote CI context is required")
	}
	jobID, err := coordinator.newID()
	if err != nil {
		return result, fmt.Errorf("create remote CI job identity: %w", err)
	}
	trace := newRemoteRunPerformanceTrace(jobID, coordinator.now, coordinator.phaseObserver)
	initializeSpan := trace.start("run.initialize", remoteCIPhaseCounts{})
	plan, catalog, entrypoint, err := buildRemotePlan(input)
	var catalogDigest string
	if err == nil {
		catalogDigest, err = gate.WorkloadCatalogDigest(catalog)
	}
	trace.finish(initializeSpan, err, remoteCIPhaseCounts{
		workloads: len(catalog.Workloads),
	})
	if err != nil {
		return result, errors.Join(err, trace.observerError())
	}
	result = coordinator.newRunResult(plan, catalogDigest, catalog, entrypoint, input, jobID)
	totalSpan := trace.start("run.total", remoteCIPhaseCounts{
		workloads: len(catalog.Workloads),
	})
	defer func() {
		result.CompletedAt = coordinator.now().UTC()
		trace.finish(totalSpan, returnErr, remoteCIPhaseCounts{
			workloads:   len(catalog.Workloads),
			shards:      len(result.Shards),
			cacheHits:   len(result.ReusedWorkloads),
			cacheMisses: len(result.CacheMissWorkloads),
		})
		result.PerformanceTimings = trace.snapshot()
		persistCounts := remoteCIPhaseCounts{
			workloads:   len(catalog.Workloads),
			shards:      len(result.Shards),
			cacheHits:   len(result.ReusedWorkloads),
			cacheMisses: len(result.CacheMissWorkloads),
		}
		persistSpan := trace.start("ledger.run_persist", persistCounts)
		persistErr := recordRemoteCIRun(input.LedgerStore, result, returnErr, trace)
		trace.finish(persistSpan, persistErr, persistCounts)
		result.PerformanceTimings = trace.snapshot()
		returnErr = errors.Join(returnErr, persistErr, trace.observerError())
	}()
	if input.LedgerStore != nil {
		catalogRecordCounts := remoteCIPhaseCounts{workloads: len(catalog.Workloads)}
		catalogRecordSpan := trace.start("ledger.catalog_record", catalogRecordCounts)
		if err := input.LedgerStore.RecordWorkloadCatalog(
			catalog,
			gate.WorkloadCatalogObservation{
				SourceTreeSHA: input.Tree,
				Entrypoint:    entrypoint.ID,
				Profile:       plan.Profile,
				ObservedAt:    result.StartedAt,
			},
		); err != nil {
			trace.finish(catalogRecordSpan, err, catalogRecordCounts)
			return result, err
		}
		trace.finish(catalogRecordSpan, nil, catalogRecordCounts)
	}
	tempRootSpan := trace.start("source.temp_create", remoteCIPhaseCounts{})
	tempRoot, err := createRemoteTempRoot()
	trace.finish(tempRootSpan, err, remoteCIPhaseCounts{})
	if err != nil {
		return result, err
	}
	defer func() {
		tempCleanupSpan := trace.start("source.temp_cleanup", remoteCIPhaseCounts{})
		tempCleanupErr := os.RemoveAll(tempRoot)
		trace.finish(tempCleanupSpan, tempCleanupErr, remoteCIPhaseCounts{})
		returnErr = errors.Join(returnErr, tempCleanupErr)
	}()
	objectKeys := make([]string, 0)
	createdGroups := make([]string, 0)
	defer func() {
		cleanupCounts := remoteCIPhaseCounts{shards: len(createdGroups)}
		cleanupSpan := trace.start("remote.cleanup", cleanupCounts)
		cleanupErr := coordinator.cleanup(jobID, createdGroups, objectKeys)
		trace.finish(cleanupSpan, cleanupErr, cleanupCounts)
		result.CleanupComplete = cleanupErr == nil
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	return coordinator.runWithWorkloadCache(
		ctx, input, plan, catalog, jobID, tempRoot, &objectKeys, &createdGroups, result, trace,
	)
}

func recordRemoteCIRun(
	store *gate.DurationLedgerStore,
	result RunResult,
	runErr error,
	traces ...*remoteRunPerformanceTrace,
) error {
	if store == nil {
		return nil
	}
	if result.Status == gate.ResultStatusPassed && result.CandidateCLIManifestSHA256 == "" &&
		len(result.CacheMissWorkloads) == 0 && len(result.ReusedWorkloads) > 0 {
		// A cache-only aggregation did not execute or materialize a candidate CLI, so it
		// has no candidate artifact identity to append as a remote execution ledger row.
		return nil
	}
	if len(traces) > 1 {
		return errors.New("remote CI run persistence accepts at most one performance trace")
	}
	shards := remoteCIShardRecords(result.Shards)
	projectionTimings, err := store.RecordRemoteCIRunProfiled(gate.RemoteCIRunRecord{
		JobID:                                   result.JobID,
		RequesterFingerprint:                    result.RequesterFingerprint,
		Entrypoint:                              result.Entrypoint,
		Profile:                                 result.Profile,
		PlanDigest:                              result.PlanDigest,
		CatalogDigest:                           result.CatalogDigest,
		SourceTreeSHA:                           result.SourceTreeSHA,
		CandidateCLIManifestSHA256:              result.CandidateCLIManifestSHA256,
		CandidateTestBinaryReceiptBindingDigest: result.CandidateTestBinaryReceiptBindingDigest,
		CandidateTestBinaryBuilds:               remoteCandidateTestBinaryBuildRecords(result.CandidateTestBinaryBuilds),
		RunnerImage:                             result.RunnerImage,
		Status:                                  result.Status,
		Authoritative:                           result.Authoritative,
		StartedAt:                               result.StartedAt,
		CompletedAt:                             result.CompletedAt,
		CleanupComplete:                         result.CleanupComplete,
		ErrorText:                               boundedRemoteRunErrorText(runErr),
		Shards:                                  shards,
		Executions:                              result.GateExecutions,
		WorkloadExecutions:                      result.WorkloadExecutions,
		ReusedWorkloads:                         append([]gate.GateID(nil), result.ReusedWorkloads...),
		CacheMisses:                             append([]gate.GateID(nil), result.CacheMissWorkloads...),
		Warnings:                                append([]string(nil), result.OptimizationWarnings...),
		PhaseTimings:                            append([]gate.RemoteCIPhaseTiming(nil), result.PerformanceTimings...),
	})
	if len(traces) == 1 {
		for _, timing := range projectionTimings {
			traces[0].record(timing)
		}
	}
	return err
}

func remoteCandidateTestBinaryBuildRecords(builds []CandidateTestBinaryBuilderBuild) []gate.CandidateTestBinaryBuildRecord {
	records := make([]gate.CandidateTestBinaryBuildRecord, 0, len(builds))
	for _, build := range builds {
		artifact := build.Artifact
		records = append(records, gate.CandidateTestBinaryBuildRecord{CandidateTree: artifact.CandidateTree, Package: artifact.Package, Mode: artifact.Mode, Platform: artifact.Platform, GoToolchain: artifact.GoToolchain, CGOEnabled: artifact.CGOEnabled, ToolchainSHA256: artifact.ToolchainSHA256, BuildFlags: append([]string(nil), artifact.BuildFlags...), CompileClosureSHA256: artifact.CompileClosureSHA256, ManifestSHA256: artifact.ManifestSHA256, ArtifactSHA256: "sha256:" + strings.TrimPrefix(artifact.BinarySHA256, "sha256:"), BinarySize: artifact.BinarySize, GoListWallMS: build.Metrics.GoListWallMS, BuildWallMS: build.Metrics.BuildWallMS, CompileActionMS: build.Metrics.CompileActionMS, LinkActionMS: build.Metrics.LinkActionMS, CompileCriticalWallMS: build.Metrics.CompileCriticalWallMS, GOCachePrivateHits: build.Metrics.GOCachePrivateHits, GOCacheOCIProjectCacheHits: build.Metrics.GOCacheOCIProjectCacheHits, GOCachePrivateRootIdentity: build.Metrics.GOCachePrivateRootIdentity, GOCacheMisses: build.Metrics.GOCacheMisses, GOCachePuts: build.Metrics.GOCachePuts})
	}
	return records
}

// runCacheMissWorkloads 仅为缓存未命中 workload 规划、创建和聚合远程分片。
//
// 源差分、候选 CLI 和分片请求由 prepareRemoteShardRequests 作为同一资产边界构造。
// 该边界确保候选制品在任一 ECI group 创建前已绑定到请求。
// 上传、创建、等待和结果汇总仍在这里顺序编排，以保留可观测 phase。
// 任一步失败都会保留已获得的执行证据并由调用方统一清理临时对象。
// 缓存写入错误会与远程执行错误一起返回，避免将局部成功误报为通过。
func (coordinator *Coordinator) runCacheMissWorkloads(
	ctx context.Context,
	input RunInput,
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	jobID string,
	tempRoot string,
	workerWorkloads []gate.Workload,
	cacheEntries []remoteWorkloadCacheEntry,
	cached map[string]gate.PlanGateExecution,
	reusedWorkloads []gate.GateID,
	goTestEntriesByParent map[string][]remoteWorkloadCacheEntry,
	objectKeys *[]string,
	createdGroups *[]string,
	result RunResult,
	trace *remoteRunPerformanceTrace,
) (RunResult, error) {
	planningCounts := remoteCIPhaseCounts{
		workloads: len(workerWorkloads) - len(reusedWorkloads),
		cacheHits: len(reusedWorkloads),
	}
	planningSpan := trace.start("planning.shards", planningCounts)
	set, err := buildRemoteExecutionShardSet(plan, catalog, reusedWorkloads, input)
	planningCounts.shards = len(set.Shards)
	trace.finish(planningSpan, err, planningCounts)
	if err != nil {
		return result, err
	}
	executionShards := set.Shards
	result.CacheMissWorkloads = remoteShardWorkloadIDs(executionShards)
	*objectKeys = make([]string, 0, 2+len(executionShards))
	*createdGroups = make([]string, 0, len(executionShards))
	resourceCounts := remoteCIPhaseCounts{
		workloads: len(result.CacheMissWorkloads),
		shards:    len(executionShards),
	}
	resourceSpan := trace.start("planning.resources", resourceCounts)
	resources, err := remoteExecutionShardResources(
		coordinator.config.ResourcePolicy,
		coordinator.config.ResourceObservations,
		set.WorkloadPlan.Catalog,
		executionShards,
		input,
	)
	trace.finish(resourceSpan, err, resourceCounts)
	if err != nil {
		return result, err
	}
	sourceCounts := remoteCIPhaseCounts{
		workloads: len(result.CacheMissWorkloads),
		shards:    len(executionShards),
	}
	assets, err := coordinator.prepareRemoteAssets(
		ctx,
		input,
		jobID,
		tempRoot,
		sourceCounts,
		trace,
	)
	if err != nil {
		return result, err
	}
	result.CandidateCLIManifestSHA256 = assets.candidateCLI.ManifestSHA256
	sourceUploadSpan := trace.start("source.upload", sourceCounts)
	if err := coordinator.uploadSourceAssets(ctx, assets, objectKeys); err != nil {
		trace.finish(sourceUploadSpan, err, sourceCounts)
		return result, err
	}
	trace.finish(sourceUploadSpan, nil, sourceCounts)
	candidateBuildSpan := trace.start("candidate_test_binary.builder", sourceCounts)
	if err := coordinator.buildCandidateTestBinaryArtifacts(ctx, input, executionShards, jobID, tempRoot, &assets, objectKeys, createdGroups); err != nil {
		trace.finish(candidateBuildSpan, err, sourceCounts)
		return result, err
	}
	trace.finish(candidateBuildSpan, nil, sourceCounts)
	result.CandidateTestBinaryBuilds = append([]CandidateTestBinaryBuilderBuild(nil), assets.candidateTestBinaryBuilds...)
	result.CandidateTestBinaryReceiptBindingDigest, err = CandidateTestBinaryReceiptBindingDigest(result.CandidateTestBinaryBuilds, result.SourceTreeSHA)
	if err != nil {
		return result, err
	}
	requestBuildSpan := trace.start("request.build", sourceCounts)
	requests, requestKeys, err := buildShardRequestsWithCandidate(coordinator.config.SourcePrefix, jobID, executionShards, assets.artifact, assets.patchKey, assets.manifestKey, assets.manifestDigest, assets.candidateCLI, assets.candidateTestBinaryRefs, input)
	trace.finish(requestBuildSpan, err, sourceCounts)
	if err != nil {
		return result, err
	}
	createSpan := trace.start("eci.create", sourceCounts)
	groupIDs, createErr := coordinator.uploadAndCreateRemoteGroups(
		ctx, tempRoot, jobID, executionShards,
		resources, requests, requestKeys,
		input, objectKeys, createdGroups,
	)
	trace.finish(createSpan, createErr, sourceCounts)
	waitSpan := trace.start("eci.wait", sourceCounts)
	executed, waitErr := coordinator.waitShards(ctx, executionShards, groupIDs)
	trace.finish(waitSpan, waitErr, sourceCounts)
	resultParseSpan := trace.start("result.parse", remoteCIPhaseCounts{
		shards: len(executed),
	})
	fresh, freshErr := remoteFreshWorkloadExecutions(workerWorkloads, executed)
	trace.finish(resultParseSpan, freshErr, remoteCIPhaseCounts{
		workloads: len(fresh),
		shards:    len(executed),
	})
	cachePersistCounts := remoteCIPhaseCounts{
		workloads: len(fresh),
		shards:    len(executed),
	}
	parentCacheSpan := trace.start("cache.persist_parent", cachePersistCounts)
	cacheErr := coordinator.storePassedWorkloadCache(
		ctx,
		tempRoot,
		cacheEntries,
		fresh,
		input.LedgerStore,
	)
	trace.finish(parentCacheSpan, cacheErr, cachePersistCounts)
	childCacheSpan := trace.start("cache.persist_children", cachePersistCounts)
	goTestCacheErr := coordinator.storePassedGoTestCache(
		ctx,
		tempRoot,
		goTestEntriesByParent,
		fresh,
		input.LedgerStore,
	)
	trace.finish(childCacheSpan, goTestCacheErr, cachePersistCounts)
	mergeCounts := remoteCIPhaseCounts{
		workloads:   len(workerWorkloads),
		shards:      len(executed),
		cacheHits:   len(cached),
		cacheMisses: len(fresh),
	}
	mergeSpan := trace.start("result.merge", mergeCounts)
	observed, mergeErr := mergeRemoteWorkloadExecutions(workerWorkloads, cached, fresh)
	trace.finish(mergeSpan, mergeErr, mergeCounts)
	if mergeErr == nil {
		aggregateSpan := trace.start("result.aggregate", mergeCounts)
		result, mergeErr = coordinator.completeRemoteRun(catalog, input, executed, observed, result)
		trace.finish(aggregateSpan, mergeErr, mergeCounts)
	} else {
		aggregateSpan := trace.start("result.aggregate_partial", mergeCounts)
		var durationErr error
		result, durationErr = coordinator.recordRemoteRunObservations(catalog, input, executed, result)
		mergeErr = errors.Join(durationErr, mergeErr)
		trace.finish(aggregateSpan, mergeErr, mergeCounts)
	}
	return result, errors.Join(createErr, waitErr, freshErr, cacheErr, goTestCacheErr, mergeErr)
}

// completeRemoteRun 保留本次实际执行样本并汇总缓存与 ECI 的完整 workload 结果。
func (coordinator *Coordinator) completeRemoteRun(
	catalog gate.WorkloadCatalog,
	input RunInput,
	shards []ShardResult,
	observed map[string]gate.PlanGateExecution,
	result RunResult,
) (RunResult, error) {
	result, err := coordinator.recordRemoteRunObservations(catalog, input, shards, result)
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
	result.OptimizationWarnings = remoteOptimizationWarnings(result.DurationSamples)
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
	result.OptimizationWarnings = remoteOptimizationWarnings(samples)
	return result, err
}

// newRunResult 初始化失败默认值的可审计远程运行结果。
func (coordinator *Coordinator) newRunResult(
	plan gate.GatePlan,
	catalogDigest string,
	catalog gate.WorkloadCatalog,
	entrypoint gate.CIEntrypoint,
	input RunInput,
	jobID string,
) RunResult {
	return RunResult{SchemaVersion: RunResultSchemaVersion, JobID: jobID, RemoteName: input.RemoteName, RemoteURL: input.RemoteURL, RequesterFingerprint: input.RequesterFingerprint, Entrypoint: entrypoint.ID, Profile: plan.Profile, PlanDigest: plan.PlanDigest, CatalogDigest: catalogDigest, SourceTreeSHA: plan.Source.SourceTreeSHA, CandidateTestBinaryReceiptBindingDigest: emptyCandidateTestBinaryReceiptBindingDigest(), RunnerImage: input.RunnerImage, Status: gate.ResultStatusFailed, Authoritative: entrypoint.Authoritative && catalog.Authoritative, StartedAt: coordinator.now().UTC()}
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
func (coordinator *Coordinator) uploadAndCreateRemoteGroups(ctx context.Context, tempRoot string, jobID string, shards []gate.ContainerShard, resources []eci.Resources, requests []ShardRequest, keys []string, input RunInput, objectKeys *[]string, created *[]string) ([]string, error) {
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

func (coordinator *Coordinator) uploadAndCreateRemoteGroup(ctx context.Context, tempRoot string, jobID string, index int, shard gate.ContainerShard, resources eci.Resources, request ShardRequest, objectKey string, input RunInput) (string, bool, error) {
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
	group, err := coordinator.runtime.CreateContainerGroup(ctx, coordinator.createRequest(jobID, shard, resources, objectKey, digest, request.CandidateCLI, input))
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

// buildRemoteExecutionShardSet applies LPT only after passed-workload cache hits are known.
func buildRemoteExecutionShardSet(
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	reusedWorkloads []gate.GateID,
	input RunInput,
) (gate.ContainerShardSet, error) {
	reused := make([]string, len(reusedWorkloads))
	for index, workloadID := range reusedWorkloads {
		reused[index] = string(workloadID)
	}
	context := remotePlanningContext(input)
	workloadPlan, err := gate.BuildWorkloadExecutionPlanWithReuse(
		plan, catalog, input.LedgerSnapshot, context, reused,
	)
	if err != nil {
		return gate.ContainerShardSet{}, err
	}
	set, err := gate.BuildContainerShardSetFromWorkloadPlan(
		plan, workloadPlan, input.RunnerIdentityDigest, input.RunnerConfigDigest,
	)
	return set, err
}

// validateRemotePlanInput 拒绝缺失绑定、树漂移和互斥的计划模式。
func validateRemotePlanInput(input RunInput) error {
	if !completeRemotePlanInput(input) {
		return errors.New("remote CI run input is incomplete")
	}
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
	if input.Calibration && input.SelectedTests {
		return errors.New("remote CI calibration cannot use selected tests")
	}
	if input.RequesterFingerprint != "" {
		if err := input.RequesterFingerprint.Validate(); err != nil {
			return fmt.Errorf("remote CI requester fingerprint: %w", err)
		}
	}
	return nil
}

// completeRemotePlanInput 判断计划所需的仓库、镜像、平台与 OCI 身份是否齐全。
func completeRemotePlanInput(input RunInput) bool {
	return input.OCIProjectCache != nil && allRemotePlanStrings(
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
