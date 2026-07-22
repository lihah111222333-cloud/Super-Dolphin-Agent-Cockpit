package main

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const (
	coordinatorPollInterval           = 20 * time.Millisecond
	coordinatorConnectTimeout         = 5 * time.Second
	coordinatorStateTransitionTimeout = time.Second
	coordinatorCleanupTimeout         = 30 * time.Second
	coordinatorProvisioningTimeout    = 15 * time.Minute
	coordinatorCandidateBuildTimeout  = 45 * time.Minute
	coordinatorSourceSetupTimeout     = 15 * time.Minute
	coordinatorNormalTimeout          = 10 * time.Minute
	coordinatorReleaseTimeout         = 30 * time.Minute
	coordinatorPromotionPollMinMillis = int64(5_000)
	coordinatorPromotionPollMaxMillis = int64(60_000)
)

var (
	errCoordinatorDependency = errors.New("coordinator dependency is not wired")
	errCoordinatorState      = errors.New("invalid coordinator state")
	errCoordinatorTransition = errors.New("coordinator state transition is in progress")
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
	RepositoryRoot       string
	Plan                 gatecontract.GatePlan
	InvocationID         string
	Entrypoint           gatecontract.CIEntrypointID
	AuthorityOwner       gatecontract.CIEntrypointOwner
	AuthorityAttestation string
	VerifiedRelease      *verifiedReleaseAuthority
}

type submissionAuthority struct {
	Entrypoint  gatecontract.CIEntrypointID
	Owner       gatecontract.CIEntrypointOwner
	Attestation string
}

// verifiedReleaseAuthority is minted only by the release-owner verifier and
// deliberately cannot be represented by the CLI's parsed string flags.
type verifiedReleaseAuthority struct {
	attestation string
}

func (request submitRequest) authority() submissionAuthority {
	return submissionAuthority{Entrypoint: request.Entrypoint, Owner: request.AuthorityOwner, Attestation: request.AuthorityAttestation}
}

type jobStatus struct {
	InvocationID                     string                                 `json:"invocation_id"`
	JobID                            string                                 `json:"job_id"`
	EnqueueSequence                  uint64                                 `json:"enqueue_sequence"`
	QueuePosition                    int                                    `json:"queue_position"`
	State                            jobState                               `json:"state"`
	Profile                          gatecontract.Profile                   `json:"profile"`
	JobSourceTreeSHA                 string                                 `json:"job_source_tree_sha"`
	ImageProvenanceSourceTreeSHA     string                                 `json:"image_provenance_source_tree_sha,omitempty"`
	SubmittedAt                      time.Time                              `json:"submitted_at"`
	StartedAt                        *time.Time                             `json:"started_at,omitempty"`
	CompletedAt                      *time.Time                             `json:"completed_at,omitempty"`
	GateResults                      []gatecontract.GateResult              `json:"gate_results,omitempty"`
	ContainerHostConfigDigest        string                                 `json:"container_host_config_digest,omitempty"`
	ContainerResourceWitness         *gatecontract.ContainerResourceWitness `json:"container_resource_witness,omitempty"`
	ContainerResourceWitnessDigest   string                                 `json:"container_resource_witness_digest,omitempty"`
	ContainerResourceWitnessVerified bool                                   `json:"container_resource_witness_verified"`
	ReceiptID                        string                                 `json:"receipt_id,omitempty"`
	Error                            string                                 `json:"error,omitempty"`
	Terminal                         bool                                   `json:"terminal"`
}

type imageEnsureRequest struct {
	RepositoryRoot   string
	Plan             gatecontract.GatePlan
	JobSourceTreeSHA string
}

type ensuredImage struct {
	Identity                     gatecontract.ImageIdentity
	AcceptedRecord               gatecontract.AcceptedImageRecord
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
	PlanExecution                bool
	ShardGateIDs                 []gatecontract.GateID
	ShardIdentity                string
	ContainerLabels              map[string]string
	Deadline                     time.Time
	ClaimDeadline                func(context.Context, time.Time) (time.Time, error)
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
	ReceiptSigner      resultReceiptSigner
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
	if interfaceIsNil(dependencies.ReceiptSigner) {
		return fmt.Errorf("%w: ResultReceiptSigner is required", errCoordinatorDependency)
	}
	return nil
}

type coordinatorOwnerStarter interface {
	StartCoordinatorOwner(context.Context, localci.DockerDaemonIdentityCheckpoint) error
}

type coordinatorSchedulerClient interface {
	Enqueue(context.Context, localci.WorkloadRequest) error
	Snapshot(context.Context) (localci.SchedulerSnapshot, error)
	Available() bool
	Close() error
}

type coordinatorTransportClient struct {
	schedulerMu        sync.Mutex
	scheduler          coordinatorSchedulerClient
	schedulerConnector func(context.Context) (coordinatorSchedulerClient, error)
	store              *coordinatorStore
	candidatePlanner   candidateSubmissionPlanner
	closed             bool
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
	connector := func(connectCtx context.Context) (coordinatorSchedulerClient, error) {
		return connectScheduler(connectCtx, checkpoint, starter)
	}
	scheduler, err := connector(ctx)
	if err != nil {
		return nil, err
	}
	store, err := openCoordinatorStore(ctx, checkpoint)
	if err != nil {
		return nil, errors.Join(err, scheduler.Close())
	}
	return &coordinatorTransportClient{scheduler: scheduler, schedulerConnector: connector, store: store}, nil
}

func connectScheduler(
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	starter coordinatorOwnerStarter,
) (*localci.SchedulerClient, error) {
	connectCtx, cancel := context.WithDeadline(ctx, time.Now().Add(coordinatorConnectTimeout))
	defer cancel()
	client, dialErr := localci.DialScheduler(connectCtx, checkpoint.SchedulerConfig)
	if dialErr == nil {
		return client, nil
	}
	if interfaceIsNil(starter) {
		return nil, fmt.Errorf("%w: owner starter is required: %v", errCoordinatorDependency, dialErr)
	}
	startErr := starter.StartCoordinatorOwner(connectCtx, checkpoint)
	return waitForScheduler(connectCtx, checkpoint, startErr)
}

func waitForScheduler(
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	startErr error,
) (*localci.SchedulerClient, error) {
	ticker := time.NewTicker(coordinatorPollInterval)
	defer ticker.Stop()
	for {
		client, dialErr := localci.DialScheduler(ctx, checkpoint.SchedulerConfig)
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
	connector := func(connectCtx context.Context) (coordinatorSchedulerClient, error) {
		return localci.DialScheduler(connectCtx, checkpoint.SchedulerConfig)
	}
	scheduler, err := connector(ctx)
	if err != nil {
		return nil, err
	}
	store, err := openCoordinatorStore(ctx, checkpoint)
	if err != nil {
		return nil, errors.Join(err, scheduler.Close())
	}
	return &coordinatorTransportClient{scheduler: scheduler, schedulerConnector: connector, store: store}, nil
}

func newDeferredCoordinatorClient(
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	planner candidateSubmissionPlanner,
	connector func(context.Context) (*localci.SchedulerClient, error),
) (*coordinatorTransportClient, error) {
	if ctx == nil || interfaceIsNil(planner) || connector == nil {
		return nil, fmt.Errorf("%w: deferred planner and scheduler connector are required", errCoordinatorDependency)
	}
	store, err := openCoordinatorStore(ctx, checkpoint)
	if err != nil {
		return nil, err
	}
	return &coordinatorTransportClient{
		store: store, candidatePlanner: planner,
		schedulerConnector: func(connectCtx context.Context) (coordinatorSchedulerClient, error) {
			return connector(connectCtx)
		},
	}, nil
}

func (client *coordinatorTransportClient) schedulerForOperation(
	ctx context.Context,
) (coordinatorSchedulerClient, error) {
	if err := client.ensureScheduler(ctx); err != nil {
		return nil, err
	}
	client.schedulerMu.Lock()
	defer client.schedulerMu.Unlock()
	if client.closed || client.scheduler == nil || !client.scheduler.Available() {
		return nil, fmt.Errorf("%w: scheduler connection is unavailable", errCoordinatorDependency)
	}
	return client.scheduler, nil
}

func (client *coordinatorTransportClient) reconnectSchedulerAfterFailure(
	ctx context.Context,
	failed coordinatorSchedulerClient,
) (coordinatorSchedulerClient, error) {
	client.schedulerMu.Lock()
	if client.scheduler == failed {
		client.scheduler = nil
	}
	client.schedulerMu.Unlock()
	if failed != nil {
		_ = failed.Close()
	}
	return client.schedulerForOperation(ctx)
}

// Submit 先 durable insert invocation/job，再同步 durable enqueue workload。
func (client *coordinatorTransportClient) Submit(ctx context.Context, request submitRequest) (jobStatus, error) {
	if client == nil || client.store == nil || ctx == nil {
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
	plan, err := client.planCandidate(ctx, normalized)
	if err != nil {
		return jobStatus{}, err
	}
	return client.createAndEnqueueSubmit(ctx, normalized, invocationID, jobID, plan)
}

func (client *coordinatorTransportClient) planCandidate(
	ctx context.Context,
	request submitRequest,
) (localci.PromotionCandidatePlan, error) {
	if client.candidatePlanner == nil {
		return localci.PromotionCandidatePlan{}, nil
	}
	return client.candidatePlanner.PlanCandidate(ctx, imageEnsureRequest{
		RepositoryRoot: request.RepositoryRoot, Plan: request.Plan,
		JobSourceTreeSHA: request.Plan.Source.SourceTreeSHA,
	})
}

// normalizeSubmitRequest 校验 plan、规范化 repository root 并分配提交身份。
func normalizeSubmitRequest(request submitRequest) (submitRequest, string, string, error) {
	if err := request.Plan.Validate(); err != nil {
		return submitRequest{}, "", "", fmt.Errorf("validate submit plan: %w", err)
	}
	if err := validateSubmissionAuthority(request); err != nil {
		return submitRequest{}, "", "", err
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
	if err := client.ensurePersistedJobScheduled(ctx, existing); err != nil {
		return jobStatus{}, true, err
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
	plan localci.PromotionCandidatePlan,
) (jobStatus, error) {
	record, err := client.store.createJob(ctx, invocationID, jobID, request.RepositoryRoot, request.Plan, plan, request.authority())
	if err != nil {
		return client.recoverConcurrentSubmit(ctx, invocationID, request, err)
	}
	if err := client.ensurePersistedJobScheduled(ctx, record); err != nil {
		return jobStatus{}, err
	}
	return client.Status(ctx, jobID)
}

// ensurePersistedJobScheduled 对 durable queued 记录幂等补齐 scheduler workload。
func (client *coordinatorTransportClient) ensurePersistedJobScheduled(
	ctx context.Context,
	record coordinatorJobRecord,
) error {
	if record.State != jobStateQueued {
		return nil
	}
	if err := client.ensureScheduler(ctx); err != nil {
		return err
	}
	recovered, err := client.schedulerWorkloadExists(ctx, record.JobID)
	if err != nil {
		return fmt.Errorf("check recovered scheduler job %q: %w", record.JobID, err)
	}
	if recovered {
		return nil
	}
	return client.enqueuePersistedJob(ctx, record)
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
	if err := client.ensurePersistedJobScheduled(ctx, existing); err != nil {
		return jobStatus{}, errors.Join(createErr, err)
	}
	return client.Status(ctx, existing.JobID)
}

func validateReusedSubmit(record coordinatorJobRecord, request submitRequest) error {
	if record.InvocationID != request.InvocationID || record.RepositoryRoot != request.RepositoryRoot ||
		!reflect.DeepEqual(record.Plan, request.Plan) || !reflect.DeepEqual(record.Authority, request.authority()) {
		return fmt.Errorf("%w: invocation %q was already bound to different repository or plan input",
			errCoordinatorState, request.InvocationID)
	}
	return nil
}

// enqueuePersistedJob 将已持久化任务及其构建依赖幂等补入调度器，任一补入失败都会终结权威任务状态。
func (client *coordinatorTransportClient) enqueuePersistedJob(
	ctx context.Context,
	record coordinatorJobRecord,
) error {
	predecessorID, err := fifoPredecessorDependency(record)
	if err != nil {
		return err
	}
	if predecessorID != "" {
		predecessor, err := client.store.job(ctx, predecessorID)
		if err != nil {
			return fmt.Errorf("load durable FIFO predecessor for %q: %w", record.JobID, err)
		}
		if err := client.ensurePersistedJobScheduled(ctx, predecessor); err != nil {
			return fmt.Errorf("ensure durable FIFO predecessor for %q: %w", record.JobID, err)
		}
	}
	buildDependency, err := candidateBuildDependency(record)
	if err != nil {
		return err
	}
	if buildDependency != "" {
		if err := client.ensureCandidateBuildEnqueued(ctx, record, buildDependency); err != nil {
			return fmt.Errorf("ensure durable build enqueue for %q: %w", record.JobID, err)
		}
	}
	request := localci.WorkloadRequest{
		ID: record.JobID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Subsequence: record.SchedulerSubsequence, Kind: localci.WorkloadKindJob,
		Dependencies: append([]string(nil), record.SchedulerDependencies...),
	}
	return client.enqueueSchedulerWorkload(ctx, request)
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
	return client.enqueueSchedulerWorkload(ctx, request)
}

func (client *coordinatorTransportClient) schedulerWorkloadExists(ctx context.Context, workloadID string) (bool, error) {
	scheduler, err := client.schedulerForOperation(ctx)
	if err != nil {
		return false, err
	}
	return schedulerClientWorkloadExists(ctx, scheduler, workloadID)
}

func schedulerClientWorkloadExists(
	ctx context.Context,
	scheduler coordinatorSchedulerClient,
	workloadID string,
) (bool, error) {
	snapshot, err := scheduler.Snapshot(ctx)
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

// enqueueSchedulerWorkload 在连接故障后重连并观察幂等入队结果，未确认时保持 durable job 为 queued。
func (client *coordinatorTransportClient) enqueueSchedulerWorkload(
	ctx context.Context,
	request localci.WorkloadRequest,
) error {
	scheduler, err := client.schedulerForOperation(ctx)
	if err != nil {
		return err
	}
	enqueueErr := scheduler.Enqueue(ctx, request)
	if enqueueErr == nil {
		return nil
	}
	reconnected, reconnectErr := client.reconnectSchedulerAfterFailure(ctx, scheduler)
	if reconnectErr != nil {
		return errors.Join(
			fmt.Errorf("scheduler enqueue outcome for %q is unknown; durable workload remains queued: %w", request.ID, enqueueErr),
			reconnectErr,
		)
	}
	exists, observeErr := schedulerClientWorkloadExists(ctx, reconnected, request.ID)
	if observeErr != nil {
		return errors.Join(
			fmt.Errorf("observe scheduler enqueue outcome for %q; durable workload remains queued: %w", request.ID, enqueueErr),
			observeErr,
		)
	}
	if exists {
		return nil
	}
	retryErr := reconnected.Enqueue(ctx, request)
	if retryErr == nil {
		return nil
	}
	finalClient, finalReconnectErr := client.reconnectSchedulerAfterFailure(ctx, reconnected)
	if finalReconnectErr != nil {
		return errors.Join(
			fmt.Errorf("retry scheduler enqueue for %q is unknown; durable workload remains queued: %w", request.ID, retryErr),
			enqueueErr,
			finalReconnectErr,
		)
	}
	exists, finalObserveErr := schedulerClientWorkloadExists(ctx, finalClient, request.ID)
	if finalObserveErr == nil && exists {
		return nil
	}
	return errors.Join(
		fmt.Errorf("scheduler enqueue for %q remains unconfirmed; durable workload remains queued: %w", request.ID, enqueueErr),
		retryErr,
		finalObserveErr,
	)
}

// Status 同时读取 scheduler 与 invocation store，并拒绝跨存储终态漂移。
func (client *coordinatorTransportClient) Status(ctx context.Context, jobID string) (jobStatus, error) {
	if client == nil || client.store == nil || ctx == nil {
		return jobStatus{}, fmt.Errorf("%w: connected client and context are required", errCoordinatorDependency)
	}
	if err := client.ensureScheduler(ctx); err != nil {
		return jobStatus{}, err
	}
	return client.readStatusWithRetry(ctx, func() (coordinatorJobRecord, error) {
		return client.store.job(ctx, jobID)
	})
}

// ResultReceipt 从 durable store 返回指定 passed job 的权威回执副本。
func (client *coordinatorTransportClient) ResultReceipt(
	ctx context.Context,
	jobID string,
) (gatecontract.ResultReceipt, error) {
	if client == nil || client.store == nil || ctx == nil {
		return gatecontract.ResultReceipt{}, fmt.Errorf("%w: connected client and context are required", errCoordinatorDependency)
	}
	record, err := client.store.job(ctx, jobID)
	if err != nil {
		return gatecontract.ResultReceipt{}, err
	}
	if record.State != jobStatePassed || record.Receipt == nil {
		return gatecontract.ResultReceipt{}, fmt.Errorf("%w: job %q has no authoritative passed receipt", errCoordinatorState, jobID)
	}
	return cloneResultReceipt(*record.Receipt), nil
}

// StatusInvocation 按 hook invocation 与活动 worktree 查询同一个 durable job。
func (client *coordinatorTransportClient) StatusInvocation(
	ctx context.Context,
	invocationID string,
	repositoryRoot string,
) (jobStatus, error) {
	if client == nil || client.store == nil || ctx == nil {
		return jobStatus{}, fmt.Errorf("%w: connected client and context are required", errCoordinatorDependency)
	}
	if err := client.ensureScheduler(ctx); err != nil {
		return jobStatus{}, err
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
	return retryCoordinatorTransition(ctx, func() (jobStatus, error) {
		record, err := load()
		if err != nil {
			return jobStatus{}, err
		}
		return client.readCoordinatorStatus(ctx, record)
	})
}

// retryCoordinatorTransition 有界重试 scheduler 与 durable store 依次持久化产生的单向过渡。
func retryCoordinatorTransition(
	ctx context.Context,
	observe func() (jobStatus, error),
) (jobStatus, error) {
	transitionCtx, cancel := localci.BoundedOperationContext(ctx, coordinatorStateTransitionTimeout)
	defer cancel()
	for {
		status, err := observe()
		if err == nil || !errors.Is(err, errCoordinatorTransition) {
			return status, err
		}
		if waitErr := waitCoordinatorStatusRetry(transitionCtx); waitErr != nil {
			if ctx.Err() != nil {
				return jobStatus{}, ctx.Err()
			}
			return jobStatus{}, fmt.Errorf("%w: reservation did not reach durable started state: %v", errCoordinatorState, err)
		}
	}
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
	client.schedulerMu.Lock()
	if client.closed {
		client.schedulerMu.Unlock()
		return localci.ErrSchedulerClosed
	}
	client.closed = true
	scheduler := client.scheduler
	client.scheduler = nil
	client.schedulerMu.Unlock()
	var schedulerErr error
	if scheduler != nil {
		schedulerErr = scheduler.Close()
	}
	return errors.Join(schedulerErr, client.store.close())
}

// reconcileCoordinatorState 拒绝 durable store 与 scheduler 的状态漂移。
func reconcileCoordinatorState(state jobState, schedulerState localci.WorkloadStatus) error {
	if coordinatorTransitionInProgress(state, schedulerState) {
		return fmt.Errorf(
			"%w: durable job %q and scheduler %q are crossing a persisted state boundary",
			errCoordinatorTransition,
			state,
			schedulerState,
		)
	}
	if state == jobStateQueued && schedulerState == localci.WorkloadStatusQueued {
		return nil
	}
	if state == jobStateStarted && schedulerState == localci.WorkloadStatusStarted {
		return nil
	}
	if state.terminal() && schedulerState == schedulerTerminalState(state) {
		return nil
	}
	return fmt.Errorf("%w: durable job %q and scheduler %q disagree", errCoordinatorState, state, schedulerState)
}

// coordinatorTransitionInProgress 只识别 owner 固定持久化顺序产生的单调向前中间态。
func coordinatorTransitionInProgress(state jobState, schedulerState localci.WorkloadStatus) bool {
	if state == jobStateQueued && schedulerState == localci.WorkloadStatusStarted {
		return true
	}
	if (state == jobStateQueued || state == jobStateStarted) && schedulerWorkloadTerminal(schedulerState) {
		return true
	}
	return state.terminal() && schedulerState == localci.WorkloadStatusStarted
}
