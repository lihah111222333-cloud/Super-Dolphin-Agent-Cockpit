package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

const (
	coordinatorLabelDaemonIdentity = "com.super-dolphin.coordinator.daemon-identity"
	coordinatorLabelJobID          = "com.super-dolphin.coordinator.job-id"
	coordinatorLabelInvocationID   = "com.super-dolphin.coordinator.invocation-id"
	coordinatorLabelGateID         = "com.super-dolphin.coordinator.gate-id"
	coordinatorLabelJobSource      = "com.super-dolphin.coordinator.job-source-tree"
	coordinatorLabelImageConfig    = "com.super-dolphin.coordinator.image-config"
	coordinatorLabelImageManifest  = "com.super-dolphin.coordinator.image-manifest"
	coordinatorLabelShardIdentity  = "com.super-dolphin.coordinator.shard-identity"
	coordinatorLabelShardIndex     = "com.super-dolphin.coordinator.shard-index"
	coordinatorLabelPlanDigest     = "com.super-dolphin.coordinator.plan-digest"
)

func coordinatorContainerLabels(
	daemonIdentityKey string,
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	image gatecontract.ImageIdentity,
) map[string]string {
	return map[string]string{
		coordinatorLabelDaemonIdentity: daemonIdentityKey,
		coordinatorLabelJobID:          record.JobID, coordinatorLabelInvocationID: record.InvocationID,
		coordinatorLabelGateID: string(gateID), coordinatorLabelJobSource: record.JobSourceTreeSHA,
		coordinatorLabelImageConfig:   image.ConfigDigest,
		coordinatorLabelImageManifest: image.PlatformManifestDigest,
	}
}

func (owner *coordinatorOwner) lifecycleHook(
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	labels map[string]string,
) localci.FreshContainerLifecycleHook {
	return func(ctx context.Context, event localci.FreshContainerLifecycleEvent) error {
		return owner.store.recordContainerLifecycle(ctx, record.JobID, gateID, labels, event)
	}
}

// reconcileRecovery 在开放 RPC 前统一判定 coordinator 与 scheduler 的持久状态。
func (owner *coordinatorOwner) reconcileRecovery(ctx context.Context) ([]coordinatorJobRecord, error) {
	records, err := owner.store.jobs(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := owner.schedulerOwner.RecoverySnapshot()
	if err != nil {
		return nil, err
	}
	schedulerWorkloads := recoverySchedulerWorkloads(snapshot)
	recovered := make([]coordinatorJobRecord, 0)
	expected, err := recoveryBuildWorkloads(records, schedulerWorkloads)
	if err != nil {
		return nil, err
	}
	for index := range records {
		record := records[index]
		workloads, observable, err := owner.recoveryRecordWorkloads(ctx, &record, schedulerWorkloads)
		if err != nil {
			return nil, err
		}
		if observable {
			recovered = append(recovered, record)
		}
		expected = append(expected, workloads...)
	}
	if err := owner.schedulerOwner.ReconcileRecovery(ctx, expected); err != nil {
		return nil, fmt.Errorf("reconcile coordinator scheduler recovery: %w", err)
	}
	return recovered, nil
}

// recoveryRecordWorkloads 按是否存在 shard admission 分流单容器与分片恢复。
func (owner *coordinatorOwner) recoveryRecordWorkloads(
	ctx context.Context,
	record *coordinatorJobRecord,
	schedulerWorkloads map[string]localci.WorkloadSnapshot,
) ([]localci.RecoveryWorkload, bool, error) {
	admission, err := owner.store.shardAdmission(ctx, record.JobID)
	if err == nil {
		return owner.shardAdmissionRecoveryWorkloads(ctx, *record, admission, schedulerWorkloads)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("read shard admission recovery state: %w", err)
	}
	if err := owner.rejectFailedRecoveryDependency(ctx, record, schedulerWorkloads); err != nil {
		return nil, false, err
	}
	observable, err := owner.reconcileRecoveryRecord(ctx, record, schedulerWorkloads[record.JobID].Status)
	if err != nil {
		return nil, false, err
	}
	workload := localci.RecoveryWorkload{
		Request: coordinatorSchedulerRequest(*record), Status: schedulerTerminalStateForRecord(record.State),
	}
	return []localci.RecoveryWorkload{workload}, observable, nil
}

// shardAdmissionRecoveryWorkloads 只重建未执行 outbox，绝不重排终态或存活组。
func (owner *coordinatorOwner) shardAdmissionRecoveryWorkloads(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	schedulerWorkloads map[string]localci.WorkloadSnapshot,
) ([]localci.RecoveryWorkload, bool, error) {
	shards, err := owner.store.containerShards(ctx, record.JobID)
	if err != nil {
		return nil, false, err
	}
	set, err := containerShardSetForRecord(record, shards)
	if err != nil {
		return nil, false, err
	}
	request, err := recoveryShardAdmissionRequest(record, set, admission)
	if err != nil {
		return nil, false, err
	}
	parent := localci.RecoveryWorkload{Request: coordinatorSchedulerRequest(record), Status: localci.WorkloadStatusPassed}
	group := localci.RecoveryWorkload{Request: request}
	snapshot, exists := schedulerWorkloads[admission.WorkloadID]
	if exists && !equalRecoveryWorkloadRequest(snapshot.Request, request) {
		return nil, false, errors.New("recovery scheduler shard workload identity drifted")
	}
	if record.State.terminal() {
		return terminalShardAdmissionRecovery(parent, group, record.State, snapshot, exists)
	}
	return activeShardAdmissionRecovery(parent, group, record, admission, snapshot, exists)
}

// recoveryShardAdmissionRequest 校验 admission 与 canonical shard set 的完整绑定。
func recoveryShardAdmissionRequest(
	record coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
	admission coordinatorShardAdmission,
) (localci.WorkloadRequest, error) {
	_, request, err := shardAdmissionForSet(record, set)
	if err != nil {
		return localci.WorkloadRequest{}, err
	}
	bound := request.ID == admission.WorkloadID && request.GroupIdentity == admission.GroupIdentity
	if !bound || !reflect.DeepEqual(request.ShardIdentities, admission.ShardIdentities) {
		return localci.WorkloadRequest{}, errors.New("recovery shard admission binding drifted")
	}
	return request, nil
}

// terminalShardAdmissionRecovery 仅恢复尚未完成 scheduler barrier 的终态组。
func terminalShardAdmissionRecovery(
	parent, group localci.RecoveryWorkload,
	state jobState,
	snapshot localci.WorkloadSnapshot,
	exists bool,
) ([]localci.RecoveryWorkload, bool, error) {
	resume := exists && slices.Contains([]localci.WorkloadStatus{
		localci.WorkloadStatusStarted, localci.WorkloadStatusCancelling,
	}, snapshot.Status)
	if exists && !resume && snapshot.Status != schedulerTerminalState(state) {
		return nil, false, errors.New("terminal shard job disagrees with scheduler group")
	}
	group.Status = schedulerTerminalState(state)
	if resume {
		group.Status = snapshot.Status
	}
	return []localci.RecoveryWorkload{parent, group}, resume, nil
}

// activeShardAdmissionRecovery 判定 outbox 重放或已运行组恢复。
func activeShardAdmissionRecovery(
	parent, group localci.RecoveryWorkload,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	snapshot localci.WorkloadSnapshot,
	exists bool,
) ([]localci.RecoveryWorkload, bool, error) {
	if admission.Phase != shardAdmissionOutbox && admission.Phase != shardAdmissionEnqueued {
		return nil, false, errors.New("shard admission has invalid durable phase")
	}
	if record.State != jobStateStarted {
		return nil, false, errors.New("nonterminal shard admission has invalid coordinator state")
	}
	if !exists || snapshot.Status == localci.WorkloadStatusQueued {
		if !shardAdmissionIsUnexecuted(record) {
			return nil, false, errors.New("queued shard admission has observed container lifecycle")
		}
		group.Status = localci.WorkloadStatusQueued
		return []localci.RecoveryWorkload{parent, group}, true, nil
	}
	if slices.Contains([]localci.WorkloadStatus{localci.WorkloadStatusStarted, localci.WorkloadStatusCancelling}, snapshot.Status) {
		group.Status = snapshot.Status
		return []localci.RecoveryWorkload{parent, group}, true, nil
	}
	return nil, false, fmt.Errorf("started shard job disagrees with terminal scheduler state %q", snapshot.Status)
}

// shardAdmissionIsUnexecuted accepts only the durable prep-to-queue crash window, never a partially observed container.
func shardAdmissionIsUnexecuted(record coordinatorJobRecord) bool {
	if slices.Contains([]bool{
		record.StartedAt != nil,
		record.Deadline != nil,
		len(record.ContainerShards) != gatecontract.MaxContainerShards,
	}, true) {
		return false
	}
	for _, shard := range record.ContainerShards {
		if slices.Contains([]bool{
			shard.ContainerPhase != "", shard.ContainerID != "", shard.StartedAt != nil, shard.Deadline != nil,
			shard.CompletedAt != nil, shard.ExitCode != nil, shard.SourceSnapshotDir != "", shard.RemovalProofDigest != "",
		}, true) {
			return false
		}
	}
	return true
}

func recoverySchedulerWorkloads(snapshot localci.SchedulerSnapshot) map[string]localci.WorkloadSnapshot {
	workloads := make(map[string]localci.WorkloadSnapshot, len(snapshot.Workloads))
	for _, workload := range snapshot.Workloads {
		workloads[workload.Request.ID] = workload
	}
	return workloads
}

// equalRecoveryWorkloadRequest 比较 scheduler 恢复请求的完整 durable identity。
func equalRecoveryWorkloadRequest(left, right localci.WorkloadRequest) bool {
	return left.ID == right.ID && left.InvocationID == right.InvocationID &&
		left.EnqueueSequence == right.EnqueueSequence && left.Subsequence == right.Subsequence &&
		left.Kind == right.Kind && left.ServiceCount == right.ServiceCount &&
		left.GroupIdentity == right.GroupIdentity && left.GroupSize == right.GroupSize &&
		slices.Equal(left.ShardIdentities, right.ShardIdentities) && slices.Equal(left.Dependencies, right.Dependencies)
}

// recoveryBuildWorkloads 按首次 enqueue 顺序去重并重建持久 build DAG。
func recoveryBuildWorkloads(
	records []coordinatorJobRecord,
	snapshot map[string]localci.WorkloadSnapshot,
) ([]localci.RecoveryWorkload, error) {
	builds := make([]localci.RecoveryWorkload, 0)
	seen := make(map[string]struct{})
	for _, record := range records {
		dependencyID, err := candidateBuildDependency(record)
		if err != nil {
			return nil, err
		}
		if dependencyID == "" {
			continue
		}
		if _, duplicate := seen[dependencyID]; duplicate {
			continue
		}
		build, err := recoveryBuildWorkload(record, dependencyID, snapshot[dependencyID])
		if err != nil {
			return nil, err
		}
		seen[dependencyID] = struct{}{}
		builds = append(builds, build)
	}
	return builds, nil
}

func recoveryBuildWorkload(
	record coordinatorJobRecord,
	dependencyID string,
	snapshot localci.WorkloadSnapshot,
) (localci.RecoveryWorkload, error) {
	request := localci.WorkloadRequest{
		ID: dependencyID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Kind: localci.WorkloadKindBuild,
	}
	status := localci.WorkloadStatusQueued
	if snapshot.Request.ID != "" {
		if snapshot.Request.Kind != localci.WorkloadKindBuild {
			return localci.RecoveryWorkload{}, fmt.Errorf("scheduler dependency %q is not a build workload", dependencyID)
		}
		request, status = snapshot.Request, snapshot.Status
		if status == localci.WorkloadStatusStarted {
			status = localci.WorkloadStatusQueued
		}
	}
	return localci.RecoveryWorkload{Request: request, Status: status}, nil
}

// rejectFailedRecoveryDependency 在候选镜像构建已失败时终止恢复中的排队任务。
func (owner *coordinatorOwner) rejectFailedRecoveryDependency(
	ctx context.Context,
	record *coordinatorJobRecord,
	snapshot map[string]localci.WorkloadSnapshot,
) error {
	if record.State != jobStateQueued {
		return nil
	}
	dependencyID, err := candidateBuildDependency(*record)
	if err != nil || dependencyID == "" {
		return err
	}
	status := snapshot[dependencyID].Status
	if status != localci.WorkloadStatusFailed && status != localci.WorkloadStatusInfraFailed {
		return nil
	}
	return owner.markRecoveryInfra(ctx, record, errors.New("candidate build dependency is terminal without accepted image"))
}

func (owner *coordinatorOwner) reconcileRecoveryRecord(
	ctx context.Context,
	record *coordinatorJobRecord,
	schedulerState localci.WorkloadStatus,
) (bool, error) {
	switch record.State {
	case jobStateQueued:
		return false, owner.reconcileQueuedRecord(ctx, record, schedulerState)
	case jobStateStarted:
		return owner.reconcileStartedRecord(ctx, record, schedulerState)
	case jobStatePassed:
		if record.Receipt == nil {
			return false, owner.rejectRecoveredPassed(ctx, record)
		}
		return false, nil
	default:
		return false, nil
	}
}

func (owner *coordinatorOwner) reconcileQueuedRecord(
	ctx context.Context,
	record *coordinatorJobRecord,
	schedulerState localci.WorkloadStatus,
) error {
	compatible := schedulerState == "" || schedulerState == localci.WorkloadStatusQueued ||
		schedulerState == localci.WorkloadStatusStarted
	if compatible {
		return nil
	}
	return owner.markRecoveryInfra(ctx, record, errors.New("durable queued job disagrees with terminal scheduler state"))
}

// reconcileStartedRecord 只有 scheduler lease 与 Docker 证明同时成立时才保留 started。
func (owner *coordinatorOwner) reconcileStartedRecord(
	ctx context.Context,
	record *coordinatorJobRecord,
	schedulerState localci.WorkloadStatus,
) (bool, error) {
	if len(record.ContainerShards) != 0 {
		return owner.reconcileStartedShardRecord(record, schedulerState)
	}
	if schedulerState != localci.WorkloadStatusStarted || !owner.recoveryRecordObservable(*record) {
		cause := errors.New("started job lacks matching scheduler lease or observable container identity")
		return false, owner.failStartedRecovery(ctx, record, cause)
	}
	request, err := owner.recoveryRequest(*record)
	if err != nil {
		return false, owner.failStartedRecovery(ctx, record, err)
	}
	observation, err := owner.dependencies.RecoveryRunner.ProbeFreshContainerRecovery(ctx, request)
	if err != nil || !recoverableObservation(observation) {
		return false, owner.failStartedRecovery(ctx, record, err)
	}
	record.ContainerID = observation.ContainerID
	return true, nil
}

// reconcileStartedShardRecord 在 scheduler client 建连前只验证全片恢复请求，实际 probe 由 group runner 唯一执行。
func (owner *coordinatorOwner) reconcileStartedShardRecord(
	record *coordinatorJobRecord,
	schedulerState localci.WorkloadStatus,
) (bool, error) {
	if schedulerState != localci.WorkloadStatusStarted && schedulerState != localci.WorkloadStatusCancelling {
		return false, fmt.Errorf("started shard group has incompatible scheduler state %q", schedulerState)
	}
	if interfaceIsNil(owner.dependencies.RecoveryRunner) {
		return false, errors.New("shard recovery runner is required")
	}
	for _, shard := range record.ContainerShards {
		if recoveryShardNeverStarted(shard) {
			continue
		}
		if _, err := owner.recoveryShardRequest(*record, shard); err != nil {
			return false, err
		}
	}
	return true, nil
}

type recoveredShardProbe struct {
	shard        coordinatorShardRecord
	request      localci.FreshContainerRecoveryRequest
	observation  localci.FreshContainerRecoveryObservation
	err          error
	neverStarted bool
}

// probeRecoveredShards 探测完整 durable shard 集，不因单片失败跳过后续成员。
func (owner *coordinatorOwner) probeRecoveredShards(ctx context.Context, record coordinatorJobRecord) []recoveredShardProbe {
	probes := make([]recoveredShardProbe, len(record.ContainerShards))
	for index, shard := range record.ContainerShards {
		probes[index].shard = shard
		if recoveryShardNeverStarted(shard) {
			probes[index].neverStarted = true
			continue
		}
		request, err := owner.recoveryShardRequest(record, shard)
		probes[index].request, probes[index].err = request, err
		if err != nil {
			continue
		}
		probes[index].observation, probes[index].err = owner.dependencies.RecoveryRunner.ProbeFreshContainerRecovery(ctx, request)
		if probes[index].err == nil && (!recoverableObservation(probes[index].observation) ||
			probes[index].observation.ContainerID != shard.ContainerID) {
			probes[index].err = fmt.Errorf("recovery shard %q is missing or identity drifted", shard.Shard.IdentityDigest)
		}
	}
	return probes
}

// recoveredShardProbesAreLive 要求每片均已启动、可观察且探测无漂移。
func recoveredShardProbesAreLive(probes []recoveredShardProbe) bool {
	if len(probes) != gatecontract.MaxContainerShards {
		return false
	}
	for _, probe := range probes {
		if probe.neverStarted || probe.err != nil || !observableContainerPhase(probe.shard.ContainerPhase) {
			return false
		}
	}
	return true
}

func recoverableObservation(observation localci.FreshContainerRecoveryObservation) bool {
	return observation.Status == "running" || observation.Status == "exited"
}

func (owner *coordinatorOwner) failStartedRecovery(
	ctx context.Context,
	record *coordinatorJobRecord,
	cause error,
) error {
	if err := owner.cleanupRecoveryRecord(ctx, *record); err != nil {
		return errors.Join(cause, err)
	}
	if err := owner.requireDurableContainerRemovalProof(ctx, record.JobID); err != nil {
		return errors.Join(cause, err)
	}
	if err := cleanupDeterministicRecoverySource(*record); err != nil {
		return errors.Join(cause, err)
	}
	return owner.markRecoveryInfra(ctx, record, cause)
}

func (owner *coordinatorOwner) requireDurableContainerRemovalProof(ctx context.Context, jobID string) error {
	persisted, err := owner.store.job(ctx, jobID)
	if err != nil {
		return err
	}
	if persisted.ContainerPhase != localci.FreshContainerPhaseRemoved || persisted.RemovalProofDigest == "" {
		return errors.New("recovery cleanup has no durable container removal proof")
	}
	return nil
}

func (owner *coordinatorOwner) rejectRecoveredPassed(ctx context.Context, record *coordinatorJobRecord) error {
	const message = "recovered passed job has no durable receipt"
	if err := owner.store.replaceRecoveredPassed(ctx, record.JobID, message); err != nil {
		return err
	}
	record.State, record.Error, record.GateResults = jobStateInfraFailed, message, nil
	return nil
}

func coordinatorSchedulerRequest(record coordinatorJobRecord) localci.WorkloadRequest {
	return localci.WorkloadRequest{
		ID: record.JobID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Subsequence: record.SchedulerSubsequence, Kind: localci.WorkloadKindJob,
		Dependencies: append([]string(nil), record.SchedulerDependencies...),
	}
}

func schedulerTerminalStateForRecord(state jobState) localci.WorkloadStatus {
	if state == jobStateQueued {
		return localci.WorkloadStatusQueued
	}
	if state == jobStateStarted {
		return localci.WorkloadStatusStarted
	}
	return schedulerTerminalState(state)
}

// recoveryRecordObservable 只接受已持久化首次启动时钟和不可变容器身份的记录。
func (owner *coordinatorOwner) recoveryRecordObservable(record coordinatorJobRecord) bool {
	if record.StartedAt == nil || record.Deadline == nil || record.ContainerLabels == nil {
		return false
	}
	if !observableContainerPhase(record.ContainerPhase) {
		return false
	}
	return owner.recoveryLabelsMatch(record)
}

func observableContainerPhase(phase localci.FreshContainerLifecyclePhase) bool {
	return phase == localci.FreshContainerPhaseStarting ||
		phase == localci.FreshContainerPhaseStarted ||
		phase == localci.FreshContainerPhaseExited ||
		phase == localci.FreshContainerPhaseRemovalPending
}

func (owner *coordinatorOwner) recoveryLabelsMatch(record coordinatorJobRecord) bool {
	expected := map[string]string{
		coordinatorLabelDaemonIdentity: owner.daemonIdentityKey,
		coordinatorLabelJobID:          record.JobID, coordinatorLabelInvocationID: record.InvocationID,
		coordinatorLabelGateID: string(record.ActiveGateID), coordinatorLabelJobSource: record.JobSourceTreeSHA,
		coordinatorLabelImageConfig: record.ContainerConfigDigest,
	}
	for key, value := range expected {
		if record.ContainerLabels[key] != value {
			return false
		}
	}
	return true
}

func (owner *coordinatorOwner) recoveryRequest(record coordinatorJobRecord) (localci.FreshContainerRecoveryRequest, error) {
	command, err := gatecontract.PlanExecutorArgv(record.Plan)
	if err != nil {
		return localci.FreshContainerRecoveryRequest{}, err
	}
	if record.StartedAt == nil || record.Deadline == nil {
		return localci.FreshContainerRecoveryRequest{}, errors.New("recovery timestamps are incomplete")
	}
	return localci.FreshContainerRecoveryRequest{
		ContainerID: record.ContainerID, ContainerLabels: record.ContainerLabels,
		ImageReference: record.ContainerImageReference, ConfigDigest: record.ContainerConfigDigest,
		SourceSnapshotDir: record.SourceSnapshotDir, Command: command, Profile: record.Profile,
		GateID: record.ActiveGateID, StartedAt: record.StartedAt.UTC(), Deadline: record.Deadline.UTC(),
		LifecycleHook: owner.lifecycleHook(record, record.ActiveGateID, record.ContainerLabels),
	}, nil
}

// cleanupRecoveryRecord 对有足够定位证据的旧容器执行幂等 kill、wait、remove。
func (owner *coordinatorOwner) cleanupRecoveryRecord(ctx context.Context, record coordinatorJobRecord) error {
	request, err := owner.recoveryCleanupRequest(record)
	if err != nil {
		return err
	}
	if request == nil {
		return nil
	}
	result, cleanupErr := owner.dependencies.RecoveryRunner.CleanupUnprovedFreshContainer(ctx, *request)
	if cleanupErr != nil {
		return cleanupErr
	}
	if !result.Container.Removed || result.RemovalProofDigest == "" {
		return errors.New("recovery cleanup has no container removal proof")
	}
	return nil
}

func (owner *coordinatorOwner) recoveryCleanupRequest(record coordinatorJobRecord) (*localci.FreshContainerCleanupRequest, error) {
	if recoveryCleanupIdentityIncomplete(record) {
		return nil, nil
	}
	command, err := gatecontract.PlanExecutorArgv(record.Plan)
	if err != nil {
		return nil, err
	}
	return &localci.FreshContainerCleanupRequest{
		ContainerID: record.ContainerID, ContainerLabels: record.ContainerLabels,
		ImageReference: record.ContainerImageReference, ConfigDigest: record.ContainerConfigDigest,
		SourceSnapshotDir: record.SourceSnapshotDir, Command: command, Profile: record.Profile,
		GateID: record.ActiveGateID, RemovalPending: record.ContainerPhase == localci.FreshContainerPhaseRemovalPending,
		LifecycleHook: owner.lifecycleHook(record, record.ActiveGateID, record.ContainerLabels),
	}, nil
}

// recoveryCleanupIdentityIncomplete 判断重启记录是否缺少足以安全定位旧容器的不可变身份字段。
func recoveryCleanupIdentityIncomplete(record coordinatorJobRecord) bool {
	return record.ContainerPhase == "" || record.ActiveGateID == "" || len(record.ContainerLabels) == 0 ||
		record.ContainerImageReference == "" || record.ContainerConfigDigest == "" || record.SourceSnapshotDir == ""
}

// cleanupDeterministicRecoverySource 在进程丢失内存 Cleanup 闭包后，仅回收与 job ID 精确对应的物化快照根目录。
func cleanupDeterministicRecoverySource(record coordinatorJobRecord) error {
	if record.JobID == "" || record.SourceSnapshotDir == "" {
		return errors.New("recovery source snapshot identity is incomplete")
	}
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		return err
	}
	outputRoot := filepath.Join(runtimeRoot, "jobs", record.JobID)
	expectedSnapshot := filepath.Join(outputRoot, "snapshot")
	if filepath.Clean(record.SourceSnapshotDir) != expectedSnapshot {
		return errors.New("recovery source snapshot path drifted from deterministic job root")
	}
	if err := os.RemoveAll(outputRoot); err != nil {
		return fmt.Errorf("remove recovery source snapshot: %w", err)
	}
	return nil
}

func (owner *coordinatorOwner) markRecoveryInfra(
	ctx context.Context,
	record *coordinatorJobRecord,
	cause error,
) error {
	message := "coordinator recovery could not prove the original execution"
	if cause != nil {
		message += ": " + cause.Error()
	}
	if err := owner.store.finishJob(ctx, record.JobID, jobStateInfraFailed, nil, message, nil); err != nil {
		return err
	}
	record.State, record.Error, record.GateResults = jobStateInfraFailed, message, nil
	return nil
}

// recoveryShardRequest 复用初次执行的 canonical shard argv，并只读取 durable group clock。
func (owner *coordinatorOwner) recoveryShardRequest(
	record coordinatorJobRecord,
	shard coordinatorShardRecord,
) (localci.FreshContainerRecoveryRequest, error) {
	if record.StartedAt == nil || record.Deadline == nil || len(shard.Shard.GateIDs) == 0 {
		return localci.FreshContainerRecoveryRequest{}, errors.New("recovery shard group clock or gate identity is incomplete")
	}
	if !reflect.DeepEqual(shard.ContainerLabels, owner.shardContainerLabels(record, shard.Shard)) {
		return localci.FreshContainerRecoveryRequest{}, errors.New("recovery shard labels drifted from initial execution")
	}
	command, err := gatecontract.ContainerShardExecutorArgv(record.Plan, shard.Shard.GateIDs)
	if err != nil {
		return localci.FreshContainerRecoveryRequest{}, err
	}
	return localci.FreshContainerRecoveryRequest{
		ContainerID: shard.ContainerID, ContainerLabels: shard.ContainerLabels,
		ImageReference: shard.ContainerImageReference, ConfigDigest: shard.ContainerConfigDigest,
		SourceSnapshotDir: shard.SourceSnapshotDir, Command: command, Profile: record.Profile,
		GateID: shard.Shard.GateIDs[0], StartedAt: record.StartedAt.UTC(), Deadline: record.Deadline.UTC(),
		LifecycleHook: owner.shardLifecycleHook(record.JobID, shard.Shard, shard.ContainerLabels),
	}, nil
}

// resumeRecoveredShardGroup 依据真实 admission workload 状态选择 outbox、观察、清理或终态补偿。
func (owner *coordinatorOwner) resumeRecoveredShardGroup(ctx context.Context, record coordinatorJobRecord) error {
	admission, err := owner.store.shardAdmission(ctx, record.JobID)
	if err != nil {
		return err
	}
	snapshot, exists, err := owner.recoveryShardSchedulerSnapshot(ctx, record, admission)
	if err != nil {
		return err
	}
	if !exists || snapshot.Status == localci.WorkloadStatusQueued {
		return owner.resumeQueuedShardAdmission(ctx, record, admission)
	}
	if schedulerRecoveryStatusTerminal(snapshot.Status) {
		return validateTerminalRecoveredShardGroup(record, snapshot.Status)
	}
	if record.State.terminal() {
		return owner.finishRecoveredTerminalShardGroup(ctx, record, admission, snapshot.Status)
	}
	return owner.resumeActiveRecoveredShardGroup(ctx, record, admission, snapshot.Status)
}

// resumeQueuedShardAdmission 幂等重放尚未观察到容器生命周期的 admission outbox。
func (owner *coordinatorOwner) resumeQueuedShardAdmission(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
) error {
	if !shardAdmissionIsUnexecuted(record) {
		return errors.New("queued recovery shard group has lifecycle evidence")
	}
	return owner.enqueueShardAdmission(ctx, record, admission)
}

// validateTerminalRecoveredShardGroup 拒绝 job 与 scheduler 终态漂移。
func validateTerminalRecoveredShardGroup(record coordinatorJobRecord, status localci.WorkloadStatus) error {
	if !record.State.terminal() || status != schedulerTerminalState(record.State) {
		return errors.New("terminal recovery shard group drifted from durable job")
	}
	return nil
}

// resumeActiveRecoveredShardGroup 只探测一次并据此选择整组观察或清理。
func (owner *coordinatorOwner) resumeActiveRecoveredShardGroup(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	status localci.WorkloadStatus,
) error {
	probes := owner.probeRecoveredShards(ctx, record)
	if status == localci.WorkloadStatusCancelling || !recoveredShardProbesAreLive(probes) {
		return owner.failRecoveredShardGroup(ctx, record, admission, errors.New("recovery shard group is incomplete or unproved"))
	}
	return owner.observeRecoveredShardGroup(ctx, record, admission, probes)
}

// observeRecoveredShardGroup 并发接管全部已证明容器，结果按 canonical shard index 精确归属。
func (owner *coordinatorOwner) observeRecoveredShardGroup(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	probes []recoveredShardProbe,
) error {
	image, err := owner.ensureRecoveredShardImage(ctx, record, probes)
	if err != nil {
		return owner.failRecoveredShardGroup(ctx, record, admission, err)
	}
	results, errs, barrierErr := owner.runRecoveredShards(ctx, admission, probes)
	if barrierErr != nil {
		return barrierErr
	}
	return owner.finishObservedRecoveredShards(ctx, record, admission, image, probes, results, errs)
}

// ensureRecoveredShardImage 在有界操作内验证恢复镜像仍匹配 durable acceptance。
func (owner *coordinatorOwner) ensureRecoveredShardImage(
	ctx context.Context,
	record coordinatorJobRecord,
	probes []recoveredShardProbe,
) (ensuredImage, error) {
	imageCtx, cancelImage := localci.BoundedOperationContext(ctx, coordinatorProvisioningTimeout)
	image, err := owner.ensureJobImage(imageCtx, record)
	cancelImage()
	if err != nil || image.Identity.PlatformManifestDigest != probes[0].shard.Shard.AcceptedManifestDigest ||
		image.Identity.ConfigDigest != probes[0].shard.Shard.AcceptedConfigDigest {
		return ensuredImage{}, errors.Join(err, errors.New("recovery accepted image drifted"))
	}
	return image, nil
}

// recoveryShardSchedulerSnapshot 以 admission workload ID 精确查找并验证真实组。
func (owner *coordinatorOwner) recoveryShardSchedulerSnapshot(
	ctx context.Context,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
) (localci.WorkloadSnapshot, bool, error) {
	shards, err := owner.store.containerShards(ctx, record.JobID)
	if err != nil {
		return localci.WorkloadSnapshot{}, false, err
	}
	set, err := containerShardSetForRecord(record, shards)
	if err != nil {
		return localci.WorkloadSnapshot{}, false, err
	}
	_, request, err := shardAdmissionForSet(record, set)
	if err != nil || request.ID != admission.WorkloadID || request.GroupIdentity != admission.GroupIdentity {
		return localci.WorkloadSnapshot{}, false, errors.New("recovery shard admission identity drifted")
	}
	scheduler, err := owner.schedulerClient.Snapshot(ctx)
	if err != nil {
		return localci.WorkloadSnapshot{}, false, err
	}
	for _, workload := range scheduler.Workloads {
		if workload.Request.ID == admission.WorkloadID {
			if !equalRecoveryWorkloadRequest(workload.Request, request) {
				return localci.WorkloadSnapshot{}, false, errors.New("recovery scheduler shard request drifted")
			}
			return workload, true, nil
		}
	}
	return localci.WorkloadSnapshot{}, false, nil
}

func schedulerRecoveryStatusTerminal(status localci.WorkloadStatus) bool {
	terminal := []localci.WorkloadStatus{
		localci.WorkloadStatusPassed, localci.WorkloadStatusFailed,
		localci.WorkloadStatusInfraFailed, localci.WorkloadStatusCancelled,
	}
	return slices.Contains(terminal, status)
}

// startRecovered 在 scheduler 恢复服务后接续观察已证明身份的容器。
func (owner *coordinatorOwner) startRecovered(ctx context.Context) {
	for _, recovered := range owner.recovered {
		record := recovered
		owner.workers.Go(func() error {
			var err error
			if len(record.ContainerShards) == 0 {
				err = owner.observeRecoveredJob(ctx, record)
			} else {
				err = owner.resumeRecoveredShardGroup(ctx, record)
			}
			if err != nil {
				select {
				case owner.fatal <- err:
				default:
				}
				return err
			}
			return nil
		})
	}
	owner.recovered = nil
}
