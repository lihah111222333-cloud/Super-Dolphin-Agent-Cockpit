package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

type coordinatorOwner struct {
	schedulerOwner  *localci.SchedulerOwner
	schedulerClient *localci.SchedulerClient
	store           *coordinatorStore
	dependencies    coordinatorDependencies
	fatal           chan error
	workers         errgroup.Group
	closeOnce       sync.Once
	closeErr        error
}

// openCoordinatorOwner 先验证全部执行依赖，再竞争 scheduler owner singleton。
func openCoordinatorOwner(
	ctx context.Context,
	checkpoint localci.DockerDaemonIdentityCheckpoint,
	dependencies coordinatorDependencies,
) (*coordinatorOwner, error) {
	if err := dependencies.validate(); err != nil {
		return nil, err
	}
	schedulerOwner, err := localci.OpenSchedulerOwner(ctx, checkpoint.SchedulerConfig)
	if err != nil {
		return nil, err
	}
	schedulerClient, err := localci.DialScheduler(ctx, checkpoint.SchedulerConfig)
	if err != nil {
		return nil, errors.Join(err, schedulerOwner.Close())
	}
	store, err := openCoordinatorStore(ctx, checkpoint)
	if err != nil {
		return nil, errors.Join(err, schedulerClient.Close(), schedulerOwner.Close())
	}
	return &coordinatorOwner{
		schedulerOwner: schedulerOwner, schedulerClient: schedulerClient, store: store,
		dependencies: dependencies, fatal: make(chan error, 1),
	}, nil
}

// Serve 同时运行 transport 与 scheduler dispatch，并在任一基础设施失败时收口。
func (owner *coordinatorOwner) Serve(ctx context.Context) error {
	if owner == nil || ctx == nil {
		return fmt.Errorf("%w: owner and context are required", errCoordinatorDependency)
	}
	group, runCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return owner.schedulerOwner.Serve(runCtx) })
	group.Go(func() error { return owner.dispatch(runCtx) })
	runErr := group.Wait()
	workerErr := owner.workers.Wait()
	closeErr := owner.Close()
	return joinOwnerErrors(ctx, runErr, workerErr, closeErr)
}

// dispatch 按固定节拍取得 scheduler reservations，并监控 worker 基础设施失败。
func (owner *coordinatorOwner) dispatch(ctx context.Context) error {
	ticker := time.NewTicker(coordinatorPollInterval)
	defer ticker.Stop()
	for {
		reservations, err := owner.schedulerClient.ReserveRunnable(ctx)
		if err != nil {
			return fmt.Errorf("reserve coordinator jobs: %w", err)
		}
		owner.startReservations(ctx, reservations)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-owner.fatal:
			return err
		case <-ticker.C:
		}
	}
}

// startReservations 只启动已取得 lease 的 job，slot 上限由 scheduler 统一裁决。
func (owner *coordinatorOwner) startReservations(ctx context.Context, reservations []localci.WorkloadReservation) {
	for _, reservation := range reservations {
		jobID := reservation.WorkloadID
		owner.workers.Go(func() error {
			if err := owner.executeJob(ctx, jobID); err != nil {
				select {
				case owner.fatal <- err:
				default:
				}
				return err
			}
			return nil
		})
	}
}

func (owner *coordinatorOwner) executeJob(parent context.Context, jobID string) error {
	if err := owner.store.startJob(parent, jobID); err != nil {
		return owner.completeExecution(parent, jobID, jobStateInfraFailed, nil, err)
	}
	record, err := owner.store.job(parent, jobID)
	if err != nil {
		return owner.completeExecution(parent, jobID, jobStateInfraFailed, nil, err)
	}
	jobCtx, cancel := context.WithDeadline(parent, time.Now().Add(coordinatorTimeout(record.Profile)))
	defer cancel()
	results, state, runErr := owner.runJob(jobCtx, record)
	return owner.completeExecution(parent, jobID, state, results, runErr)
}

func (owner *coordinatorOwner) runJob(
	ctx context.Context,
	record coordinatorJobRecord,
) ([]gatecontract.GateResult, jobState, error) {
	image, err := owner.ensureJobImage(ctx, record)
	if err != nil {
		return nil, failureState(ctx, err), err
	}
	materialized, err := owner.materializeJobSource(ctx, record)
	if err != nil {
		return nil, failureState(ctx, err), err
	}
	results, state, runErr := owner.runPlanGates(ctx, record, image, materialized)
	cleanupErr := materialized.Cleanup()
	if cleanupErr != nil {
		return results, jobStateInfraFailed, errors.Join(runErr, fmt.Errorf("cleanup source snapshot: %w", cleanupErr))
	}
	return results, state, runErr
}

func (owner *coordinatorOwner) ensureJobImage(ctx context.Context, record coordinatorJobRecord) (ensuredImage, error) {
	image, err := owner.dependencies.ImageEnsurer.EnsureImage(ctx, imageEnsureRequest{
		RepositoryRoot: record.RepositoryRoot, Plan: record.Plan, JobSourceTreeSHA: record.JobSourceTreeSHA,
	})
	if err != nil {
		return ensuredImage{}, fmt.Errorf("ensure accepted image: %w", err)
	}
	if image.ImageProvenanceSourceTreeSHA == "" {
		return ensuredImage{}, errors.New("image provenance source tree SHA is required")
	}
	if err := owner.store.recordImageProvenance(ctx, record.JobID, image.ImageProvenanceSourceTreeSHA); err != nil {
		return ensuredImage{}, err
	}
	return image, nil
}

// materializeJobSource 验证 job tree 与清理责任均由物化结果明确提供。
func (owner *coordinatorOwner) materializeJobSource(
	ctx context.Context,
	record coordinatorJobRecord,
) (materializedJobSource, error) {
	outputRoot, err := coordinatorJobOutputRoot(record.JobID)
	if err != nil {
		return materializedJobSource{}, err
	}
	result, err := owner.dependencies.SourceMaterializer.Materialize(ctx, sourceMaterializeRequest{
		RepositoryRoot: record.RepositoryRoot, OutputRoot: outputRoot, Source: record.Plan.Source,
	})
	if err != nil {
		return materializedJobSource{}, fmt.Errorf("materialize job source: %w", err)
	}
	if result.SourceTreeSHA != record.JobSourceTreeSHA || result.SnapshotDir == "" || result.Cleanup == nil {
		return materializedJobSource{}, errors.New("materialized source result is incomplete or mismatched")
	}
	return result, nil
}

func (owner *coordinatorOwner) runPlanGates(
	ctx context.Context,
	record coordinatorJobRecord,
	image ensuredImage,
	source materializedJobSource,
) ([]gatecontract.GateResult, jobState, error) {
	results := make([]gatecontract.GateResult, 0, len(record.Plan.Gates))
	for _, gateSpec := range record.Plan.Gates {
		result, err := owner.dependencies.FreshRunner.RunFreshContainer(ctx, freshContainerRequest{
			Image: image.Identity, ImageTruth: image.Truth,
			ImageProvenanceSourceTreeSHA: image.ImageProvenanceSourceTreeSHA,
			JobSourceTreeSHA:             record.JobSourceTreeSHA, SourceSnapshotDir: source.SnapshotDir,
			Profile: record.Profile, Plan: record.Plan, GateID: gateSpec.ID,
		})
		if result.GateResult != nil {
			results = append(results, *result.GateResult)
		}
		if err != nil {
			return results, failureState(ctx, err), fmt.Errorf("run gate %q: %w", gateSpec.ID, err)
		}
		state := stateForContainerResult(result)
		if state != jobStatePassed {
			return results, state, nil
		}
	}
	return results, jobStatePassed, nil
}

func (owner *coordinatorOwner) completeExecution(
	ctx context.Context,
	jobID string,
	state jobState,
	results []gatecontract.GateResult,
	executionErr error,
) error {
	message := ""
	if executionErr != nil {
		message = executionErr.Error()
	}
	if err := owner.store.finishJob(ctx, jobID, state, results, message); err != nil {
		return err
	}
	if err := owner.schedulerClient.Complete(ctx, jobID, schedulerTerminalState(state)); err != nil {
		return fmt.Errorf("complete scheduler workload %q: %w", jobID, err)
	}
	return nil
}

func schedulerTerminalState(state jobState) localci.WorkloadStatus {
	if state == jobStatePassed {
		return localci.WorkloadStatusPassed
	}
	if state == jobStateInfraFailed {
		return localci.WorkloadStatusInfraFailed
	}
	return localci.WorkloadStatusFailed
}

func failureState(ctx context.Context, err error) jobState {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return jobStateTimeout
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return jobStateCancelled
	}
	return jobStateInfraFailed
}

// stateForContainerResult 严格映射容器直接观测结果，未知状态归基础设施失败。
func stateForContainerResult(result localci.FreshContainerResult) jobState {
	if result.Status == gatecontract.ResultStatusPassed && result.ExitCode == 0 {
		return jobStatePassed
	}
	switch result.Status {
	case gatecontract.ResultStatusFailed, gatecontract.ResultStatusPassedStalePolicy:
		return jobStateFailed
	case gatecontract.ResultStatusCancelled:
		return jobStateCancelled
	case gatecontract.ResultStatusTimeout:
		return jobStateTimeout
	default:
		return jobStateInfraFailed
	}
}

// coordinatorJobOutputRoot 为每个独立 job 分配不可复用的 owner-global 路径。
func coordinatorJobOutputRoot(jobID string) (string, error) {
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		return "", err
	}
	jobsRoot := filepath.Join(runtimeRoot, "jobs")
	if err := os.Mkdir(jobsRoot, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create coordinator jobs root: %w", err)
	}
	outputRoot := filepath.Join(jobsRoot, jobID)
	if _, err := os.Lstat(outputRoot); err == nil {
		return "", errors.New("coordinator job output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect coordinator job output: %w", err)
	}
	return outputRoot, nil
}

func joinOwnerErrors(parent context.Context, values ...error) error {
	filtered := make([]error, 0, len(values))
	for _, err := range values {
		if err == nil || (parent.Err() != nil && errors.Is(err, context.Canceled)) {
			continue
		}
		filtered = append(filtered, err)
	}
	return errors.Join(filtered...)
}

// Close 等待全部 job worker 后依次关闭 client、owner transport 与 store。
func (owner *coordinatorOwner) Close() error {
	if owner == nil {
		return localci.ErrSchedulerClosed
	}
	owner.closeOnce.Do(func() {
		owner.closeErr = errors.Join(owner.schedulerClient.Close(), owner.schedulerOwner.Close(), owner.store.close())
	})
	return owner.closeErr
}
