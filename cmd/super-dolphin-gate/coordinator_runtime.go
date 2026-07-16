package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const (
	coordinatorPollInterval   = 20 * time.Millisecond
	coordinatorConnectTimeout = 5 * time.Second
	coordinatorNormalTimeout  = 10 * time.Minute
	coordinatorReleaseTimeout = 30 * time.Minute
)

var (
	errCoordinatorDependency = errors.New("coordinator dependency is not wired")
	errCoordinatorState      = errors.New("invalid coordinator state")
	errCoordinatorNotFound   = errors.New("coordinator job not found")
)

type jobState string

const (
	jobStateQueued      jobState = "queued"
	jobStateStarted     jobState = "started"
	jobStatePassed      jobState = "passed"
	jobStateFailed      jobState = "failed"
	jobStateInfraFailed jobState = "infra_failed"
	jobStateCancelled   jobState = "cancelled"
	jobStateTimeout     jobState = "timeout"
)

func (state jobState) terminal() bool {
	return state == jobStatePassed || state == jobStateFailed || state == jobStateInfraFailed ||
		state == jobStateCancelled || state == jobStateTimeout
}

type submitRequest struct {
	RepositoryRoot string
	Plan           gatecontract.GatePlan
	InvocationID   string
}

type jobStatus struct {
	InvocationID                 string                    `json:"invocation_id"`
	JobID                        string                    `json:"job_id"`
	EnqueueSequence              uint64                    `json:"enqueue_sequence"`
	QueuePosition                int                       `json:"queue_position"`
	State                        jobState                  `json:"state"`
	Profile                      gatecontract.Profile      `json:"profile"`
	JobSourceTreeSHA             string                    `json:"job_source_tree_sha"`
	ImageProvenanceSourceTreeSHA string                    `json:"image_provenance_source_tree_sha,omitempty"`
	SubmittedAt                  time.Time                 `json:"submitted_at"`
	StartedAt                    *time.Time                `json:"started_at,omitempty"`
	CompletedAt                  *time.Time                `json:"completed_at,omitempty"`
	GateResults                  []gatecontract.GateResult `json:"gate_results,omitempty"`
	Error                        string                    `json:"error,omitempty"`
	Terminal                     bool                      `json:"terminal"`
}

type imageEnsureRequest struct {
	RepositoryRoot   string
	Plan             gatecontract.GatePlan
	JobSourceTreeSHA string
}

type ensuredImage struct {
	Identity                     gatecontract.ImageIdentity
	Truth                        localci.FreshContainerImageTruth
	ImageProvenanceSourceTreeSHA string
}

// ImageEnsurer 只负责解析或构建 accepted image，并返回独立 provenance tree。
type ImageEnsurer interface {
	EnsureImage(context.Context, imageEnsureRequest) (ensuredImage, error)
}

type candidateSubmissionPlanner interface {
	PlanCandidate(context.Context, imageEnsureRequest) (localci.PromotionCandidatePlan, error)
}

type candidateBuildService interface {
	ExecuteBuild(context.Context, string) error
}

type promotionWatcher interface {
	Run(context.Context) error
}

type sourceMaterializeRequest struct {
	RepositoryRoot string
	OutputRoot     string
	Source         gatecontract.SourceSpec
}

type materializedJobSource struct {
	SnapshotDir   string
	SourceTreeSHA string
	Cleanup       func() error
}

// SourceMaterializer 将 job source tree 物化为一次性只读执行输入。
type SourceMaterializer interface {
	Materialize(context.Context, sourceMaterializeRequest) (materializedJobSource, error)
}

type freshContainerRequest struct {
	Image                        gatecontract.ImageIdentity
	ImageTruth                   localci.FreshContainerImageTruth
	ImageProvenanceSourceTreeSHA string
	JobSourceTreeSHA             string
	SourceSnapshotDir            string
	Profile                      gatecontract.Profile
	Plan                         gatecontract.GatePlan
	GateID                       gatecontract.GateID
	ContainerLabels              map[string]string
	Deadline                     time.Time
	LifecycleHook                localci.FreshContainerLifecycleHook
}

// FreshContainerRunner 为每个 gate 创建独立容器，不允许宿主直接执行 gate。
type FreshContainerRunner interface {
	RunFreshContainer(context.Context, freshContainerRequest) (localci.FreshContainerResult, error)
}

// FreshContainerRecoveryRunner 验证、观察或清理重启前已存在的 Docker 容器。
type FreshContainerRecoveryRunner interface {
	RecoverFreshContainer(context.Context, localci.FreshContainerRecoveryRequest) (localci.FreshContainerResult, error)
	ProbeFreshContainerRecovery(context.Context, localci.FreshContainerRecoveryRequest) (localci.FreshContainerRecoveryObservation, error)
	CleanupUnprovedFreshContainer(context.Context, localci.FreshContainerCleanupRequest) (localci.FreshContainerResult, error)
}

type coordinatorDependencies struct {
	ImageEnsurer       ImageEnsurer
	CandidateBuilder   candidateBuildService
	PromotionWatcher   promotionWatcher
	SourceMaterializer SourceMaterializer
	FreshRunner        FreshContainerRunner
	RecoveryRunner     FreshContainerRecoveryRunner
}

// validate 要求 owner 执行、构建、晋升与物化依赖全部显式接线。
func (dependencies coordinatorDependencies) validate() error {
	if interfaceIsNil(dependencies.ImageEnsurer) {
		return fmt.Errorf("%w: ImageEnsurer is required", errCoordinatorDependency)
	}
	if interfaceIsNil(dependencies.CandidateBuilder) {
		return fmt.Errorf("%w: CandidateBuilder is required", errCoordinatorDependency)
	}
	if interfaceIsNil(dependencies.PromotionWatcher) {
		return fmt.Errorf("%w: PromotionWatcher is required", errCoordinatorDependency)
	}
	if interfaceIsNil(dependencies.SourceMaterializer) {
		return fmt.Errorf("%w: SourceMaterializer is required", errCoordinatorDependency)
	}
	if interfaceIsNil(dependencies.FreshRunner) {
		return fmt.Errorf("%w: FreshContainerRunner is required", errCoordinatorDependency)
	}
	if interfaceIsNil(dependencies.RecoveryRunner) {
		return fmt.Errorf("%w: FreshContainerRecoveryRunner is required", errCoordinatorDependency)
	}
	return nil
}

type coordinatorOwnerStarter interface {
	StartCoordinatorOwner(context.Context, localci.DockerDaemonIdentityCheckpoint) error
}

type coordinatorTransportClient struct {
	scheduler        *localci.SchedulerClient
	store            *coordinatorStore
	candidatePlanner candidateSubmissionPlanner
}

// connectCoordinator 自动发现 owner；发现失败时只允许经严格 starter 竞争启动。
func connectCoordinator(
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	starter coordinatorOwnerStarter,
) (*coordinatorTransportClient, error) {
	if ctx == nil {
		return nil, fmt.Errorf("%w: connect context is required", errCoordinatorDependency)
	}
	connectCtx, cancel := context.WithDeadline(ctx, time.Now().Add(coordinatorConnectTimeout))
	defer cancel()
	client, dialErr := dialCoordinator(connectCtx, checkpoint)
	if dialErr == nil {
		return client, nil
	}
	if interfaceIsNil(starter) {
		return nil, fmt.Errorf("%w: owner starter is required: %v", errCoordinatorDependency, dialErr)
	}
	startErr := starter.StartCoordinatorOwner(connectCtx, checkpoint)
	return waitForCoordinator(connectCtx, checkpoint, startErr)
}

func waitForCoordinator(
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	startErr error,
) (*coordinatorTransportClient, error) {
	ticker := time.NewTicker(coordinatorPollInterval)
	defer ticker.Stop()
	for {
		client, dialErr := dialCoordinator(ctx, checkpoint)
		if dialErr == nil {
			return client, nil
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(fmt.Errorf("connect coordinator owner: %w", ctx.Err()), startErr, dialErr)
		case <-ticker.C:
		}
	}
}

func dialCoordinator(
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
) (*coordinatorTransportClient, error) {
	scheduler, err := localci.DialScheduler(ctx, checkpoint.SchedulerConfig)
	if err != nil {
		return nil, err
	}
	store, err := openCoordinatorStore(ctx, checkpoint)
	if err != nil {
		return nil, errors.Join(err, scheduler.Close())
	}
	return &coordinatorTransportClient{scheduler: scheduler, store: store}, nil
}

// Submit 先 durable insert invocation/job，再同步 durable enqueue workload。
func (client *coordinatorTransportClient) Submit(ctx context.Context, request submitRequest) (jobStatus, error) {
	if client == nil || client.scheduler == nil || client.store == nil || ctx == nil {
		return jobStatus{}, fmt.Errorf("%w: connected client and context are required", errCoordinatorDependency)
	}
	normalized, invocationID, jobID, err := normalizeSubmitRequest(request)
	if err != nil {
		return jobStatus{}, err
	}
	status, reused, err := client.reusedSubmitStatus(ctx, normalized, invocationID)
	if err != nil || reused {
		return status, err
	}
	return client.createAndEnqueueSubmit(ctx, normalized, invocationID, jobID)
}

// normalizeSubmitRequest 校验 plan、规范化 repository root 并分配提交身份。
func normalizeSubmitRequest(request submitRequest) (submitRequest, string, string, error) {
	if err := request.Plan.Validate(); err != nil {
		return submitRequest{}, "", "", fmt.Errorf("validate submit plan: %w", err)
	}
	root, err := canonicalAbsolutePath("repository root", request.RepositoryRoot)
	if err != nil {
		return submitRequest{}, "", "", err
	}
	invocationID, jobID, err := submitCoordinatorIDs(request.InvocationID)
	if err != nil {
		return submitRequest{}, "", "", err
	}
	request.RepositoryRoot = root
	return request, invocationID, jobID, nil
}

// reusedSubmitStatus 只复用与同一 repository 和 plan 完全一致的 hook invocation。
func (client *coordinatorTransportClient) reusedSubmitStatus(
	ctx context.Context,
	request submitRequest,
	invocationID string,
) (jobStatus, bool, error) {
	if request.InvocationID == "" {
		return jobStatus{}, false, nil
	}
	existing, err := client.store.jobByInvocation(ctx, invocationID)
	if errors.Is(err, errCoordinatorNotFound) {
		return jobStatus{}, false, nil
	}
	if err != nil {
		return jobStatus{}, false, err
	}
	if err := validateReusedSubmit(existing, request); err != nil {
		return jobStatus{}, false, err
	}
	status, err := client.Status(ctx, existing.JobID)
	return status, true, err
}

// createAndEnqueueSubmit 持久化新 job 后将同一 workload 交给 owner scheduler。
func (client *coordinatorTransportClient) createAndEnqueueSubmit(
	ctx context.Context,
	request submitRequest,
	invocationID string,
	jobID string,
) (jobStatus, error) {
	record, err := client.store.createJob(ctx, invocationID, jobID, request.RepositoryRoot, request.Plan)
	if err != nil {
		return client.recoverConcurrentSubmit(ctx, invocationID, request, err)
	}
	plan := localci.PromotionCandidatePlan{}
	if client.candidatePlanner != nil {
		plan, err = client.candidatePlanner.PlanCandidate(ctx, imageEnsureRequest{
			RepositoryRoot: record.RepositoryRoot, Plan: record.Plan, JobSourceTreeSHA: record.JobSourceTreeSHA,
		})
		if err != nil {
			markErr := client.store.finishJob(ctx, record.JobID, jobStateInfraFailed, nil, "candidate planning failed: "+err.Error())
			return jobStatus{}, errors.Join(err, markErr)
		}
	}
	if err := client.enqueuePersistedJob(ctx, record, plan); err != nil {
		return jobStatus{}, err
	}
	return client.Status(ctx, jobID)
}

// recoverConcurrentSubmit 在唯一键竞争后只恢复已验证为同源的 invocation。
func (client *coordinatorTransportClient) recoverConcurrentSubmit(
	ctx context.Context,
	invocationID string,
	request submitRequest,
	createErr error,
) (jobStatus, error) {
	if request.InvocationID == "" {
		return jobStatus{}, createErr
	}
	existing, lookupErr := client.store.jobByInvocation(ctx, invocationID)
	if lookupErr != nil {
		return jobStatus{}, errors.Join(createErr, lookupErr)
	}
	if err := validateReusedSubmit(existing, request); err != nil {
		return jobStatus{}, errors.Join(createErr, err)
	}
	return client.Status(ctx, existing.JobID)
}

func validateReusedSubmit(record coordinatorJobRecord, request submitRequest) error {
	if record.InvocationID != request.InvocationID || record.RepositoryRoot != request.RepositoryRoot ||
		!reflect.DeepEqual(record.Plan, request.Plan) {
		return fmt.Errorf("%w: invocation %q was already bound to different repository or plan input",
			errCoordinatorState, request.InvocationID)
	}
	return nil
}

func (client *coordinatorTransportClient) enqueuePersistedJob(
	ctx context.Context,
	record coordinatorJobRecord,
	plan localci.PromotionCandidatePlan,
) error {
	dependencies := []string{}
	subsequence := uint32(0)
	if plan.BuildRequired {
		if err := client.ensureCandidateBuildEnqueued(ctx, record, plan.WorkloadID); err != nil {
			markErr := client.store.finishJob(ctx, record.JobID, jobStateInfraFailed, nil, "durable build enqueue failed: "+err.Error())
			return errors.Join(err, markErr)
		}
		dependencies = []string{plan.WorkloadID}
		subsequence = 1
	}
	request := localci.WorkloadRequest{
		ID: record.JobID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Subsequence: subsequence, Kind: localci.WorkloadKindJob, Dependencies: dependencies,
	}
	if err := client.scheduler.Enqueue(ctx, request); err != nil {
		markErr := client.store.finishJob(ctx, record.JobID, jobStateInfraFailed, nil, "durable scheduler enqueue failed: "+err.Error())
		return errors.Join(fmt.Errorf("enqueue persisted job %q: %w", record.JobID, err), markErr)
	}
	return nil
}

// ensureCandidateBuildEnqueued 幂等确认共享 build workload 已先于依赖 job 入队。
func (client *coordinatorTransportClient) ensureCandidateBuildEnqueued(
	ctx context.Context,
	record coordinatorJobRecord,
	workloadID string,
) error {
	if workloadID == "" {
		return errors.New("candidate build workload ID is required")
	}
	exists, err := client.schedulerWorkloadExists(ctx, workloadID)
	if err != nil || exists {
		return err
	}
	request := localci.WorkloadRequest{
		ID: workloadID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Subsequence: 0, Kind: localci.WorkloadKindBuild, Dependencies: []string{},
	}
	if err := client.scheduler.Enqueue(ctx, request); err == nil {
		return nil
	} else if exists, lookupErr := client.schedulerWorkloadExists(ctx, workloadID); lookupErr == nil && exists {
		return nil
	} else {
		return errors.Join(fmt.Errorf("enqueue candidate build %q: %w", workloadID, err), lookupErr)
	}
}

func (client *coordinatorTransportClient) schedulerWorkloadExists(ctx context.Context, workloadID string) (bool, error) {
	snapshot, err := client.scheduler.Snapshot(ctx)
	if err != nil {
		return false, err
	}
	for _, workload := range snapshot.Workloads {
		if workload.Request.ID == workloadID {
			return true, nil
		}
	}
	return false, nil
}

// Status 同时读取 scheduler 与 invocation store，并拒绝跨存储终态漂移。
func (client *coordinatorTransportClient) Status(ctx context.Context, jobID string) (jobStatus, error) {
	if client == nil || client.scheduler == nil || client.store == nil || ctx == nil {
		return jobStatus{}, fmt.Errorf("%w: connected client and context are required", errCoordinatorDependency)
	}
	return client.readStatusWithRetry(ctx, func() (coordinatorJobRecord, error) {
		return client.store.job(ctx, jobID)
	})
}

// StatusInvocation 按 hook invocation 与活动 worktree 查询同一个 durable job。
func (client *coordinatorTransportClient) StatusInvocation(
	ctx context.Context,
	invocationID string,
	repositoryRoot string,
) (jobStatus, error) {
	if client == nil || client.scheduler == nil || client.store == nil || ctx == nil {
		return jobStatus{}, fmt.Errorf("%w: connected client and context are required", errCoordinatorDependency)
	}
	root, err := canonicalAbsolutePath("repository root", repositoryRoot)
	if err != nil {
		return jobStatus{}, err
	}
	if err := validateHookInvocationID(invocationID); err != nil {
		return jobStatus{}, err
	}
	return client.readStatusWithRetry(ctx, func() (coordinatorJobRecord, error) {
		record, lookupErr := client.store.jobByInvocation(ctx, invocationID)
		if lookupErr != nil {
			return coordinatorJobRecord{}, lookupErr
		}
		if record.RepositoryRoot != root {
			return coordinatorJobRecord{}, fmt.Errorf(
				"%w: invocation repository does not match active worktree", errCoordinatorState,
			)
		}
		return record, nil
	})
}

// readStatusWithRetry 收敛 store 与 scheduler 之间的短暂状态过渡。
func (client *coordinatorTransportClient) readStatusWithRetry(
	ctx context.Context,
	load func() (coordinatorJobRecord, error),
) (jobStatus, error) {
	for attempt := range 10 {
		record, err := load()
		if err != nil {
			return jobStatus{}, err
		}
		status, err := client.readCoordinatorStatus(ctx, record)
		if err == nil || attempt == 9 {
			return status, err
		}
		if !errors.Is(err, errCoordinatorState) {
			return jobStatus{}, err
		}
		if err := waitCoordinatorStatusRetry(ctx); err != nil {
			return jobStatus{}, err
		}
	}
	return jobStatus{}, fmt.Errorf("%w: consistency retry exhausted", errCoordinatorState)
}

func waitCoordinatorStatusRetry(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Millisecond):
		return nil
	}
}

// readCoordinatorStatus 从同一 scheduler 快照计算状态与真实 FIFO 位置。
func (client *coordinatorTransportClient) readCoordinatorStatus(
	ctx context.Context,
	record coordinatorJobRecord,
) (jobStatus, error) {
	snapshot, err := client.scheduler.Snapshot(ctx)
	if err != nil {
		return jobStatus{}, fmt.Errorf("read scheduler snapshot for %q: %w", record.JobID, err)
	}
	schedulerState, queuePosition, err := schedulerJobObservation(snapshot, record.JobID)
	if err != nil {
		return jobStatus{}, err
	}
	if err := reconcileCoordinatorState(record.State, schedulerState); err != nil {
		return jobStatus{}, err
	}
	status := record.status()
	if record.State == jobStateQueued {
		if queuePosition <= 0 {
			return jobStatus{}, fmt.Errorf(
				"%w: queued job %q has no positive scheduler queue position", errCoordinatorState, record.JobID,
			)
		}
		status.QueuePosition = queuePosition
	}
	return status, nil
}

// schedulerJobObservation 在权威排序快照中定位 workload 及其 1-based queued 位置。
func schedulerJobObservation(snapshot localci.SchedulerSnapshot, jobID string) (localci.WorkloadStatus, int, error) {
	queuePosition := 0
	for _, workload := range snapshot.Workloads {
		if workload.Status == localci.WorkloadStatusQueued {
			queuePosition++
		}
		if workload.Request.ID != jobID {
			continue
		}
		if workload.Status == localci.WorkloadStatusQueued {
			return workload.Status, queuePosition, nil
		}
		return workload.Status, 0, nil
	}
	return "", 0, fmt.Errorf("%w: scheduler workload %q is missing", errCoordinatorState, jobID)
}

// Wait 轮询 owner transport，只有 durable 结构化终态才返回。
func (client *coordinatorTransportClient) Wait(ctx context.Context, jobID string) (jobStatus, error) {
	ticker := time.NewTicker(coordinatorPollInterval)
	defer ticker.Stop()
	for {
		status, err := client.Status(ctx, jobID)
		if err != nil || status.Terminal {
			return status, err
		}
		select {
		case <-ctx.Done():
			return jobStatus{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Close 关闭 client socket 与 SQLite 读写句柄。
func (client *coordinatorTransportClient) Close() error {
	if client == nil {
		return localci.ErrSchedulerClosed
	}
	return errors.Join(client.scheduler.Close(), client.store.close())
}

func newInvocationAndJobIDs() (string, string, error) {
	invocationID, err := newCoordinatorID("inv")
	if err != nil {
		return "", "", err
	}
	jobID, err := newCoordinatorID("job")
	return invocationID, jobID, err
}

func submitCoordinatorIDs(invocationID string) (string, string, error) {
	if invocationID == "" {
		return newInvocationAndJobIDs()
	}
	if err := validateHookInvocationID(invocationID); err != nil {
		return "", "", err
	}
	jobID, err := newCoordinatorID("job")
	return invocationID, jobID, err
}

// validateHookInvocationID 拒绝非 canonical SHA-256 hook invocation。
func validateHookInvocationID(invocationID string) error {
	const prefix = "hook-"
	if len(invocationID) != len(prefix)+64 || !strings.HasPrefix(invocationID, prefix) {
		return errors.New("hook invocation id must be hook- followed by a SHA-256 digest")
	}
	for _, character := range invocationID[len(prefix):] {
		if character < '0' || character > '9' && (character < 'a' || character > 'f') {
			return errors.New("hook invocation id digest must be lowercase hexadecimal")
		}
	}
	return nil
}

func newCoordinatorID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate coordinator %s ID: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}

func canonicalAbsolutePath(name, value string) (string, error) {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", fmt.Errorf("%s must be canonical and absolute", name)
	}
	return value, nil
}

// reconcileCoordinatorState 拒绝 durable store 与 scheduler 的状态漂移。
func reconcileCoordinatorState(state jobState, schedulerState localci.WorkloadStatus) error {
	if state == jobStateQueued && schedulerState != localci.WorkloadStatusQueued {
		return fmt.Errorf("%w: durable queued job and scheduler %q disagree", errCoordinatorState, schedulerState)
	}
	if state == jobStateStarted && schedulerState != localci.WorkloadStatusStarted {
		return fmt.Errorf("%w: durable started job and scheduler %q disagree", errCoordinatorState, schedulerState)
	}
	schedulerTerminal := schedulerState == localci.WorkloadStatusPassed ||
		schedulerState == localci.WorkloadStatusFailed || schedulerState == localci.WorkloadStatusInfraFailed
	if state.terminal() != schedulerTerminal {
		return fmt.Errorf("%w: durable job %q and scheduler %q disagree", errCoordinatorState, state, schedulerState)
	}
	return nil
}

func coordinatorTimeout(profile gatecontract.Profile) time.Duration {
	if profile == gatecontract.ProfileRelease {
		return coordinatorReleaseTimeout
	}
	return coordinatorNormalTimeout
}

func interfaceIsNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	return reflected.Kind() == reflect.Pointer && reflected.IsNil()
}
