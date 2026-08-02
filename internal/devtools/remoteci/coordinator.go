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
	"sort"
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
const RunResultSchemaVersion uint32 = 8

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
	SchemaVersion                uint32                    `json:"schema_version"`
	AcceptedGeneration           uint64                    `json:"accepted_generation"`
	JobID                        string                    `json:"job_id"`
	RemoteName                   string                    `json:"remote_name,omitempty"`
	RemoteURL                    string                    `json:"remote_url,omitempty"`
	RequesterFingerprint         gate.RequesterFingerprint `json:"requester_fingerprint,omitempty"`
	Entrypoint                   gate.CIEntrypointID       `json:"entrypoint"`
	Profile                      gate.Profile              `json:"profile"`
	PlanDigest                   string                    `json:"plan_digest"`
	CatalogDigest                string                    `json:"catalog_digest"`
	SourceTreeSHA                string                    `json:"source_tree_sha"`
	CandidateGateSourceSHA256    string                    `json:"candidate_gate_source_sha256"`
	CandidateGateToolchainSHA256 string                    `json:"candidate_gate_toolchain_sha256"`
	RunnerImage                  string                    `json:"runner_image"`
	Status                       gate.ResultStatus         `json:"status"`
	Authoritative                bool                      `json:"authoritative"`
	StartedAt                    time.Time                 `json:"started_at"`
	CompletedAt                  time.Time                 `json:"completed_at"`
	Shards                       []ShardResult             `json:"shards"`
	GateExecutions               []gate.PlanGateExecution  `json:"gate_executions"`
	WorkloadExecutions           []gate.PlanGateExecution  `json:"workload_executions,omitempty"`
	DurationSamples              []gate.DurationSample     `json:"duration_samples"`
	OptimizationWarnings         []string                  `json:"optimization_warnings,omitempty"`
	CleanupComplete              bool                      `json:"cleanup_complete"`
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

// Run 在已接受的 ImageCache 上执行当前 catalog 的全部 workload。
func (coordinator *Coordinator) Run(ctx context.Context, input RunInput) (result RunResult, returnErr error) {
	if ctx == nil {
		return result, errors.New("remote CI context is required")
	}
	if input.LedgerStore == nil {
		return result, errors.New("remote CI duration ledger SQLite authority is required")
	}
	if err := validateRunImageCacheAuthority(coordinator.config, input); err != nil {
		return result, err
	}
	jobID, err := coordinator.newID()
	if err != nil {
		return result, fmt.Errorf("create remote CI job identity: %w", err)
	}
	plan, catalog, entrypoint, err := buildRemotePlan(input)
	var catalogDigest string
	if err == nil {
		catalogDigest, err = gate.WorkloadCatalogDigest(catalog)
	}
	if err != nil {
		return result, err
	}
	result = coordinator.newRunResult(plan, catalogDigest, catalog, entrypoint, input, jobID)
	defer func() {
		result.CompletedAt = coordinator.now().UTC()
		persistErr := recordRemoteCIRun(input.LedgerStore, result, returnErr)
		returnErr = errors.Join(returnErr, persistErr)
	}()
	if err := input.LedgerStore.RecordWorkloadCatalog(
		catalog,
		gate.WorkloadCatalogObservation{
			AcceptedGeneration: input.AcceptedGeneration,
			SourceTreeSHA:      input.Tree,
			Entrypoint:         entrypoint.ID,
			Profile:            plan.Profile,
			ObservedAt:         result.StartedAt,
		},
	); err != nil {
		return result, err
	}
	tempRoot, err := createRemoteTempRoot()
	if err != nil {
		return result, err
	}
	defer func() {
		tempCleanupErr := os.RemoveAll(tempRoot)
		returnErr = errors.Join(returnErr, tempCleanupErr)
	}()
	objectKeys := make([]string, 0)
	createdGroups := make([]string, 0)
	defer func() {
		cleanupErr := coordinator.cleanup(jobID, createdGroups, objectKeys)
		result.CleanupComplete = cleanupErr == nil
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	return coordinator.runAllWorkloads(
		ctx, input, plan, catalog, jobID, tempRoot, &objectKeys, &createdGroups, result,
	)
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

func recordRemoteCIRun(
	store *gate.DurationLedgerStore,
	result RunResult,
	runErr error,
) error {
	if store == nil {
		return errors.New("remote CI duration ledger SQLite authority is required")
	}
	shards := remoteCIShardRecords(result.Shards)
	timingObservations, err := remoteTimingObservations(result)
	if err != nil {
		return err
	}
	err = store.RecordRemoteCIRun(gate.RemoteCIRunRecord{
		JobID:                        result.JobID,
		AcceptedGeneration:           result.AcceptedGeneration,
		RequesterFingerprint:         result.RequesterFingerprint,
		Entrypoint:                   result.Entrypoint,
		Profile:                      result.Profile,
		PlanDigest:                   result.PlanDigest,
		CatalogDigest:                result.CatalogDigest,
		SourceTreeSHA:                result.SourceTreeSHA,
		CandidateGateSourceSHA256:    result.CandidateGateSourceSHA256,
		CandidateGateToolchainSHA256: result.CandidateGateToolchainSHA256,
		RunnerImage:                  result.RunnerImage,
		Status:                       result.Status,
		Authoritative:                result.Authoritative,
		StartedAt:                    result.StartedAt,
		CompletedAt:                  result.CompletedAt,
		CleanupComplete:              result.CleanupComplete,
		ErrorText:                    boundedRemoteRunErrorText(runErr),
		Shards:                       shards,
		Executions:                   result.GateExecutions,
		WorkloadExecutions:           result.WorkloadExecutions,
		Warnings:                     append([]string(nil), result.OptimizationWarnings...),
		TimingObservations:           timingObservations,
	})
	return err
}

// remoteTimingObservations emits the only authority input. Missing producer
// intervals are errors: they must never be recast as not_applicable.
func remoteTimingObservations(result RunResult) ([]gate.TimingObservation, error) {
	assignments := make(map[gate.GateID]string)
	for _, shard := range result.Shards {
		if shard.ShardIdentity == "" {
			return nil, errors.New("remote CI shard timing identity is required")
		}
		for _, workload := range shard.ExecutedWorkloads {
			if prior, duplicate := assignments[workload]; duplicate {
				return nil, fmt.Errorf("remote CI workload %q is assigned to shards %q and %q", workload, prior, shard.ShardIdentity)
			}
			assignments[workload] = shard.ShardIdentity
		}
	}
	byShard := make(map[string][]gate.PlanGateExecution, len(result.Shards))
	observations := make([]gate.TimingObservation, 0, len(result.Shards)*6+len(result.WorkloadExecutions)*6+1)
	for _, execution := range result.WorkloadExecutions {
		assigned, ok := assignments[execution.GateID]
		if !ok || execution.ShardIdentity == "" || execution.ShardIdentity != assigned {
			return nil, fmt.Errorf("remote CI workload %q shard identity is not bound to its executed shard", execution.GateID)
		}
		startupStart, startupEnd, bodyStart, bodyEnd, err := workloadPhaseIntervals(execution)
		if err != nil {
			return nil, err
		}
		cacheEvidence := gate.NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)
		for _, phase := range []cicontract.TimingPhase{cicontract.TimingECIWait, cicontract.TimingSourceMaterialize, cicontract.TimingCandidateCompile} {
			observations = append(observations, notApplicableWorkloadObservation(result.JobID, assigned, execution.GateID, phase, cacheEvidence))
		}
		for _, item := range []struct {
			phase              cicontract.TimingPhase
			started, completed time.Time
		}{
			{cicontract.TimingStartup, startupStart, startupEnd}, {cicontract.TimingTestBody, bodyStart, bodyEnd}, {cicontract.TimingTotal, execution.StartedAt, execution.CompletedAt},
		} {
			observation, err := timingObservation(result.JobID, cicontract.TimingScopeWorkload, assigned, execution.GateID, item.phase, item.started, item.completed, cicontract.TimingAggregationRaw, cacheEvidence)
			if err != nil {
				return nil, err
			}
			observations = append(observations, observation)
		}
		byShard[assigned] = append(byShard[assigned], execution)
	}
	for _, shard := range result.Shards {
		executions := byShard[shard.ShardIdentity]
		if len(executions) == 0 {
			return nil, fmt.Errorf("remote CI shard %q has no measured workload intervals", shard.ShardIdentity)
		}
		for _, item := range []struct {
			phase              cicontract.TimingPhase
			started, completed time.Time
		}{
			{cicontract.TimingECIWait, shard.ECIWaitStartedAt, shard.ECIWaitCompletedAt},
			{cicontract.TimingSourceMaterialize, time.UnixMilli(shard.MaterializationTiming.Source.StartedAtUnixMS), time.UnixMilli(shard.MaterializationTiming.Source.CompletedAtUnixMS)},
			{cicontract.TimingCandidateCompile, time.UnixMilli(shard.MaterializationTiming.CandidateCompile.StartedAtUnixMS), time.UnixMilli(shard.MaterializationTiming.CandidateCompile.CompletedAtUnixMS)},
		} {
			observation, err := timingObservation(result.JobID, cicontract.TimingScopeShard, shard.ShardIdentity, "", item.phase, item.started, item.completed, cicontract.TimingAggregationRaw, gate.NewNotApplicableCacheEvidence("not_workload_cache_evidence"))
			if err != nil {
				return nil, err
			}
			observations = append(observations, observation)
		}
		startup, body, err := shardWorkloadIntervals(executions)
		if err != nil {
			return nil, fmt.Errorf("remote CI shard %q workload intervals: %w", shard.ShardIdentity, err)
		}
		for _, item := range []struct {
			phase              cicontract.TimingPhase
			started, completed time.Time
			aggregation        cicontract.TimingAggregation
			durationMS         int64
		}{
			{cicontract.TimingStartup, startup.startedAt, startup.completedAt, cicontract.TimingAggregationIntervalUnion, startup.durationMS},
			{cicontract.TimingTestBody, body.startedAt, body.completedAt, cicontract.TimingAggregationIntervalUnion, body.durationMS},
			{cicontract.TimingTotal, shard.ECIWaitStartedAt, shard.ECITerminalAt, cicontract.TimingAggregationCriticalPath, shard.ECITerminalAt.Sub(shard.ECIWaitStartedAt).Milliseconds()},
		} {
			observation, err := timingObservation(result.JobID, cicontract.TimingScopeShard, shard.ShardIdentity, "", item.phase, item.started, item.completed, item.aggregation, gate.NewNotApplicableCacheEvidence("not_workload_cache_evidence"))
			if err != nil {
				return nil, err
			}
			observation.DurationMS = item.durationMS
			if err := observation.Validate(); err != nil {
				return nil, fmt.Errorf("remote CI shard %q %s duration: %w", shard.ShardIdentity, item.phase, err)
			}
			observations = append(observations, observation)
		}
	}
	runStartedAt, runCompletedAt, err := remoteRunCriticalPathEnvelope(result.Shards)
	if err != nil {
		return nil, err
	}
	run, err := timingObservation(result.JobID, cicontract.TimingScopeRun, "", "", cicontract.TimingTotal, runStartedAt, runCompletedAt, cicontract.TimingAggregationCriticalPath, gate.NewNotApplicableCacheEvidence("not_workload_cache_evidence"))
	if err != nil {
		return nil, err
	}
	return append(observations, run), nil
}

// remoteRunCriticalPathEnvelope derives the run total only from the observed
// shard totals, so the result proves that the run includes every shard.
func remoteRunCriticalPathEnvelope(shards []ShardResult) (time.Time, time.Time, error) {
	if len(shards) == 0 {
		return time.Time{}, time.Time{}, errors.New("remote CI run has no shard totals")
	}
	var startedAt, completedAt time.Time
	for _, shard := range shards {
		if shard.ECIWaitStartedAt.IsZero() || shard.ECITerminalAt.IsZero() || !shard.ECITerminalAt.After(shard.ECIWaitStartedAt) {
			return time.Time{}, time.Time{}, fmt.Errorf("remote CI shard %q total interval is missing or invalid", shard.ShardIdentity)
		}
		if startedAt.IsZero() || shard.ECIWaitStartedAt.Before(startedAt) {
			startedAt = shard.ECIWaitStartedAt
		}
		if completedAt.IsZero() || shard.ECITerminalAt.After(completedAt) {
			completedAt = shard.ECITerminalAt
		}
	}
	return startedAt, completedAt, nil
}

func notApplicableWorkloadObservation(jobID, shardIdentity string, workloadID gate.GateID, phase cicontract.TimingPhase, cacheEvidence gate.CacheEvidence) gate.TimingObservation {
	return gate.TimingObservation{JobID: jobID, Scope: cicontract.TimingScopeWorkload, ShardIdentity: shardIdentity, WorkloadID: workloadID, Phase: phase, Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:" + shardIdentity, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: cacheEvidence}
}

func timingObservation(jobID string, scope cicontract.TimingScope, shardIdentity string, workloadID gate.GateID, phase cicontract.TimingPhase, startedAt, completedAt time.Time, aggregation cicontract.TimingAggregation, cacheEvidence gate.CacheEvidence) (gate.TimingObservation, error) {
	observation := gate.TimingObservation{JobID: jobID, Scope: scope, ShardIdentity: shardIdentity, WorkloadID: workloadID, Phase: phase, StartedAt: startedAt.UTC(), CompletedAt: completedAt.UTC(), DurationMS: completedAt.Sub(startedAt).Milliseconds(), Measurement: cicontract.ObservationMeasured, Aggregation: aggregation, CacheEvidence: cacheEvidence}
	if err := observation.Validate(); err != nil {
		return gate.TimingObservation{}, fmt.Errorf("remote CI %s %s timing interval is missing or invalid: %w", scope, phase, err)
	}
	return observation, nil
}

func workloadPhaseIntervals(execution gate.PlanGateExecution) (time.Time, time.Time, time.Time, time.Time, error) {
	if execution.StartedAt.IsZero() || !execution.CompletedAt.After(execution.StartedAt) || execution.ExecutionProfile.StartupMS <= 0 || execution.ExecutionProfile.TestBodyMS <= 0 {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI workload %q measured startup and test-body intervals are required", execution.GateID)
	}
	startupEnd := execution.StartedAt.Add(time.Duration(execution.ExecutionProfile.StartupMS) * time.Millisecond)
	bodyStart := execution.CompletedAt.Add(-time.Duration(execution.ExecutionProfile.TestBodyMS) * time.Millisecond)
	if startupEnd.After(execution.CompletedAt) || bodyStart.Before(execution.StartedAt) {
		return time.Time{}, time.Time{}, time.Time{}, time.Time{}, fmt.Errorf("remote CI workload %q measured phase interval exceeds total", execution.GateID)
	}
	return execution.StartedAt, startupEnd, bodyStart, execution.CompletedAt, nil
}

type phaseIntervalUnion struct {
	startedAt, completedAt time.Time
	durationMS             int64
}

func shardWorkloadIntervals(executions []gate.PlanGateExecution) (phaseIntervalUnion, phaseIntervalUnion, error) {
	var startupIntervals, bodyIntervals []phaseIntervalUnion
	for _, execution := range executions {
		sStart, sEnd, bStart, bEnd, err := workloadPhaseIntervals(execution)
		if err != nil {
			return phaseIntervalUnion{}, phaseIntervalUnion{}, err
		}
		startupIntervals = append(startupIntervals, phaseIntervalUnion{startedAt: sStart, completedAt: sEnd})
		bodyIntervals = append(bodyIntervals, phaseIntervalUnion{startedAt: bStart, completedAt: bEnd})
	}
	startup, err := mergeWorkloadIntervals(startupIntervals)
	if err != nil {
		return phaseIntervalUnion{}, phaseIntervalUnion{}, err
	}
	body, err := mergeWorkloadIntervals(bodyIntervals)
	if err != nil {
		return phaseIntervalUnion{}, phaseIntervalUnion{}, err
	}
	return startup, body, nil
}

func mergeWorkloadIntervals(intervals []phaseIntervalUnion) (phaseIntervalUnion, error) {
	if len(intervals) == 0 {
		return phaseIntervalUnion{}, errors.New("no workload phase intervals")
	}
	sort.Slice(intervals, func(left, right int) bool { return intervals[left].startedAt.Before(intervals[right].startedAt) })
	merged := phaseIntervalUnion{startedAt: intervals[0].startedAt, completedAt: intervals[0].completedAt}
	segmentStart, segmentEnd := merged.startedAt, merged.completedAt
	for _, interval := range intervals[1:] {
		if interval.startedAt.After(segmentEnd) {
			merged.durationMS += segmentEnd.Sub(segmentStart).Milliseconds()
			segmentStart, segmentEnd = interval.startedAt, interval.completedAt
			continue
		}
		if interval.completedAt.After(segmentEnd) {
			segmentEnd = interval.completedAt
		}
	}
	merged.durationMS += segmentEnd.Sub(segmentStart).Milliseconds()
	merged.completedAt = segmentEnd
	return merged, nil
}

// runAllWorkloads 为当前 catalog 的全部 workload 规划、创建和聚合远程分片。
//
// 源差分和分片请求由 prepareRemoteShardRequests 作为同一资产边界构造。
// 每个分片随后只在物化后的候选源码内增量构建其 worker CLI。
// 上传、创建、等待和结果汇总仍在这里顺序编排，以保留可观测 phase。
// 任一步失败都会保留已获得的执行证据并由调用方统一清理临时对象。
func (coordinator *Coordinator) runAllWorkloads(
	ctx context.Context,
	input RunInput,
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	jobID string,
	tempRoot string,
	objectKeys *[]string,
	createdGroups *[]string,
	result RunResult,
) (RunResult, error) {
	set, err := buildRemoteExecutionShardSet(plan, catalog, input)
	if err != nil {
		return result, err
	}
	executionShards := set.Shards
	*objectKeys = make([]string, 0, 2+len(executionShards))
	*createdGroups = make([]string, 0, len(executionShards))
	resources, err := remoteExecutionShardResources(
		coordinator.config.ResourcePolicy,
		coordinator.config.ResourceObservations,
		set.WorkloadPlan.Catalog,
		executionShards,
		input,
	)
	if err != nil {
		return result, err
	}
	assets, err := coordinator.prepareRemoteAssets(
		ctx,
		input,
		jobID,
		tempRoot,
	)
	if err != nil {
		return result, err
	}
	if err := coordinator.uploadSourceAssets(ctx, assets, objectKeys); err != nil {
		return result, err
	}
	requests, requestKeys, err := buildShardRequests(coordinator.config.SourcePrefix, jobID, executionShards, assets.artifact, assets.patchKey, assets.manifestKey, assets.manifestDigest, input)
	if err != nil {
		return result, err
	}
	groupIDs, createErr := coordinator.uploadAndCreateRemoteGroups(
		ctx, tempRoot, jobID, executionShards,
		resources, requests, requestKeys,
		input, objectKeys, createdGroups,
	)
	executed, targetWarnings, waitErr := coordinator.waitShards(ctx, executionShards, groupIDs)
	result.OptimizationWarnings = appendUniqueRemoteWarnings(result.OptimizationWarnings, targetWarnings)
	if bindErr := bindRemoteShardResources(executed, resources, requests); bindErr != nil {
		waitErr = errors.Join(waitErr, bindErr)
	}
	fresh, freshErr := remoteFreshWorkloadExecutions(remoteShardableWorkloads(catalog), executed)
	observed, mergeErr := collectFreshRemoteWorkloadExecutions(remoteShardableWorkloads(catalog), fresh)
	if mergeErr == nil {
		result, mergeErr = coordinator.completeRemoteRun(catalog, input, executed, observed, result)
	} else {
		var durationErr error
		result, durationErr = coordinator.recordRemoteRunObservations(catalog, input, executed, result)
		mergeErr = errors.Join(durationErr, mergeErr)
	}
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
	result.DurationSamples = append(result.DurationSamples, parentSamples...)
	result.GateExecutions, result.WorkloadExecutions, result.Status = executions, workloadExecutions, status
	result.OptimizationWarnings = appendUniqueRemoteWarnings(
		result.OptimizationWarnings,
		remoteWorkloadTimingWarnings(result.WorkloadExecutions),
	)
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
	result.OptimizationWarnings = appendUniqueRemoteWarnings(result.OptimizationWarnings, remoteOptimizationWarnings(samples))
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
	return RunResult{SchemaVersion: RunResultSchemaVersion, AcceptedGeneration: input.AcceptedGeneration, JobID: jobID, RemoteName: input.RemoteName, RemoteURL: input.RemoteURL, RequesterFingerprint: input.RequesterFingerprint, Entrypoint: entrypoint.ID, Profile: plan.Profile, PlanDigest: plan.PlanDigest, CatalogDigest: catalogDigest, SourceTreeSHA: plan.Source.SourceTreeSHA, CandidateGateSourceSHA256: input.CandidateGateSourceSHA256, CandidateGateToolchainSHA256: input.CandidateGateToolchainSHA256, RunnerImage: input.RunnerImage, Status: gate.ResultStatusFailed, Authoritative: entrypoint.Authoritative && catalog.Authoritative, StartedAt: coordinator.now().UTC()}
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

// buildRemoteExecutionShardSet applies LPT to every current planned workload.
func buildRemoteExecutionShardSet(
	plan gate.GatePlan,
	catalog gate.WorkloadCatalog,
	input RunInput,
) (gate.ContainerShardSet, error) {
	context := remotePlanningContext(input)
	workloadPlan, err := gate.BuildWorkloadExecutionPlan(plan, catalog, input.LedgerSnapshot, context)
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
	if err := cicontract.ValidateTargetPlatform(input.Platform); err != nil {
		return err
	}
	if input.Calibration {
		if err := cicontract.ValidateCalibrationResources(input.CalibrationResource.ID, input.CalibrationResource.VCPU, input.CalibrationResource.MemoryGiB); err != nil {
			return err
		}
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
