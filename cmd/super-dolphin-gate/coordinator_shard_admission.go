package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const coordinatorShardAdmissionSchema = `
CREATE TABLE IF NOT EXISTS coordinator_shard_admissions (
 job_id TEXT PRIMARY KEY,
 workload_id TEXT NOT NULL UNIQUE,
 group_identity TEXT NOT NULL UNIQUE,
 shard_identities_json BLOB NOT NULL,
 phase TEXT NOT NULL
);`

const (
	shardAdmissionOutbox   = "outbox"
	shardAdmissionEnqueued = "enqueued"
)

var errShardDeadlineAlreadyClaimed = errors.New("durable shard deadline was claimed concurrently")

type coordinatorShardAdmission struct {
	JobID, WorkloadID, GroupIdentity string
	ShardIdentities                  []string
	Phase                            string
}

func ensureCoordinatorShardAdmissionSchema(ctx context.Context, db coordinatorExecer) error {
	if _, err := db.ExecContext(ctx, coordinatorShardAdmissionSchema); err != nil {
		return fmt.Errorf("initialize coordinator shard admission schema: %w", err)
	}
	return nil
}

// recoveryShardNeverStarted 判断分片尚无可恢复的 Docker 生命周期证据。
func recoveryShardNeverStarted(shard coordinatorShardRecord) bool {
	return shard.ContainerPhase == "" || shard.ContainerPhase == localci.FreshContainerPhasePrepared
}

// recoveryShardCleanupRequest 用 durable shard 身份构造恢复期清理请求。
func (owner *coordinatorOwner) recoveryShardCleanupRequest(
	record coordinatorJobRecord,
	shard coordinatorShardRecord,
) (*localci.FreshContainerCleanupRequest, error) {
	if recoveryShardNeverStarted(shard) || shard.ContainerPhase == localci.FreshContainerPhaseRemoved {
		return nil, nil
	}
	request, err := owner.recoveryShardRequest(record, shard)
	if err != nil {
		return nil, err
	}
	return &localci.FreshContainerCleanupRequest{
		ContainerID: request.ContainerID, ContainerLabels: request.ContainerLabels,
		ImageReference: request.ImageReference, ConfigDigest: request.ConfigDigest,
		SourceSnapshotDir: request.SourceSnapshotDir, Command: request.Command,
		Profile: request.Profile, GateID: request.GateID,
		RemovalPending: shard.ContainerPhase == localci.FreshContainerPhaseRemovalPending,
		LifecycleHook:  request.LifecycleHook,
	}, nil
}

type coordinatorExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// shardAdmissionForSet 将完整三 shard 集合绑定为唯一的调度 group 请求。
func shardAdmissionForSet(record coordinatorJobRecord, set gatecontract.ContainerShardSet) (coordinatorShardAdmission, localci.WorkloadRequest, error) {
	if record.JobID == "" || record.InvocationID == "" || record.EnqueueSequence == 0 || set.Validate() != nil {
		return coordinatorShardAdmission{}, localci.WorkloadRequest{}, errors.New("validated job and container shard set are required")
	}
	identities := make([]string, len(set.Shards))
	for index, shard := range set.Shards {
		identities[index] = shard.IdentityDigest
	}
	admission := coordinatorShardAdmission{JobID: record.JobID, WorkloadID: record.JobID + "/shards", GroupIdentity: record.JobID + "/shard-group", ShardIdentities: identities, Phase: shardAdmissionOutbox}
	request := localci.WorkloadRequest{ID: admission.WorkloadID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Subsequence: record.SchedulerSubsequence, Kind: localci.WorkloadKindShard,
		GroupIdentity: admission.GroupIdentity, GroupSize: len(identities), ServiceCount: len(identities) - 1, ShardIdentities: append([]string(nil), identities...)}
	return admission, request, nil
}

// prepareShardAdmission 在同一事务持久化 shard 集合与可重放的 admission outbox。
func (store *coordinatorStore) prepareShardAdmission(ctx context.Context, record coordinatorJobRecord, set gatecontract.ContainerShardSet) (coordinatorShardAdmission, error) {
	admission, _, err := shardAdmissionForSet(record, set)
	if err != nil {
		return coordinatorShardAdmission{}, err
	}
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return coordinatorShardAdmission{}, err
	}
	defer tx.Rollback()
	if err := persistContainerShardSetTx(ctx, tx, record.JobID, set); err != nil {
		return coordinatorShardAdmission{}, err
	}
	encoded, err := json.Marshal(admission.ShardIdentities)
	if err != nil {
		return coordinatorShardAdmission{}, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO coordinator_shard_admissions
(job_id, workload_id, group_identity, shard_identities_json, phase) VALUES (?, ?, ?, ?, ?)
ON CONFLICT(job_id) DO NOTHING`, admission.JobID, admission.WorkloadID, admission.GroupIdentity, encoded, admission.Phase)
	if err != nil {
		return coordinatorShardAdmission{}, fmt.Errorf("persist shard admission outbox: %w", err)
	}
	stored, err := shardAdmissionFromQuery(ctx, tx, record.JobID)
	if err != nil || !reflect.DeepEqual(stored, admission) {
		return coordinatorShardAdmission{}, errors.New("shard admission replay drifted")
	}
	if err := tx.Commit(); err != nil {
		return coordinatorShardAdmission{}, err
	}
	return stored, nil
}

type coordinatorShardAdmissionQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func shardAdmissionFromQuery(ctx context.Context, queryer coordinatorShardAdmissionQueryer, jobID string) (coordinatorShardAdmission, error) {
	var result coordinatorShardAdmission
	var encoded []byte
	err := queryer.QueryRowContext(ctx, `SELECT job_id, workload_id, group_identity, shard_identities_json, phase
FROM coordinator_shard_admissions WHERE job_id = ?`, jobID).Scan(&result.JobID, &result.WorkloadID, &result.GroupIdentity, &encoded, &result.Phase)
	if err != nil {
		return result, err
	}
	if err := json.Unmarshal(encoded, &result.ShardIdentities); err != nil || result.Phase == "" || len(result.ShardIdentities) == 0 {
		return coordinatorShardAdmission{}, errors.New("invalid durable shard admission")
	}
	return result, nil
}

func (store *coordinatorStore) shardAdmission(ctx context.Context, jobID string) (coordinatorShardAdmission, error) {
	return shardAdmissionFromQuery(ctx, store.db, jobID)
}

func (store *coordinatorStore) markShardAdmissionEnqueued(ctx context.Context, jobID string) error {
	result, err := store.db.ExecContext(ctx, `UPDATE coordinator_shard_admissions SET phase = ? WHERE job_id = ? AND phase = ?`, shardAdmissionEnqueued, jobID, shardAdmissionOutbox)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil || changed > 1 {
		return errors.New("mark shard admission enqueued")
	}
	return nil
}

// enqueueShardAdmission 重放 durable outbox，并在释放 prep lease 前验证完整 group 请求。
func (owner *coordinatorOwner) enqueueShardAdmission(ctx context.Context, record coordinatorJobRecord, admission coordinatorShardAdmission) error {
	request, err := owner.shardAdmissionRequest(ctx, record, admission)
	if err != nil {
		return err
	}
	snapshot, err := owner.schedulerClient.Snapshot(ctx)
	if err != nil {
		return err
	}
	enqueued, err := matchingShardAdmissionInSnapshot(snapshot, request)
	if err != nil {
		return err
	}
	if !enqueued {
		if err := owner.schedulerClient.Enqueue(ctx, request); err != nil {
			return err
		}
	}
	return owner.store.markShardAdmissionEnqueued(ctx, record.JobID)
}

// shardAdmissionRequest 从 durable shard 集合重建并核验 admission 请求。
func (owner *coordinatorOwner) shardAdmissionRequest(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
) (localci.WorkloadRequest, error) {
	shards, err := owner.store.containerShards(ctx, record.JobID)
	if err != nil {
		return localci.WorkloadRequest{}, err
	}
	set := gatecontract.ContainerShardSet{Profile: record.Profile, PlanDigest: record.Plan.PlanDigest, SourceTreeSHA: record.JobSourceTreeSHA, Shards: make([]gatecontract.ContainerShard, len(shards))}
	for index, shard := range shards {
		set.Shards[index] = shard.Shard
	}
	set.AcceptedManifestDigest, set.AcceptedConfigDigest = set.Shards[0].AcceptedManifestDigest, set.Shards[0].AcceptedConfigDigest
	_, request, err := shardAdmissionForSet(record, set)
	if err != nil || request.ID != admission.WorkloadID || request.GroupIdentity != admission.GroupIdentity {
		return localci.WorkloadRequest{}, errors.New("durable shard admission binding drifted")
	}
	return request, nil
}

// matchingShardAdmissionInSnapshot 要求同 ID workload 的完整请求字节级等价。
func matchingShardAdmissionInSnapshot(snapshot localci.SchedulerSnapshot, request localci.WorkloadRequest) (bool, error) {
	for _, workload := range snapshot.Workloads {
		if workload.Request.ID == request.ID {
			if !reflect.DeepEqual(workload.Request, request) {
				return false, errors.New("scheduler shard workload drifted")
			}
			return true, nil
		}
	}
	return false, nil
}

func coordinatorSchedulerObservationError(record coordinatorJobRecord, workloadID string, err error) error {
	if len(record.ContainerShards) != 0 && !record.State.terminal() {
		return fmt.Errorf(
			"%w: durable shard admission %q is waiting for scheduler outbox replay: %v",
			errCoordinatorTransition,
			workloadID,
			err,
		)
	}
	return err
}

func coordinatorStatusForSchedulerObservation(
	record coordinatorJobRecord,
	workloadID string,
	schedulerState localci.WorkloadStatus,
	queuePosition int,
) (jobStatus, error) {
	if status, handled, err := shardCoordinatorStatus(record, workloadID, schedulerState, queuePosition); handled || err != nil {
		return status, err
	}
	if err := reconcileCoordinatorState(record.State, schedulerState); err != nil {
		return jobStatus{}, err
	}
	return queuedCoordinatorStatus(record, queuePosition)
}

// shardCoordinatorStatus 处理 shard admission 与 scheduler 持久化之间的过渡状态。
func shardCoordinatorStatus(
	record coordinatorJobRecord,
	workloadID string,
	schedulerState localci.WorkloadStatus,
	queuePosition int,
) (jobStatus, bool, error) {
	if len(record.ContainerShards) == 0 {
		return jobStatus{}, false, nil
	}
	if record.State == jobStateStarted && schedulerState == localci.WorkloadStatusQueued {
		if queuePosition <= 0 {
			return jobStatus{}, true, fmt.Errorf("%w: queued shard group %q has no positive queue position", errCoordinatorState, workloadID)
		}
		status := record.status()
		status.State, status.QueuePosition = jobStateQueued, queuePosition
		return status, true, nil
	}
	if schedulerState == localci.WorkloadStatusCancelling {
		return jobStatus{}, true, fmt.Errorf(
			"%w: durable shard job %q and scheduler %q are crossing a persisted state boundary",
			errCoordinatorTransition,
			record.State,
			schedulerState,
		)
	}
	return jobStatus{}, false, nil
}

func queuedCoordinatorStatus(record coordinatorJobRecord, queuePosition int) (jobStatus, error) {
	status := record.status()
	if record.State != jobStateQueued {
		return status, nil
	}
	if queuePosition <= 0 {
		return jobStatus{}, fmt.Errorf("%w: queued job %q has no positive scheduler queue position", errCoordinatorState, record.JobID)
	}
	status.QueuePosition = queuePosition
	return status, nil
}

type shardGroupExecutionInput struct {
	record    coordinatorJobRecord
	admission coordinatorShardAdmission
	set       gatecontract.ContainerShardSet
}

// executeShardGroup runs only the durable canonical shard set reserved by the scheduler gang.
func (owner *coordinatorOwner) executeShardGroup(ctx context.Context, reservation localci.WorkloadReservation) error {
	input, err := owner.loadShardGroupExecution(ctx, reservation)
	if err != nil {
		return err
	}
	return owner.executeLoadedShardGroup(ctx, input)
}

// loadShardGroupExecution 只接受与 durable admission 完全匹配的 scheduler reservation。
func (owner *coordinatorOwner) loadShardGroupExecution(
	ctx context.Context,
	reservation localci.WorkloadReservation,
) (shardGroupExecutionInput, error) {
	if !strings.HasSuffix(reservation.WorkloadID, "/shards") {
		return shardGroupExecutionInput{}, errors.New("shard reservation workload ID is invalid")
	}
	record, err := owner.store.job(ctx, strings.TrimSuffix(reservation.WorkloadID, "/shards"))
	if err != nil {
		return shardGroupExecutionInput{}, err
	}
	admission, err := owner.store.shardAdmission(ctx, record.JobID)
	if err != nil || admission.WorkloadID != reservation.WorkloadID || admission.GroupIdentity != reservation.GroupIdentity {
		return shardGroupExecutionInput{}, errors.New("shard reservation does not match durable admission")
	}
	shards, err := owner.store.containerShards(ctx, record.JobID)
	if err != nil {
		return shardGroupExecutionInput{}, err
	}
	set, err := containerShardSetForRecord(record, shards)
	if err != nil {
		return shardGroupExecutionInput{}, err
	}
	if !reflect.DeepEqual(admission.ShardIdentities, reservationShardIdentities(reservation)) {
		return shardGroupExecutionInput{}, errors.New("reserved shard identities drifted")
	}
	return shardGroupExecutionInput{record: record, admission: admission, set: set}, nil
}

func (owner *coordinatorOwner) executeLoadedShardGroup(ctx context.Context, input shardGroupExecutionInput) error {
	image, err := owner.ensureShardGroupImage(ctx, input.record, input.set)
	if err != nil {
		return owner.completeShardGroup(ctx, input.record, input.admission, receiptExecution{}, jobStateInfraFailed, err)
	}
	source, err := owner.materializeShardGroupSource(ctx, input.record)
	if err != nil {
		return owner.completeShardGroup(ctx, input.record, input.admission, receiptExecution{}, jobStateInfraFailed, err)
	}
	return owner.runShardGroup(ctx, input, image, source)
}

func (owner *coordinatorOwner) ensureShardGroupImage(
	ctx context.Context,
	record coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
) (ensuredImage, error) {
	imageCtx, cancelImage := localci.BoundedOperationContext(ctx, coordinatorProvisioningTimeout)
	image, err := owner.ensureJobImage(imageCtx, record)
	cancelImage()
	if err != nil {
		return ensuredImage{}, err
	}
	if image.Identity.PlatformManifestDigest != set.AcceptedManifestDigest || image.Identity.ConfigDigest != set.AcceptedConfigDigest {
		return ensuredImage{}, errors.New("accepted image drifted after shard admission")
	}
	return image, nil
}

func (owner *coordinatorOwner) materializeShardGroupSource(ctx context.Context, record coordinatorJobRecord) (materializedJobSource, error) {
	sourceCtx, cancelSource := localci.BoundedOperationContext(ctx, coordinatorSourceSetupTimeout)
	source, err := owner.materializeJobSource(sourceCtx, record)
	cancelSource()
	if err != nil {
		return materializedJobSource{}, err
	}
	return source, nil
}

func (owner *coordinatorOwner) runShardGroup(
	ctx context.Context,
	input shardGroupExecutionInput,
	image ensuredImage,
	source materializedJobSource,
) error {
	receipts, runErr := gatecontract.RunContainerShards(ctx, input.set, func(runCtx context.Context, shard gatecontract.ContainerShard) (gatecontract.ContainerShardReceipt, error) {
		return owner.runShard(input.record, image, source, runCtx, shard)
	})
	cleanupCtx, cleanupCancel := owner.shardCleanupContext(ctx)
	defer cleanupCancel()
	partial, partialErr := gatecontract.AggregateContainerShardFailureEvidence(input.set, receipts)
	execution, executionErr := shardReceiptExecution(image.AcceptedRecord, input.set, receipts, partial)
	executionErr = errors.Join(partialErr, executionErr)
	return owner.completeShardRun(cleanupCtx, ctx, input, image, source, receipts, runErr, execution, executionErr)
}

func (owner *coordinatorOwner) runShard(
	record coordinatorJobRecord,
	image ensuredImage,
	source materializedJobSource,
	runCtx context.Context,
	shard gatecontract.ContainerShard,
) (gatecontract.ContainerShardReceipt, error) {
	labels := owner.shardContainerLabels(record, shard)
	result, err := owner.dependencies.FreshRunner.RunFreshContainer(runCtx, freshContainerRequest{
		Image: image.Identity, ImageTruth: image.Truth, ImageProvenanceSourceTreeSHA: image.ImageProvenanceSourceTreeSHA,
		JobSourceTreeSHA: record.JobSourceTreeSHA, SourceSnapshotDir: source.SnapshotDir, Profile: record.Profile, Plan: record.Plan,
		GateID: shard.GateIDs[0], ShardGateIDs: append([]gatecontract.GateID(nil), shard.GateIDs...), ShardIdentity: shard.IdentityDigest,
		ContainerLabels: labels,
		ClaimDeadline: func(claimCtx context.Context, started time.Time) (time.Time, error) {
			return owner.store.claimShardExecutionDeadline(claimCtx, record.JobID, started)
		},
		LifecycleHook: owner.shardLifecycleHook(record.JobID, shard, labels),
	})
	receipt := shardReceipt(shard, result)
	if err != nil && len(result.PlanGateResults) == 0 {
		return receipt, err
	}
	cleanupCtx, cleanupCancel := owner.shardCleanupContext(runCtx)
	defer cleanupCancel()
	if logErr := owner.persistShardGateLogs(cleanupCtx, record.JobID, shard, result); logErr != nil {
		return receipt, logErr
	}
	return receipt, err
}

// completeShardRun 在 source cleanup 后汇总证据，并只以 durable 时钟签发 passed 状态。
func (owner *coordinatorOwner) completeShardRun(
	cleanupCtx context.Context,
	runCtx context.Context,
	input shardGroupExecutionInput,
	image ensuredImage,
	source materializedJobSource,
	receipts []gatecontract.ContainerShardReceipt,
	runErr error,
	execution receiptExecution,
	executionErr error,
) error {
	cleanupErr := source.Cleanup()
	if cleanupErr != nil {
		return owner.completeShardGroup(cleanupCtx, input.record, input.admission, execution, jobStateInfraFailed,
			errors.Join(runErr, executionErr, fmt.Errorf("cleanup source snapshot: %w", cleanupErr)))
	}
	if executionErr != nil {
		return owner.completeShardGroup(cleanupCtx, input.record, input.admission, execution, jobStateInfraFailed, errors.Join(runErr, executionErr))
	}
	if runErr != nil {
		return owner.completeShardGroup(cleanupCtx, input.record, input.admission, execution, stateForShardRunFailure(runCtx, receipts), runErr)
	}
	aggregated, err := gatecontract.AggregateContainerShards(input.set, receipts)
	if err != nil {
		return owner.completeShardGroup(cleanupCtx, input.record, input.admission, execution, jobStateInfraFailed, err)
	}
	execution, err = shardReceiptExecution(image.AcceptedRecord, input.set, receipts, aggregated)
	if err != nil {
		return owner.completeShardGroup(cleanupCtx, input.record, input.admission, execution, jobStateInfraFailed, err)
	}
	durableRecord, err := owner.store.job(cleanupCtx, input.record.JobID)
	if err != nil || durableRecord.StartedAt == nil || durableRecord.Deadline == nil {
		return owner.completeShardGroup(cleanupCtx, input.record, input.admission, execution, jobStateInfraFailed,
			errors.Join(err, errors.New("durable shard execution clock is incomplete")))
	}
	execution.StartedAt, execution.Deadline = durableRecord.StartedAt.UTC(), durableRecord.Deadline.UTC()
	return owner.completeShardGroup(cleanupCtx, input.record, input.admission, execution, jobStatePassed, nil)
}

func stateForShardRunFailure(ctx context.Context, receipts []gatecontract.ContainerShardReceipt) jobState {
	for _, state := range []jobState{jobStateTimeout, jobStateInfraFailed, jobStateFailed, jobStateCancelled} {
		if shardReceiptsContainState(receipts, state) {
			return state
		}
	}
	return stateForShardContextError(ctx.Err())
}

func shardReceiptsContainState(receipts []gatecontract.ContainerShardReceipt, want jobState) bool {
	for _, receipt := range receipts {
		if shardReceiptFailureState(receipt.Status) == want {
			return true
		}
	}
	return false
}

func shardReceiptFailureState(status gatecontract.ResultStatus) jobState {
	switch status {
	case gatecontract.ResultStatusTimeout:
		return jobStateTimeout
	case gatecontract.ResultStatusInfraFailed:
		return jobStateInfraFailed
	case gatecontract.ResultStatusFailed:
		return jobStateFailed
	case gatecontract.ResultStatusCancelled:
		return jobStateCancelled
	default:
		return ""
	}
}

func stateForShardContextError(err error) jobState {
	if errors.Is(err, context.DeadlineExceeded) {
		return jobStateTimeout
	}
	if errors.Is(err, context.Canceled) {
		return jobStateCancelled
	}
	return jobStateFailed
}

// persistShardGateLogs keeps each shard's exact canonical gate evidence durable before aggregation.
func (owner *coordinatorOwner) persistShardGateLogs(
	ctx context.Context,
	jobID string,
	shard gatecontract.ContainerShard,
	result localci.FreshContainerResult,
) error {
	if len(result.PlanGateResults) != len(shard.GateIDs) {
		return fmt.Errorf("shard report gate coverage does not match durable shard identity: got %d want %d", len(result.PlanGateResults), len(shard.GateIDs))
	}
	for index, planResult := range result.PlanGateResults {
		gateID := gatecontract.GateID(planResult.GateResult.GateID)
		if gateID != shard.GateIDs[index] {
			return errors.New("shard report gate identity drifted")
		}
		if err := owner.persistObservedGateLog(ctx, jobID, gateID, planObservedContainerResult(result, planResult)); err != nil {
			return fmt.Errorf("persist shard %s gate %s log: %w", shard.IdentityDigest, gateID, err)
		}
	}
	return nil
}

func containerShardSetForRecord(record coordinatorJobRecord, records []coordinatorShardRecord) (gatecontract.ContainerShardSet, error) {
	if len(records) == 0 {
		return gatecontract.ContainerShardSet{}, errors.New("durable shard set is missing")
	}
	set := gatecontract.ContainerShardSet{Profile: record.Profile, PlanDigest: record.Plan.PlanDigest, SourceTreeSHA: record.JobSourceTreeSHA, Shards: make([]gatecontract.ContainerShard, len(records))}
	set.AcceptedManifestDigest, set.AcceptedConfigDigest = records[0].Shard.AcceptedManifestDigest, records[0].Shard.AcceptedConfigDigest
	for index, record := range records {
		set.Shards[index] = record.Shard
	}
	return set, set.Validate()
}

func reservationShardIdentities(reservation localci.WorkloadReservation) []string {
	values := make([]string, len(reservation.Leases))
	for index, lease := range reservation.Leases {
		values[index] = lease.ShardIdentity
	}
	return values
}

func (owner *coordinatorOwner) shardContainerLabels(record coordinatorJobRecord, shard gatecontract.ContainerShard) map[string]string {
	labels := coordinatorContainerLabels(owner.daemonIdentityKey, record, shard.GateIDs[0], gatecontract.ImageIdentity{PlatformManifestDigest: shard.AcceptedManifestDigest, ConfigDigest: shard.AcceptedConfigDigest})
	labels[coordinatorLabelShardIdentity], labels[coordinatorLabelShardIndex], labels[coordinatorLabelPlanDigest] = shard.IdentityDigest, fmt.Sprintf("%d", shard.Index), shard.PlanDigest
	labels[coordinatorLabelJobSource], labels[coordinatorLabelImageConfig], labels[coordinatorLabelImageManifest] = shard.SourceTreeSHA, shard.AcceptedConfigDigest, shard.AcceptedManifestDigest
	return labels
}

func (owner *coordinatorOwner) shardLifecycleHook(jobID string, shard gatecontract.ContainerShard, labels map[string]string) localci.FreshContainerLifecycleHook {
	return func(ctx context.Context, event localci.FreshContainerLifecycleEvent) error {
		return owner.store.recordContainerShardLifecycle(ctx, jobID, shard.IdentityDigest, labels, event)
	}
}

// claimShardExecutionDeadline atomically fixes the first actual Starting instant and reuses its deadline for siblings.
func (store *coordinatorStore) claimShardExecutionDeadline(ctx context.Context, jobID string, started time.Time) (time.Time, error) {
	store.shardDeadlineMu.Lock()
	defer store.shardDeadlineMu.Unlock()
	tx, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	return store.claimShardExecutionDeadlineTx(ctx, tx, jobID, started)
}

// claimShardExecutionDeadlineTx 在同一事务内提交首次时钟或读取并发赢家的 deadline。
func (store *coordinatorStore) claimShardExecutionDeadlineTx(
	ctx context.Context,
	tx *sql.Tx,
	jobID string,
	started time.Time,
) (time.Time, error) {
	defer tx.Rollback()
	var persistedStarted, persistedDeadline sql.NullString
	var profile string
	if err := tx.QueryRowContext(ctx, `SELECT started_at, deadline_at, profile FROM coordinator_jobs WHERE job_id = ? AND state = ?`, jobID, jobStateStarted).Scan(&persistedStarted, &persistedDeadline, &profile); err != nil {
		return time.Time{}, err
	}
	if !persistedStarted.Valid {
		deadline, err := initializeShardExecutionDeadline(ctx, tx, jobID, started, profile)
		if err == nil {
			if err := tx.Commit(); err != nil {
				return time.Time{}, err
			}
			return deadline, nil
		}
		if !errors.Is(err, errShardDeadlineAlreadyClaimed) {
			return time.Time{}, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT deadline_at FROM coordinator_jobs WHERE job_id = ?`, jobID).Scan(&persistedDeadline); err != nil {
			return time.Time{}, err
		}
	}
	deadline, err := time.Parse(timeFormat, persistedDeadline.String)
	if err != nil {
		return time.Time{}, err
	}
	if err := tx.Commit(); err != nil {
		return time.Time{}, err
	}
	return deadline, nil
}

func initializeShardExecutionDeadline(
	ctx context.Context,
	tx *sql.Tx,
	jobID string,
	started time.Time,
	profile string,
) (time.Time, error) {
	deadline := started.Add(coordinatorTimeout(gatecontract.Profile(profile))).UTC()
	updated, err := tx.ExecContext(ctx, `UPDATE coordinator_jobs SET started_at = ?, deadline_at = ? WHERE job_id = ? AND started_at IS NULL`, started.UTC().Format(timeFormat), deadline.Format(timeFormat), jobID)
	if err != nil {
		return time.Time{}, err
	}
	changed, err := updated.RowsAffected()
	if err != nil {
		return time.Time{}, err
	}
	switch changed {
	case 0:
		return time.Time{}, errShardDeadlineAlreadyClaimed
	case 1:
		return deadline, nil
	default:
		return time.Time{}, errors.New("durable shard deadline compare-and-set changed multiple rows")
	}
}

func shardReceipt(shard gatecontract.ContainerShard, result localci.FreshContainerResult) gatecontract.ContainerShardReceipt {
	gates := make([]gatecontract.PlanGateExecution, len(result.PlanGateResults))
	for index, value := range result.PlanGateResults {
		gates[index] = gatecontract.PlanGateExecution{GateID: gatecontract.GateID(value.GateResult.GateID), Status: value.Status, ExitCode: value.GateResult.ExitCode, StartedAt: value.GateResult.StartedAt, CompletedAt: value.GateResult.CompletedAt, ArgvDigest: value.GateResult.ArgvDigest, Log: value.LogOutput, LogDigest: value.GateResult.LogDigest}
	}
	return gatecontract.ContainerShardReceipt{Shard: shard, Status: result.Status, GateExecutions: gates, ContainerID: result.Container.ContainerID, Container: result.Container, ResourceWitness: result.Container.ResourceWitness, ResourceWitnessDigest: result.Container.ResourceWitnessDigest, Removed: result.Container.Removed, RemovalProofDigest: result.RemovalProofDigest, StartedAt: result.StartedAt, ExitedAt: result.ExitedAt, CompletedAt: result.CompletedAt, Deadline: result.Deadline}
}

// shardFailureBarrierIdentity is intentionally independent from worker completion order.
func shardFailureBarrierIdentity(admission coordinatorShardAdmission) (string, error) {
	if len(admission.ShardIdentities) == 0 || admission.ShardIdentities[0] == "" {
		return "", errors.New("shard admission has no durable failure identity")
	}
	return admission.ShardIdentities[0], nil
}

// shardReceiptExecution 将 exact shard receipts 组织为可签名的 receipt execution。
func shardReceiptExecution(accepted gatecontract.AcceptedImageRecord, set gatecontract.ContainerShardSet, receipts []gatecontract.ContainerShardReceipt, aggregate []gatecontract.PlanGateExecution) (receiptExecution, error) {
	execution := receiptExecution{Accepted: accepted, ShardSet: &set, ShardReceipts: append([]gatecontract.ContainerShardReceipt(nil), receipts...)}
	for _, receipt := range receipts {
		execution.Containers = append(execution.Containers, receipt.Container)
		if execution.StartedAt.IsZero() || receipt.StartedAt.Before(execution.StartedAt) {
			execution.StartedAt, execution.Deadline = receipt.StartedAt, receipt.Deadline
		}
		if receipt.CompletedAt.After(execution.CompletedAt) {
			execution.CompletedAt = receipt.CompletedAt
		}
	}
	results := aggregate
	if results == nil {
		for _, receipt := range receipts {
			results = append(results, receipt.GateExecutions...)
		}
	}
	for _, result := range results {
		execution.Results = append(execution.Results, gateResultFromPlanExecution(result))
		execution.Evidence = append(execution.Evidence, gatecontract.Evidence{Kind: gatecontract.EvidenceKindLog, Digest: result.LogDigest})
	}
	return execution, nil
}

func gateStatusForResult(status gatecontract.ResultStatus) gatecontract.GateStatus {
	if status == gatecontract.ResultStatusPassed {
		return gatecontract.GateStatusPassed
	}
	if status == gatecontract.ResultStatusCancelled {
		return gatecontract.GateStatusCancelled
	}
	if status == gatecontract.ResultStatusTimeout {
		return gatecontract.GateStatusTimeout
	}
	return gatecontract.GateStatusFailed
}

func (owner *coordinatorOwner) completeShardGroup(ctx context.Context, record coordinatorJobRecord, admission coordinatorShardAdmission, execution receiptExecution, state jobState, executionErr error) error {
	if state == jobStatePassed {
		if executionErr == nil {
			return owner.completePassedShardGroup(ctx, record, admission, execution)
		}
		state = jobStateInfraFailed
	}
	return owner.completeFailedShardGroup(ctx, record, admission, execution, state, executionErr)
}

func (owner *coordinatorOwner) completePassedShardGroup(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	execution receiptExecution,
) error {
	receipt, err := buildPassedResultReceipt(record, execution, owner.dependencies.ReceiptSigner)
	if err != nil {
		return owner.completeFailedShardGroup(ctx, record, admission, execution, jobStateInfraFailed, fmt.Errorf("sign canonical result receipt: %w", err))
	}
	if err := owner.requireShardGroupRemovalBeforeComplete(ctx, record.JobID); err != nil {
		return err
	}
	if err := owner.store.finishJob(ctx, record.JobID, jobStatePassed, execution.Results, "", &receipt); err != nil {
		return err
	}
	return owner.schedulerClient.CompleteGroup(ctx, admission.WorkloadID, admission.GroupIdentity, schedulerTerminalState(jobStatePassed))
}

// completeFailedShardGroup 先建立 scheduler failure barrier，再释放完成后的 group lease。
func (owner *coordinatorOwner) completeFailedShardGroup(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	execution receiptExecution,
	state jobState,
	executionErr error,
) error {
	if executionErr == nil {
		executionErr = errors.New("shard group did not produce a passed result")
	}
	failureIdentity, err := shardFailureBarrierIdentity(admission)
	if err != nil {
		return err
	}
	// 任何非通过终态都先将 scheduler group 置入 cancelling，随后才允许释放其 gang lease。
	if _, err := owner.schedulerClient.ReportShardFailure(ctx, admission.WorkloadID, admission.GroupIdentity, failureIdentity); err != nil {
		return errors.Join(executionErr, fmt.Errorf("enter shard failure barrier: %w", err))
	}
	if err := owner.requireShardGroupRemovalBeforeComplete(ctx, record.JobID); err != nil {
		return errors.Join(executionErr, err)
	}
	if err := owner.store.finishJob(ctx, record.JobID, state, execution.Results, executionErr.Error(), nil); err != nil {
		return err
	}
	return owner.schedulerClient.CompleteGroup(ctx, admission.WorkloadID, admission.GroupIdentity, schedulerTerminalState(state))
}

// requireShardGroupRemovalBeforeComplete keeps a live gang lease until every started disposable container has removal proof.
func (owner *coordinatorOwner) requireShardGroupRemovalBeforeComplete(ctx context.Context, jobID string) error {
	shards, err := owner.store.containerShards(ctx, jobID)
	if err != nil {
		return err
	}
	for _, shard := range shards {
		if err := requireShardRemovalProof(shard); err != nil {
			return err
		}
	}
	return nil
}

// requireShardRemovalProof 拒绝任何已经可观察到容器却没有 removal proof 的 shard。
func requireShardRemovalProof(shard coordinatorShardRecord) error {
	if shardHasStartedContainer(shard) {
		if shard.ContainerPhase == localci.FreshContainerPhaseRemoved && shard.RemovalProofDigest != "" {
			return nil
		}
		return errors.New("started shard group lacks durable removal proof")
	}
	if shard.ContainerPhase == "" || shard.ContainerPhase == localci.FreshContainerPhasePrepared {
		return nil
	}
	return errors.New("started shard group lacks durable removal proof")
}

func shardHasStartedContainer(shard coordinatorShardRecord) bool {
	if shard.ContainerID != "" || shard.StartedAt != nil {
		return true
	}
	switch shard.ContainerPhase {
	case localci.FreshContainerPhaseCreated,
		localci.FreshContainerPhaseStarting,
		localci.FreshContainerPhaseStarted,
		localci.FreshContainerPhaseExited,
		localci.FreshContainerPhaseRemoved:
		return true
	default:
		return false
	}
}
