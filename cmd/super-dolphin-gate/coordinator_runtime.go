package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
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
}

type jobStatus struct {
	InvocationID                 string                    `json:"invocation_id"`
	JobID                        string                    `json:"job_id"`
	EnqueueSequence              uint64                    `json:"enqueue_sequence"`
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
}

// FreshContainerRunner 为每个 gate 创建独立容器，不允许宿主直接执行 gate。
type FreshContainerRunner interface {
	RunFreshContainer(context.Context, freshContainerRequest) (localci.FreshContainerResult, error)
}

type coordinatorDependencies struct {
	ImageEnsurer       ImageEnsurer
	SourceMaterializer SourceMaterializer
	FreshRunner        FreshContainerRunner
}

func (dependencies coordinatorDependencies) validate() error {
	if interfaceIsNil(dependencies.ImageEnsurer) {
		return fmt.Errorf("%w: ImageEnsurer is required", errCoordinatorDependency)
	}
	if interfaceIsNil(dependencies.SourceMaterializer) {
		return fmt.Errorf("%w: SourceMaterializer is required", errCoordinatorDependency)
	}
	if interfaceIsNil(dependencies.FreshRunner) {
		return fmt.Errorf("%w: FreshContainerRunner is required", errCoordinatorDependency)
	}
	return nil
}

type coordinatorOwnerStarter interface {
	StartCoordinatorOwner(context.Context, localci.DockerDaemonIdentityCheckpoint) error
}

type coordinatorTransportClient struct {
	scheduler *localci.SchedulerClient
	store     *coordinatorStore
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
	if err := request.Plan.Validate(); err != nil {
		return jobStatus{}, fmt.Errorf("validate submit plan: %w", err)
	}
	root, err := canonicalAbsolutePath("repository root", request.RepositoryRoot)
	if err != nil {
		return jobStatus{}, err
	}
	invocationID, jobID, err := newInvocationAndJobIDs()
	if err != nil {
		return jobStatus{}, err
	}
	record, err := client.store.createJob(ctx, invocationID, jobID, root, request.Plan)
	if err != nil {
		return jobStatus{}, err
	}
	if err := client.enqueuePersistedJob(ctx, record); err != nil {
		return jobStatus{}, err
	}
	return client.Status(ctx, jobID)
}

func (client *coordinatorTransportClient) enqueuePersistedJob(ctx context.Context, record coordinatorJobRecord) error {
	request := localci.WorkloadRequest{
		ID: record.JobID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Kind: localci.WorkloadKindJob, Dependencies: []string{},
	}
	if err := client.scheduler.Enqueue(ctx, request); err != nil {
		markErr := client.store.finishJob(ctx, record.JobID, jobStateInfraFailed, nil, "durable scheduler enqueue failed: "+err.Error())
		return errors.Join(fmt.Errorf("enqueue persisted job %q: %w", record.JobID, err), markErr)
	}
	return nil
}

// Status 同时读取 scheduler 与 invocation store，并拒绝跨存储终态漂移。
func (client *coordinatorTransportClient) Status(ctx context.Context, jobID string) (jobStatus, error) {
	if client == nil || client.scheduler == nil || client.store == nil || ctx == nil {
		return jobStatus{}, fmt.Errorf("%w: connected client and context are required", errCoordinatorDependency)
	}
	for attempt := range 10 {
		status, err := client.readCoordinatorStatus(ctx, jobID)
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

func (client *coordinatorTransportClient) readCoordinatorStatus(ctx context.Context, jobID string) (jobStatus, error) {
	record, err := client.store.job(ctx, jobID)
	if err != nil {
		return jobStatus{}, err
	}
	schedulerState, err := client.scheduler.State(ctx, jobID)
	if err != nil {
		return jobStatus{}, fmt.Errorf("read scheduler state for %q: %w", jobID, err)
	}
	if err := reconcileCoordinatorState(record.State, schedulerState); err != nil {
		return jobStatus{}, err
	}
	return record.status(), nil
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

func reconcileCoordinatorState(state jobState, schedulerState localci.WorkloadStatus) error {
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
