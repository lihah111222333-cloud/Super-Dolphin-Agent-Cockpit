package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

type coordinatorOwner struct {
	schedulerOwner      *localci.SchedulerOwner
	schedulerClient     *localci.SchedulerClient
	store               *coordinatorStore
	dependencies        coordinatorDependencies
	daemonIdentityKey   string
	shardCleanupTimeout time.Duration
	schedulingPolicy    coordinatorSchedulingPolicy
	recovered           []coordinatorJobRecord
	fatal               chan error
	workers             errgroup.Group
	closeOnce           sync.Once
	closeErr            error
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
	schedulerConfig := checkpoint.SchedulerConfig
	schedulerConfig.MaxActiveWorkloads = dependencies.SchedulingPolicy.MaxActiveCIWorkloads
	schedulerOwner, err := localci.OpenSchedulerOwner(ctx, schedulerConfig)
	if err != nil {
		return nil, err
	}
	store, err := openCoordinatorStore(ctx, checkpoint)
	if err != nil {
		return nil, errors.Join(err, schedulerOwner.Close())
	}
	owner := &coordinatorOwner{
		schedulerOwner: schedulerOwner, store: store, dependencies: dependencies,
		daemonIdentityKey: checkpoint.IdentityKey, shardCleanupTimeout: coordinatorCleanupTimeout,
		schedulingPolicy: dependencies.SchedulingPolicy,
		fatal:            make(chan error, 1),
	}
	reconcileCtx, cancelReconcile := localci.BoundedOperationContext(context.WithoutCancel(ctx), coordinatorCleanupTimeout)
	owner.recovered, err = owner.reconcileRecovery(reconcileCtx)
	cancelReconcile()
	if err != nil {
		return nil, errors.Join(err, store.close(), schedulerOwner.Close())
	}
	connectCtx, cancelConnect := localci.BoundedOperationContext(context.WithoutCancel(ctx), coordinatorConnectTimeout)
	owner.schedulerClient, err = localci.DialScheduler(connectCtx, schedulerConfig)
	cancelConnect()
	if err != nil {
		return nil, errors.Join(err, store.close(), schedulerOwner.Close())
	}
	return owner, nil
}

// Serve 同时运行 transport 与 scheduler dispatch，并在任一基础设施失败时收口。
func (owner *coordinatorOwner) Serve(ctx context.Context) error {
	if owner == nil || ctx == nil {
		return fmt.Errorf("%w: owner and context are required", errCoordinatorDependency)
	}
	transportCtx, stopTransport := context.WithCancel(context.WithoutCancel(ctx))
	transportGroup := errgroup.Group{}
	transportGroup.Go(func() error {
		err := owner.schedulerOwner.Serve(transportCtx)
		if err != nil && transportCtx.Err() == nil {
			select {
			case owner.fatal <- fmt.Errorf("serve coordinator scheduler transport: %w", err):
			default:
			}
		}
		return err
	})
	group, runCtx := errgroup.WithContext(ctx)
	owner.startRecovered(runCtx)
	group.Go(func() error { return owner.dispatch(runCtx) })
	group.Go(func() error { return owner.dependencies.PromotionWatcher.Run(runCtx) })
	runErr := group.Wait()
	workerErr := owner.workers.Wait()
	clientCloseErr := owner.schedulerClient.Close()
	stopTransport()
	transportErr := transportGroup.Wait()
	closeErr := owner.Close()
	return joinOwnerErrors(ctx, runErr, workerErr, clientCloseErr, transportErr, closeErr)
}

// dispatch 按固定节拍取得 scheduler reservations，并监控 worker 基础设施失败。
func (owner *coordinatorOwner) dispatch(ctx context.Context) error {
	ticker := time.NewTicker(coordinatorPollInterval)
	defer ticker.Stop()
	for {
		reservations, err := owner.schedulerClient.ReserveRunnable(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
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

// startReservations 在 lease 持久化后按 workload 类型分派执行器。
func (owner *coordinatorOwner) startReservations(ctx context.Context, reservations []localci.WorkloadReservation) {
	for _, reservation := range reservations {
		owner.workers.Go(func() error {
			if err := owner.executeReservation(ctx, reservation); err != nil {
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

func (owner *coordinatorOwner) executeReservation(
	ctx context.Context,
	reservation localci.WorkloadReservation,
) error {
	kind, err := reservationWorkloadKind(reservation)
	if err != nil {
		return err
	}
	switch kind {
	case localci.WorkloadKindBuild:
		return owner.executeCandidateBuild(ctx, reservation.WorkloadID)
	case localci.WorkloadKindJob:
		return owner.executeJob(ctx, reservation.WorkloadID)
	case localci.WorkloadKindShard:
		return owner.executeShardGroup(ctx, reservation)
	default:
		return fmt.Errorf("unsupported coordinator reservation kind %q", kind)
	}
}

// reservationWorkloadKind 保留单 workload 语义，并验证 scheduler 的固定三片 lease group 表示。
func reservationWorkloadKind(reservation localci.WorkloadReservation) (localci.WorkloadKind, error) {
	if reservation.WorkloadID == "" || len(reservation.Leases) == 0 {
		return "", errors.New("coordinator reservation is incomplete")
	}
	if reservation.GroupIdentity != "" {
		return validateShardReservation(reservation)
	}
	kind := reservation.Leases[0].Kind
	for _, lease := range reservation.Leases {
		if lease.WorkloadID != reservation.WorkloadID || lease.Kind != kind ||
			lease.GroupIdentity != "" || lease.ShardIdentity != "" {
			return "", errors.New("coordinator reservation lease identity drifted")
		}
	}
	return kind, nil
}

// validateShardReservation 验证 primary shard 与后续 service lease 的固定 scheduler 表示。
func validateShardReservation(reservation localci.WorkloadReservation) (localci.WorkloadKind, error) {
	if len(reservation.Leases) != 3 {
		return "", errors.New("coordinator shard reservation must occupy exactly three slots")
	}
	seenLease := make(map[string]struct{}, len(reservation.Leases))
	seenShard := make(map[string]struct{}, len(reservation.Leases))
	for index, lease := range reservation.Leases {
		if !validShardLeaseBinding(reservation, lease, index) {
			return "", errors.New("coordinator shard reservation identity drifted")
		}
		if _, duplicate := seenLease[lease.ID]; duplicate {
			return "", errors.New("coordinator shard reservation has duplicate lease")
		}
		seenLease[lease.ID] = struct{}{}
		if _, duplicate := seenShard[lease.ShardIdentity]; duplicate {
			return "", errors.New("coordinator shard reservation has duplicate shard identity")
		}
		seenShard[lease.ShardIdentity] = struct{}{}
	}
	return localci.WorkloadKindShard, nil
}

// validShardLeaseBinding 校验 gang 中主 shard 与伴随 service lease 的稳定身份和顺序绑定。
func validShardLeaseBinding(reservation localci.WorkloadReservation, lease localci.Lease, index int) bool {
	if lease.ID == "" || lease.WorkloadID != reservation.WorkloadID ||
		lease.GroupIdentity != reservation.GroupIdentity || lease.ShardIdentity == "" {
		return false
	}
	if index == 0 {
		return lease.Kind == localci.WorkloadKindShard && lease.ID == reservation.WorkloadID+"/shard"
	}
	return lease.Kind == localci.WorkloadKindService &&
		lease.ID == fmt.Sprintf("%s/service/%d", reservation.WorkloadID, index)
}

func (owner *coordinatorOwner) executeCandidateBuild(ctx context.Context, workloadID string) error {
	buildCtx, cancel := localci.BoundedOperationContext(ctx, coordinatorCandidateBuildTimeout)
	err := owner.dependencies.CandidateBuilder.ExecuteBuild(buildCtx, workloadID)
	cancel()
	status := localci.WorkloadStatusPassed
	if err != nil {
		status = localci.WorkloadStatusInfraFailed
	}
	cleanupCtx, cleanupCancel := coordinatorCleanupContext(ctx)
	defer cleanupCancel()
	if completeErr := owner.schedulerClient.Complete(cleanupCtx, workloadID, status); completeErr != nil {
		return errors.Join(err, fmt.Errorf("complete candidate build workload %q: %w", workloadID, completeErr))
	}
	if err != nil {
		return owner.failCandidateBuildDependents(cleanupCtx, workloadID,
			fmt.Errorf("execute candidate build workload %q within candidate build timeout: %w", workloadID, err))
	}
	return nil
}

// failCandidateBuildDependents 将构建错误写入全部排队依赖任务，但不把预期的 workload 失败升级为 owner 进程故障。
func (owner *coordinatorOwner) failCandidateBuildDependents(ctx context.Context, workloadID string, buildErr error) error {
	records, err := owner.store.jobs(ctx)
	if err != nil {
		return errors.Join(buildErr, fmt.Errorf("load candidate build dependents: %w", err))
	}
	var completionErrors []error
	for _, record := range records {
		if record.State != jobStateQueued || !slices.Contains(record.SchedulerDependencies, workloadID) {
			continue
		}
		if err := owner.completeExecution(ctx, record, receiptExecution{}, jobStateInfraFailed, buildErr); err != nil {
			completionErrors = append(completionErrors, fmt.Errorf("fail candidate build dependent %q: %w", record.JobID, err))
		}
	}
	return errors.Join(completionErrors...)
}

func (owner *coordinatorOwner) executeJob(parent context.Context, jobID string) error {
	if err := owner.store.startJob(parent, jobID); err != nil {
		return owner.completeExecution(parent, coordinatorJobRecord{JobID: jobID}, receiptExecution{}, jobStateInfraFailed, err)
	}
	record, err := owner.store.job(parent, jobID)
	if err != nil {
		return owner.completeExecution(parent, coordinatorJobRecord{JobID: jobID}, receiptExecution{}, jobStateInfraFailed, err)
	}
	return owner.admitContainerShards(parent, record)
}

// admitContainerShards 固化真相镜像与 canonical shard 集合后，持久化并排队整组 gang admission。
func (owner *coordinatorOwner) admitContainerShards(ctx context.Context, record coordinatorJobRecord) error {
	imageCtx, cancel := localci.BoundedOperationContext(ctx, coordinatorProvisioningTimeout)
	image, err := owner.ensureJobImage(imageCtx, record)
	cancel()
	if err != nil {
		return owner.completeExecution(ctx, record, receiptExecution{}, jobStateInfraFailed,
			fmt.Errorf("provision truth image within provisioning timeout: %w", err))
	}
	set, err := owner.buildContainerShardSet(record.Plan, image.Identity.PlatformManifestDigest, image.Identity.ConfigDigest)
	if err != nil {
		return owner.completeExecution(ctx, record, receiptExecution{}, jobStateInfraFailed, err)
	}
	admission, err := owner.store.prepareShardAdmission(ctx, record, set)
	if err == nil {
		err = owner.enqueueShardAdmission(ctx, record, admission)
	}
	if err != nil {
		return owner.completeExecution(ctx, record, receiptExecution{}, jobStateInfraFailed, err)
	}
	if err := owner.schedulerClient.Complete(ctx, record.JobID, localci.WorkloadStatusPassed); err != nil {
		return fmt.Errorf("complete shard preparation workload: %w", err)
	}
	return nil
}

// buildContainerShardSet keeps the owner-side configured count bound to the durable set.
func (owner *coordinatorOwner) buildContainerShardSet(
	plan gatecontract.GatePlan,
	manifestDigest string,
	configDigest string,
) (gatecontract.ContainerShardSet, error) {
	set, err := gatecontract.BuildContainerShardSetWithCount(
		plan,
		manifestDigest,
		configDigest,
		uint8(owner.schedulingPolicy.ShardsPerJob),
	)
	if err != nil {
		return gatecontract.ContainerShardSet{}, err
	}
	if len(set.Shards) != owner.schedulingPolicy.ShardsPerJob {
		return gatecontract.ContainerShardSet{}, fmt.Errorf(
			"configured shards_per_job %d does not match gate shard set %d",
			owner.schedulingPolicy.ShardsPerJob, len(set.Shards),
		)
	}
	return set, nil
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

func planObservedContainerResult(
	container localci.FreshContainerResult,
	gateResult localci.FreshPlanGateResult,
) localci.FreshContainerResult {
	result := container
	result.Status = gateResult.Status
	result.GateResult = &gateResult.GateResult
	result.PlanGateResults = nil
	result.ExitCode = gateResult.GateResult.ExitCode
	result.StartedAt = gateResult.GateResult.StartedAt
	result.CompletedAt = gateResult.GateResult.CompletedAt
	result.LogOutput = append([]byte(nil), gateResult.LogOutput...)
	result.LogDigest = gateResult.GateResult.LogDigest
	result.Evidence = append(result.Evidence, gatecontract.Evidence{
		Kind: gatecontract.EvidenceKindLog, Digest: result.LogDigest,
	})
	return result
}

func (owner *coordinatorOwner) persistObservedGateLog(
	ctx context.Context,
	jobID string,
	gateID gatecontract.GateID,
	result localci.FreshContainerResult,
) error {
	if result.LogDigest == "" {
		return nil
	}
	cleanupCtx, cancel := coordinatorCleanupContext(ctx)
	defer cancel()
	if err := owner.store.recordGateLog(cleanupCtx, jobID, gateID, result.LogDigest, result.LogOutput); err != nil {
		return fmt.Errorf("persist gate %q log: %w", gateID, err)
	}
	return nil
}

// completeExecution 将签名失败收敛为 infra_failed 并持久化 terminal 状态。
func (owner *coordinatorOwner) completeExecution(
	ctx context.Context,
	record coordinatorJobRecord,
	execution receiptExecution,
	state jobState,
	executionErr error,
) error {
	cleanupCtx, cancel := coordinatorCleanupContext(ctx)
	defer cancel()
	durableRecord, err := owner.store.job(cleanupCtx, record.JobID)
	if err != nil {
		return fmt.Errorf("reload durable container lifecycle before scheduler completion: %w", err)
	}
	if err := requireExecutionCapacityRelease(durableRecord, execution); err != nil {
		return err
	}
	record = durableRecord
	message := ""
	if executionErr != nil {
		message = executionErr.Error()
	}
	var receipt *gatecontract.ResultReceipt
	if state == jobStatePassed {
		signed, err := buildPassedResultReceipt(record, execution, owner.dependencies.ReceiptSigner)
		if err != nil {
			state = jobStateInfraFailed
			message = "sign canonical result receipt: " + err.Error()
		} else {
			receipt = &signed
		}
	}
	if err := owner.store.finishJob(cleanupCtx, record.JobID, state, execution.Results, message, receipt); err != nil {
		if state == jobStatePassed {
			fallbackErr := owner.store.finishJob(
				cleanupCtx, record.JobID, jobStateInfraFailed, execution.Results,
				"persist canonical result receipt: "+err.Error(), nil,
			)
			return errors.Join(err, fallbackErr)
		}
		return err
	}
	if err := owner.schedulerClient.Complete(cleanupCtx, record.JobID, schedulerTerminalState(state)); err != nil {
		return fmt.Errorf("complete scheduler workload %q: %w", record.JobID, err)
	}
	return nil
}

// requireExecutionCapacityRelease 只有在 Docker 容器删除及其持久证明一致时才允许释放 scheduler 容量租约。
func requireExecutionCapacityRelease(record coordinatorJobRecord, execution receiptExecution) error {
	if execution.ContainerObserved {
		if !execution.ContainerRemovalProven || execution.ContainerRemovalProofDigest == "" {
			return errors.New("cannot release scheduler capacity without container removal proof")
		}
		if record.ContainerPhase != localci.FreshContainerPhaseRemoved || record.RemovalProofDigest == "" {
			return errors.New("cannot release scheduler capacity without durable container removal proof")
		}
		if record.RemovalProofDigest != execution.ContainerRemovalProofDigest {
			return errors.New("durable container removal proof drifted from execution result")
		}
		return nil
	}
	if execution.ContainerRemovalProven || execution.ContainerRemovalProofDigest != "" {
		return errors.New("container removal proof requires an observed container")
	}
	switch record.ContainerPhase {
	case "", localci.FreshContainerPhasePrepared:
		return nil
	default:
		return errors.New("cannot release scheduler capacity after Docker create was attempted without removal proof")
	}
}

func coordinatorCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return localci.BoundedCleanupContext(parent, coordinatorCleanupTimeout)
}

func (owner *coordinatorOwner) shardCleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	return localci.BoundedCleanupContext(parent, owner.shardCleanupTimeout)
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

// joinOwnerErrors 保留首个真实 owner 故障，并过滤其引发的纯取消清理噪声。
func joinOwnerErrors(parent context.Context, values ...error) error {
	hasPrimary := false
	for _, err := range values {
		if err != nil && !isOwnerShutdownNoise(err) {
			hasPrimary = true
		}
	}
	filtered := make([]error, 0, len(values))
	for _, err := range values {
		if err == nil || ((parent.Err() != nil || hasPrimary) && isOwnerShutdownNoise(err)) {
			continue
		}
		filtered = append(filtered, err)
	}
	return errors.Join(filtered...)
}

// isOwnerShutdownNoise 只接受取消和关闭阶段可预期的 scheduler sentinel 包装链。
func isOwnerShutdownNoise(err error) bool {
	if err == context.Canceled || err == localci.ErrSchedulerClosed {
		return true
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		children := joined.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !isOwnerShutdownNoise(child) {
				return false
			}
		}
		return true
	}
	if wrapped, ok := err.(interface{ Unwrap() error }); ok {
		return isOwnerShutdownNoise(wrapped.Unwrap())
	}
	return false
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

// runRecoveredShards 并发接管全部容器，并在首个执行失败时先建立 cancelling barrier。
func (owner *coordinatorOwner) runRecoveredShards(
	ctx context.Context,
	admission coordinatorShardAdmission,
	probes []recoveredShardProbe,
) ([]localci.FreshContainerResult, []error, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]localci.FreshContainerResult, len(probes))
	errs := make([]error, len(probes))
	var barrierOnce sync.Once
	var barrierErr error
	group := errgroup.Group{}
	for index := range probes {
		group.Go(func() error {
			results[index], errs[index] = owner.dependencies.RecoveryRunner.RecoverFreshContainer(runCtx, probes[index].request)
			if errs[index] != nil || results[index].Status != gatecontract.ResultStatusPassed {
				barrierOnce.Do(func() {
					barrierErr = owner.reportRecoveredShardFailure(ctx, admission)
					if barrierErr == nil {
						cancel()
					}
				})
			}
			return nil
		})
	}
	_ = group.Wait()
	return results, errs, barrierErr
}

// finishObservedRecoveredShards 持久化逐片证据并生成组级终态。
func (owner *coordinatorOwner) finishObservedRecoveredShards(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	image ensuredImage,
	probes []recoveredShardProbe,
	results []localci.FreshContainerResult,
	errs []error,
) error {
	receipts, recoveryErr := owner.collectRecoveredShardEvidence(ctx, record.JobID, probes, results, errs)
	if recoveryErr != nil && !recoveredShardResultsFailed(results) {
		if err := owner.reportRecoveredShardFailure(ctx, admission); err != nil {
			return errors.Join(recoveryErr, err)
		}
	}
	if recoveryErr != nil || recoveredShardResultsFailed(results) {
		return owner.finishFailedRecoveredShardGroup(ctx, record, admission, receipts,
			errors.Join(recoveryErr, errors.New("recovered shard execution failed")))
	}
	set, err := recoveredShardSet(record, probes)
	if err != nil {
		return owner.failRecoveredShardGroup(ctx, record, admission, err)
	}
	aggregate, err := gatecontract.AggregateContainerShards(set, receipts)
	if err != nil {
		return owner.failRecoveredShardGroup(ctx, record, admission, err)
	}
	execution, err := shardReceiptExecution(image.AcceptedRecord, set, receipts, aggregate)
	if err != nil {
		return owner.failRecoveredShardGroup(ctx, record, admission, err)
	}
	execution.StartedAt, execution.Deadline = record.StartedAt.UTC(), record.Deadline.UTC()
	return owner.finishRecoveredShardGroup(ctx, record, admission, execution, jobStatePassed, nil)
}

// collectRecoveredShardEvidence 按 canonical index 持久化逐片日志并汇总执行错误。
func (owner *coordinatorOwner) collectRecoveredShardEvidence(
	ctx context.Context,
	jobID string,
	probes []recoveredShardProbe,
	results []localci.FreshContainerResult,
	errs []error,
) ([]gatecontract.ContainerShardReceipt, error) {
	durableShards, err := owner.store.containerShards(ctx, jobID)
	if err != nil {
		return nil, err
	}
	durableByIdentity := make(map[string]coordinatorShardRecord, len(durableShards))
	for _, shard := range durableShards {
		durableByIdentity[shard.Shard.IdentityDigest] = shard
	}
	receipts := make([]gatecontract.ContainerShardReceipt, len(probes))
	var recoveryErr error
	for index, result := range results {
		shard, exists := durableByIdentity[probes[index].shard.Shard.IdentityDigest]
		if !exists {
			recoveryErr = errors.Join(recoveryErr, errors.New("recovered shard durable identity is missing"))
			continue
		}
		receipt, err := recoveredShardReceipt(shard, result)
		if err != nil {
			recoveryErr = errors.Join(recoveryErr, err)
		} else {
			receipts[index] = receipt
		}
		if len(result.PlanGateResults) != 0 {
			recoveryErr = errors.Join(recoveryErr, owner.persistShardGateLogs(ctx, jobID, probes[index].shard.Shard, result))
		}
		recoveryErr = errors.Join(recoveryErr, errs[index])
	}
	return receipts, recoveryErr
}

// recoveredShardReceipt 将恢复输出绑定到持久化生命周期已接受的退出时刻。
func recoveredShardReceipt(shard coordinatorShardRecord, result localci.FreshContainerResult) (gatecontract.ContainerShardReceipt, error) {
	if shard.ExitedAt == nil {
		return gatecontract.ContainerShardReceipt{}, errors.New("recovered shard durable exit timestamp is missing")
	}
	if !result.ExitedAt.IsZero() && !result.ExitedAt.Equal(*shard.ExitedAt) {
		return gatecontract.ContainerShardReceipt{}, errors.New("recovered shard exit timestamp drifted from durable lifecycle")
	}
	receipt := shardReceipt(shard.Shard, result)
	receipt.ExitedAt = shard.ExitedAt.UTC()
	return receipt, nil
}

// recoveredShardResultsFailed 判断任一恢复分片是否未成功完成。
func recoveredShardResultsFailed(results []localci.FreshContainerResult) bool {
	for _, result := range results {
		if result.Status != gatecontract.ResultStatusPassed {
			return true
		}
	}
	return false
}

// recoveredShardSet 用持久 job 与恢复探针重建 canonical 分片集合。
func recoveredShardSet(record coordinatorJobRecord, probes []recoveredShardProbe) (gatecontract.ContainerShardSet, error) {
	set := gatecontract.ContainerShardSet{Profile: record.Profile, PlanDigest: record.Plan.PlanDigest, SourceTreeSHA: record.JobSourceTreeSHA,
		AcceptedManifestDigest: probes[0].shard.Shard.AcceptedManifestDigest, AcceptedConfigDigest: probes[0].shard.Shard.AcceptedConfigDigest,
		ShardsPerJob: probes[0].shard.Shard.ShardsPerJob, Shards: make([]gatecontract.ContainerShard, len(probes))}
	for index := range probes {
		set.Shards[index] = probes[index].shard.Shard
	}
	return set, set.Validate()
}

// reportRecoveredShardFailure 建立恢复失败屏障，并接受已进入取消态的幂等结果。
func (owner *coordinatorOwner) reportRecoveredShardFailure(ctx context.Context, admission coordinatorShardAdmission) error {
	identity, err := shardFailureBarrierIdentity(admission)
	if err != nil {
		return err
	}
	if _, err := owner.schedulerClient.ReportShardFailure(ctx, admission.WorkloadID, admission.GroupIdentity, identity); err == nil {
		return nil
	} else if status, stateErr := owner.schedulerClient.State(ctx, admission.WorkloadID); stateErr == nil && status == localci.WorkloadStatusCancelling {
		return nil
	} else {
		return errors.Join(err, stateErr)
	}
}

// failRecoveredShardGroup 先建立 scheduler barrier，再清理全部可能已创建的 sibling。
func (owner *coordinatorOwner) failRecoveredShardGroup(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	cause error,
) error {
	if err := owner.reportRecoveredShardFailure(ctx, admission); err != nil {
		return errors.Join(cause, err)
	}
	if err := owner.cleanupRecoveredShardGroup(ctx, record); err != nil {
		return errors.Join(cause, err)
	}
	return owner.finishRecoveredShardGroup(ctx, record, admission, receiptExecution{}, jobStateInfraFailed, cause)
}

// finishFailedRecoveredShardGroup 清理失败分片并依据收集到的回执确定终态。
func (owner *coordinatorOwner) finishFailedRecoveredShardGroup(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	receipts []gatecontract.ContainerShardReceipt,
	cause error,
) error {
	if err := owner.cleanupRecoveredShardGroup(ctx, record); err != nil {
		return errors.Join(cause, err)
	}
	state := stateForShardRunFailure(context.Background(), receipts)
	return owner.finishRecoveredShardGroup(ctx, record, admission,
		receiptExecution{ShardReceipts: receipts}, state, cause)
}

// finishRecoveredShardGroup 只在 removal proof 齐备后持久化 job 并释放 scheduler group。
func (owner *coordinatorOwner) finishRecoveredShardGroup(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	execution receiptExecution,
	state jobState,
	cause error,
) error {
	var passedReceipt *gatecontract.ResultReceipt
	if state == jobStatePassed {
		receipt, err := buildPassedResultReceipt(record, execution, owner.dependencies.ReceiptSigner)
		if err != nil {
			return owner.failRecoveredShardGroup(ctx, record, admission, fmt.Errorf("build recovered shard receipt: %w", err))
		}
		passedReceipt = &receipt
	}
	if err := owner.requireRecoveredShardRemovalProofs(ctx, record.JobID); err != nil {
		return err
	}
	if err := cleanupRecoveredShardSource(record); err != nil {
		return err
	}
	if state == jobStatePassed {
		if err := owner.store.finishJob(ctx, record.JobID, state, execution.Results, "", passedReceipt); err != nil {
			return err
		}
	} else {
		if cause == nil {
			cause = errors.New("recovered shard group failed")
		}
		if err := owner.store.finishJob(ctx, record.JobID, state, execution.Results, cause.Error(), nil); err != nil {
			return err
		}
	}
	return owner.completeRecoveredShardSchedulerGroup(ctx, admission, schedulerTerminalState(state))
}

// observeRecoveredJob 接续已证明容器的恢复观察，并要求删除证明和 durable 状态一致后才完成任务。
func (owner *coordinatorOwner) observeRecoveredJob(ctx context.Context, record coordinatorJobRecord) error {
	request, err := owner.recoveryRequest(record)
	if err != nil {
		return owner.completeExecution(ctx, record, receiptExecution{}, jobStateInfraFailed, err)
	}
	result, recoveryErr := owner.dependencies.RecoveryRunner.RecoverFreshContainer(ctx, request)
	state := jobStateInfraFailed
	if result.Status == gatecontract.ResultStatusTimeout {
		state = jobStateTimeout
	}
	if recoveryErr == nil {
		recoveryErr = errors.New("recovered execution has no durable receipt and cannot become passed")
	}
	if !result.Container.Removed || result.RemovalProofDigest == "" {
		return errors.Join(recoveryErr, errors.New("recovered container has no removal proof"))
	}
	persisted, result, persistErr := owner.recoveredLegacyDurableResult(ctx, record.JobID, result)
	if persistErr != nil {
		return errors.Join(recoveryErr, persistErr)
	}
	record = persisted
	if cleanupErr := cleanupDeterministicRecoverySource(record); cleanupErr != nil {
		return errors.Join(recoveryErr, cleanupErr)
	}
	execution := receiptExecution{
		ContainerObserved: true, ContainerRemovalProven: true, ContainerRemovalProofDigest: result.RemovalProofDigest,
	}
	return owner.completeExecution(ctx, record, execution, state, recoveryErr)
}

// recoveredLegacyDurableResult 读取恢复容器的持久记录并校验退出与删除证据。
func (owner *coordinatorOwner) recoveredLegacyDurableResult(
	ctx context.Context,
	jobID string,
	result localci.FreshContainerResult,
) (coordinatorJobRecord, localci.FreshContainerResult, error) {
	persisted, err := owner.store.job(ctx, jobID)
	if err != nil {
		return coordinatorJobRecord{}, localci.FreshContainerResult{}, err
	}
	result, err = recoveredLegacyResultWithDurableExit(persisted, result)
	if err != nil {
		return coordinatorJobRecord{}, localci.FreshContainerResult{}, err
	}
	if persisted.ContainerPhase != localci.FreshContainerPhaseRemoved || persisted.RemovalProofDigest == "" {
		return coordinatorJobRecord{}, localci.FreshContainerResult{}, errors.New("recovered container has no durable removal proof")
	}
	return persisted, result, nil
}

// recoveredLegacyResultWithDurableExit 拒绝与持久化 Docker 退出观测不一致的恢复结果。
func recoveredLegacyResultWithDurableExit(
	record coordinatorJobRecord,
	result localci.FreshContainerResult,
) (localci.FreshContainerResult, error) {
	if record.ContainerExitedAt == nil {
		if normalResultRequiresContainerExit(result.Status) {
			return localci.FreshContainerResult{}, errors.New("recovered normal container result lacks durable exited_at")
		}
		return result, nil
	}
	if !result.ExitedAt.IsZero() && !result.ExitedAt.Equal(*record.ContainerExitedAt) {
		return localci.FreshContainerResult{}, errors.New("recovered container exit timestamp drifted from durable lifecycle")
	}
	result.ExitedAt = record.ContainerExitedAt.UTC()
	return result, nil
}

// normalResultRequiresContainerExit 标记必须有 Docker 退出观测的正常执行终态。
func normalResultRequiresContainerExit(status gatecontract.ResultStatus) bool {
	return status == gatecontract.ResultStatusPassed || status == gatecontract.ResultStatusFailed ||
		status == gatecontract.ResultStatusCancelled || status == gatecontract.ResultStatusTimeout
}
