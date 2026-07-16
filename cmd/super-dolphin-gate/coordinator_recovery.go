package main

import (
	"context"
	"errors"
	"fmt"

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
	schedulerStates := recoverySchedulerStates(snapshot)
	recovered := make([]coordinatorJobRecord, 0)
	expected := make([]localci.RecoveryWorkload, 0, len(records))
	for index := range records {
		record := records[index]
		observable, err := owner.reconcileRecoveryRecord(ctx, &record, schedulerStates[record.JobID])
		if err != nil {
			return nil, err
		}
		if observable {
			recovered = append(recovered, record)
		}
		expected = append(expected, localci.RecoveryWorkload{
			Request: coordinatorSchedulerRequest(record), Status: schedulerTerminalStateForRecord(record.State),
		})
	}
	if err := owner.schedulerOwner.ReconcileRecovery(ctx, expected); err != nil {
		return nil, fmt.Errorf("reconcile coordinator scheduler recovery: %w", err)
	}
	return recovered, nil
}

func recoverySchedulerStates(snapshot localci.SchedulerSnapshot) map[string]localci.WorkloadStatus {
	states := make(map[string]localci.WorkloadStatus, len(snapshot.Workloads))
	for _, workload := range snapshot.Workloads {
		states[workload.Request.ID] = workload.Status
	}
	return states
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
		return false, owner.rejectRecoveredPassed(ctx, record)
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

func recoverableObservation(observation localci.FreshContainerRecoveryObservation) bool {
	return observation.Status == "running" || observation.Status == "exited"
}

func (owner *coordinatorOwner) failStartedRecovery(
	ctx context.Context,
	record *coordinatorJobRecord,
	cause error,
) error {
	cleanupErr := owner.cleanupRecoveryRecord(ctx, *record)
	return owner.markRecoveryInfra(ctx, record, errors.Join(cause, cleanupErr))
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
		Kind: localci.WorkloadKindJob,
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
		phase == localci.FreshContainerPhaseExited
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
	command, err := coordinatorGateCommand(record.Plan, record.ActiveGateID)
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

func coordinatorGateCommand(plan gatecontract.GatePlan, gateID gatecontract.GateID) ([]string, error) {
	for _, spec := range plan.Gates {
		if spec.ID == gateID {
			return append([]string(nil), spec.Argv...), nil
		}
	}
	return nil, fmt.Errorf("persisted gate %q is absent from canonical plan", gateID)
}

// cleanupRecoveryRecord 对有足够定位证据的旧容器执行幂等 kill、wait、remove。
func (owner *coordinatorOwner) cleanupRecoveryRecord(ctx context.Context, record coordinatorJobRecord) error {
	if record.ContainerPhase == "" || record.ActiveGateID == "" || len(record.ContainerLabels) == 0 ||
		record.ContainerImageReference == "" || record.ContainerConfigDigest == "" || record.SourceSnapshotDir == "" {
		return nil
	}
	command, err := coordinatorGateCommand(record.Plan, record.ActiveGateID)
	if err != nil {
		return err
	}
	_, cleanupErr := owner.dependencies.RecoveryRunner.CleanupUnprovedFreshContainer(ctx, localci.FreshContainerCleanupRequest{
		ContainerID: record.ContainerID, ContainerLabels: record.ContainerLabels,
		ImageReference: record.ContainerImageReference, ConfigDigest: record.ContainerConfigDigest,
		SourceSnapshotDir: record.SourceSnapshotDir, Command: command, Profile: record.Profile,
		GateID: record.ActiveGateID, LifecycleHook: owner.lifecycleHook(record, record.ActiveGateID, record.ContainerLabels),
	})
	return cleanupErr
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
	if err := owner.store.finishJob(ctx, record.JobID, jobStateInfraFailed, nil, message); err != nil {
		return err
	}
	record.State, record.Error, record.GateResults = jobStateInfraFailed, message, nil
	return nil
}

func (owner *coordinatorOwner) observeRecoveredJob(ctx context.Context, record coordinatorJobRecord) error {
	request, err := owner.recoveryRequest(record)
	if err != nil {
		return owner.completeExecution(ctx, record.JobID, jobStateInfraFailed, nil, err)
	}
	result, recoveryErr := owner.dependencies.RecoveryRunner.RecoverFreshContainer(ctx, request)
	state := jobStateInfraFailed
	if result.Status == gatecontract.ResultStatusTimeout {
		state = jobStateTimeout
	}
	if recoveryErr == nil {
		recoveryErr = errors.New("recovered execution has no durable receipt and cannot become passed")
	}
	return owner.completeExecution(ctx, record.JobID, state, nil, recoveryErr)
}

// startRecovered 在 scheduler 恢复服务后接续观察已证明身份的容器。
func (owner *coordinatorOwner) startRecovered(ctx context.Context) {
	for _, recovered := range owner.recovered {
		record := recovered
		owner.workers.Go(func() error {
			if err := owner.observeRecoveredJob(ctx, record); err != nil {
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
