package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
)

func TestRecoveryRequestUsesCanonicalPlanExecutorArgv(t *testing.T) {
	plan := mustTestGatePlan(t, "b")
	started := time.Now().UTC()
	owner := &coordinatorOwner{daemonIdentityKey: "daemon"}
	record := coordinatorJobRecord{
		JobID: "job", InvocationID: "invocation", Plan: plan, Profile: plan.Profile,
		JobSourceTreeSHA: plan.Source.SourceTreeSHA, ActiveGateID: plan.Gates[0].ID,
		ContainerPhase: localci.FreshContainerPhaseStarted, ContainerID: strings.Repeat("c", 64),
		ContainerLabels: map[string]string{}, ContainerImageReference: "repo@sha256:" + strings.Repeat("b", 64),
		ContainerConfigDigest: productionDigest("a"), SourceSnapshotDir: t.TempDir(),
		StartedAt: &started,
	}
	deadline := started.Add(coordinatorTimeout(plan.Profile))
	record.Deadline = &deadline
	request, err := owner.recoveryRequest(record)
	if err != nil {
		t.Fatal(err)
	}
	want, err := gatecontract.PlanExecutorArgv(plan)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(request.Command, want) {
		t.Fatalf("recovery command=%v want plan executor=%v", request.Command, want)
	}
}

func TestRequireExecutionCapacityReleaseRequiresContainerRemovalProof(t *testing.T) {
	record := coordinatorJobRecord{ContainerPhase: localci.FreshContainerPhaseCreated}
	execution := receiptExecution{ContainerObserved: true}
	if err := requireExecutionCapacityRelease(record, execution); err == nil {
		t.Fatal("scheduler capacity was releasable without container removal proof")
	}
	record.ContainerPhase = localci.FreshContainerPhaseRemoved
	record.RemovalProofDigest = productionDigest("d")
	execution.ContainerRemovalProven = true
	execution.ContainerRemovalProofDigest = record.RemovalProofDigest
	if err := requireExecutionCapacityRelease(record, execution); err != nil {
		t.Fatalf("scheduler capacity remained held after removal proof: %v", err)
	}
}

func TestRequireExecutionCapacityReleaseDistinguishesPreCreateFromCreateAttempt(t *testing.T) {
	for _, phase := range []localci.FreshContainerLifecyclePhase{"", localci.FreshContainerPhasePrepared} {
		if err := requireExecutionCapacityRelease(coordinatorJobRecord{ContainerPhase: phase}, receiptExecution{}); err != nil {
			t.Fatalf("phase %q rejected a no-container pre-create failure: %v", phase, err)
		}
	}
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhaseCreating, localci.FreshContainerPhaseCreated,
		localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseExited,
		localci.FreshContainerPhaseRemovalPending,
	} {
		if err := requireExecutionCapacityRelease(coordinatorJobRecord{ContainerPhase: phase}, receiptExecution{}); err == nil {
			t.Fatalf("phase %q released scheduler capacity after Docker create was attempted", phase)
		}
	}
}

func TestRemovalPendingLifecycleRemainsObservableForRecovery(t *testing.T) {
	if !observableContainerPhase(localci.FreshContainerPhaseRemovalPending) {
		t.Fatal("removal_pending lifecycle was skipped by coordinator recovery")
	}
}

func TestCompleteExecutionKeepsCapacityWhenRemovalIsNotDurable(t *testing.T) {
	for _, test := range []struct {
		name           string
		persistRemoval bool
		proof          string
	}{
		{name: "missing lifecycle", proof: containerRemovalProofDigest(strings.Repeat("a", 64))},
		{name: "proof drift", persistRemoval: true, proof: coordinatorDigest("e")},
	} {
		t.Run(test.name, func(t *testing.T) {
			owner := startDeadlineCleanupOwner(t)
			record := prepareStartedDeadlineJob(t, owner)
			proof := containerRemovalProofDigest(strings.Repeat("a", 64))
			if test.persistRemoval {
				persistRemovalLifecycle(t, owner, record)
			}
			execution := receiptExecution{
				ContainerObserved: true, ContainerRemovalProven: true, ContainerRemovalProofDigest: test.proof,
			}
			if err := owner.completeExecution(context.Background(), record, execution, jobStateInfraFailed, errors.New("execution failed")); err == nil {
				t.Fatal("completeExecution() released scheduler capacity without matching durable removal proof")
			}
			status, err := owner.schedulerClient.State(context.Background(), record.JobID)
			if err != nil || status != localci.WorkloadStatusStarted {
				t.Fatalf("scheduler workload status=%q err=%v, want started lease retained", status, err)
			}
			snapshot, err := owner.schedulerClient.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !schedulerSnapshotHasWorkloadLease(snapshot, record.JobID) {
				t.Fatalf("scheduler snapshot released capacity after incomplete execution: %#v", snapshot)
			}
			if test.persistRemoval && proof == test.proof {
				t.Fatal("test fixture did not create a removal proof drift")
			}
		})
	}
}

func schedulerSnapshotHasWorkloadLease(snapshot localci.SchedulerSnapshot, workloadID string) bool {
	for _, lease := range snapshot.Leases {
		if lease.WorkloadID == workloadID {
			return true
		}
	}
	return false
}

func persistRemovalLifecycle(t *testing.T, owner *coordinatorOwner, record coordinatorJobRecord) {
	t.Helper()
	request := freshContainerRequest{
		Image: testAcceptedImageRecord(record.Plan).Image,
		Plan:  record.Plan, GateID: record.Plan.Gates[0].ID, SourceSnapshotDir: t.TempDir(),
		ContainerLabels: map[string]string{"job": record.JobID}, LifecycleHook: owner.lifecycleHook(
			record, record.Plan.Gates[0].ID, map[string]string{"job": record.JobID},
		),
	}
	if _, _, err := emitFakeContainerLifecycle(context.Background(), request, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}

func TestCleanupDeterministicRecoverySourceReclaimsPersistedSnapshot(t *testing.T) {
	home := t.TempDir()
	cacheHome := filepath.Join(home, "cache")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("LOCALAPPDATA", cacheHome)
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot := filepath.Join(cacheRoot, "super-dolphin", "localci")
	snapshot := filepath.Join(runtimeRoot, "jobs", "source-cleanup", "snapshot")
	if err := os.MkdirAll(snapshot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(snapshot, "materialized"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := coordinatorJobRecord{JobID: "source-cleanup", SourceSnapshotDir: snapshot}
	if err := cleanupDeterministicRecoverySource(record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(snapshot)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery source root remains after cleanup: %v", err)
	}
}

func TestRecoveredShardGroupUsesExactArgvAndCompletesAfterAllLive(t *testing.T) {
	runner := newRecoveryShardRunner()
	owner, record, admission := recoveredShardGroupFixture(t, runner)
	if err := owner.resumeRecoveredShardGroup(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if got := runner.recoveredRequests(); len(got) != gatecontract.MaxContainerShards {
		t.Fatalf("recovered containers = %d, want %d", len(got), gatecontract.MaxContainerShards)
	} else {
		for _, request := range got {
			identity := request.ContainerLabels[coordinatorLabelShardIdentity]
			shard := recoveryTestShard(t, record, identity)
			want, err := gatecontract.ContainerShardExecutorArgv(record.Plan, shard.Shard.GateIDs)
			if err != nil || !slices.Equal(request.Command, want) || !request.StartedAt.Equal(*record.StartedAt) ||
				!request.Deadline.Equal(*record.Deadline) {
				t.Fatalf("recovery request for %q drifted: %+v err=%v", identity, request, err)
			}
		}
	}
	assertRecoveredShardTerminal(t, owner, record.JobID, admission.WorkloadID, localci.WorkloadStatusPassed)
}

func TestRecoveredShardGroupFailureAndMissingEnterBarrierBeforeCleanup(t *testing.T) {
	for _, test := range []struct {
		name    string
		prepare func(*recoveryShardRunner, coordinatorJobRecord)
		want    localci.WorkloadStatus
	}{
		{name: "one exited failed", prepare: func(runner *recoveryShardRunner, record coordinatorJobRecord) {
			runner.failedIdentity = record.ContainerShards[1].Shard.IdentityDigest
		}, want: localci.WorkloadStatusFailed},
		{name: "one missing", prepare: func(runner *recoveryShardRunner, record coordinatorJobRecord) {
			runner.missingIdentity = record.ContainerShards[1].Shard.IdentityDigest
		}, want: localci.WorkloadStatusInfraFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := newRecoveryShardRunner()
			owner, record, admission := recoveredShardGroupFixture(t, runner)
			test.prepare(runner, record)
			if err := owner.resumeRecoveredShardGroup(context.Background(), record); err != nil {
				t.Fatal(err)
			}
			if runner.cleanupBeforeBarrier {
				t.Fatal("sibling cleanup ran before scheduler cancelling barrier")
			}
			assertRecoveredShardTerminal(t, owner, record.JobID, admission.WorkloadID, test.want)
		})
	}
}

func TestRecoveredShardCleanupFailureRetainsCancellingLease(t *testing.T) {
	runner := newRecoveryShardRunner()
	owner, record, admission := recoveredShardGroupFixture(t, runner)
	runner.cleanupFailureIdentity = record.ContainerShards[0].Shard.IdentityDigest
	runner.missingIdentity = record.ContainerShards[1].Shard.IdentityDigest
	if err := owner.resumeRecoveredShardGroup(context.Background(), record); err == nil {
		t.Fatal("cleanup failure released recovery shard group")
	}
	status, err := owner.schedulerClient.State(context.Background(), admission.WorkloadID)
	if err != nil || status != localci.WorkloadStatusCancelling {
		t.Fatalf("scheduler status=%q err=%v, want cancelling", status, err)
	}
	persisted, err := owner.store.job(context.Background(), record.JobID)
	if err != nil || persisted.State != jobStateStarted {
		t.Fatalf("job state=%q err=%v, want started", persisted.State, err)
	}
	if got := runner.cleanupCount(); got != gatecontract.MaxContainerShards {
		t.Fatalf("cleanup attempts=%d, want every shard=%d", got, gatecontract.MaxContainerShards)
	}
}

func TestRecoveredCreatingOrCreatedShardsWithoutJobClockResolveCleanup(t *testing.T) {
	for _, test := range []recoveredPreClockShardCleanupCase{
		{
			name:   "creating cleanup succeeds",
			phase:  localci.FreshContainerPhaseCreating,
			assert: assertRecoveredPreClockCleanupSucceeded,
		},
		{
			name:  "created container is absent",
			phase: localci.FreshContainerPhaseCreated,
			prepare: func(runner *recoveryShardRunner, record coordinatorJobRecord) {
				runner.absentIdentity = record.ContainerShards[1].Shard.IdentityDigest
			},
			assert: assertRecoveredPreClockCleanupSucceeded,
		},
		{
			name:  "created cleanup failure stays fail closed",
			phase: localci.FreshContainerPhaseCreated,
			prepare: func(runner *recoveryShardRunner, record coordinatorJobRecord) {
				runner.cleanupFailureIdentity = record.ContainerShards[1].Shard.IdentityDigest
			},
			assert: assertRecoveredPreClockCleanupFailedClosed,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runRecoveredPreClockShardCleanupCase(t, test)
		})
	}
}

type recoveredPreClockShardCleanupCase struct {
	name    string
	phase   localci.FreshContainerLifecyclePhase
	prepare func(*recoveryShardRunner, coordinatorJobRecord)
	assert  func(*testing.T, *coordinatorOwner, *recoveryShardRunner, coordinatorJobRecord, coordinatorShardAdmission, error)
}

func runRecoveredPreClockShardCleanupCase(t *testing.T, test recoveredPreClockShardCleanupCase) {
	t.Helper()
	runner := newRecoveryShardRunner()
	owner, record, admission := recoveredPreClockShardGroupFixture(t, runner, test.phase)
	if test.prepare != nil {
		test.prepare(runner, record)
	}

	err := owner.resumeRecoveredShardGroup(context.Background(), record)
	test.assert(t, owner, runner, record, admission, err)

	if got := runner.cleanupCount(); got != gatecontract.MaxContainerShards {
		t.Fatalf("cleanup attempts=%d, want every shard=%d", got, gatecontract.MaxContainerShards)
	}
}

func assertRecoveredPreClockCleanupSucceeded(
	t *testing.T,
	owner *coordinatorOwner,
	runner *recoveryShardRunner,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	err error,
) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if runner.cleanupBeforeBarrier {
		t.Fatal("cleanup ran before the cancelling barrier")
	}
	assertRecoveredShardTerminal(t, owner, record.JobID, admission.WorkloadID, localci.WorkloadStatusInfraFailed)
	stored, storeErr := owner.store.job(context.Background(), record.JobID)
	if storeErr != nil || stored.State != jobStateInfraFailed {
		t.Fatalf("durable state=%q err=%v, want infra_failed", stored.State, storeErr)
	}
}

func assertRecoveredPreClockCleanupFailedClosed(
	t *testing.T,
	owner *coordinatorOwner,
	_ *recoveryShardRunner,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
	err error,
) {
	t.Helper()
	if err == nil {
		t.Fatal("cleanup failure completed a shard group with no job clock")
	}
	status, stateErr := owner.schedulerClient.State(context.Background(), admission.WorkloadID)
	if stateErr != nil || status != localci.WorkloadStatusCancelling {
		t.Fatalf("scheduler status=%q err=%v, want cancelling", status, stateErr)
	}
	stored, storeErr := owner.store.job(context.Background(), record.JobID)
	if storeErr != nil || stored.State != jobStateStarted {
		t.Fatalf("durable state=%q err=%v, want started", stored.State, storeErr)
	}
	snapshot, snapshotErr := owner.schedulerClient.Snapshot(context.Background())
	if snapshotErr != nil || len(snapshot.Leases) != gatecontract.MaxContainerShards {
		t.Fatalf("cleanup failure leases=%#v err=%v", snapshot.Leases, snapshotErr)
	}
}

func TestRecoveredShardPostRunEvidenceFailureEntersBarrier(t *testing.T) {
	runner := newRecoveryShardRunner()
	owner, record, admission := recoveredShardGroupFixture(t, runner)
	runner.invalidGateEvidence = true
	if err := owner.resumeRecoveredShardGroup(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	assertRecoveredShardTerminal(t, owner, record.JobID, admission.WorkloadID, localci.WorkloadStatusFailed)
}

func TestRecoveredShardReceiptSigningFailureCleansBeforeInfraComplete(t *testing.T) {
	runner := newRecoveryShardRunner()
	runner.omitPassedRemovalLifecycle = true
	owner, record, admission := recoveredShardGroupFixture(t, runner)
	owner.dependencies.ReceiptSigner = failingResultReceiptSigner{}
	if err := owner.resumeRecoveredShardGroup(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	if runner.cleanupBeforeBarrier {
		t.Fatal("receipt signing compensation cleaned before scheduler cancelling barrier")
	}
	if got := runner.cleanupCount(); got != gatecontract.MaxContainerShards {
		t.Fatalf("receipt signing compensation cleanup count=%d want=%d", got, gatecontract.MaxContainerShards)
	}
	assertRecoveredShardTerminal(t, owner, record.JobID, admission.WorkloadID, localci.WorkloadStatusInfraFailed)
}

func TestRecoveredShardQueuedOutboxCrashWindowReenqueuesExactGroup(t *testing.T) {
	owner, runner, record, admission := queuedRecoveredShardGroupFixture(t)
	if err := owner.resumeRecoveredShardGroup(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	assertQueuedRecoveredShardGroup(t, owner, runner, record, admission)
}

func queuedRecoveredShardGroupFixture(
	t *testing.T,
) (*coordinatorOwner, *recoveryShardRunner, coordinatorJobRecord, coordinatorShardAdmission) {
	t.Helper()
	runner := newRecoveryShardRunner()
	owner := startDeadlineOwner(t, fakeImageEnsurer{}, immediateFreshRunner{})
	owner.dependencies.RecoveryRunner = runner
	record := prepareStartedDeadlineJob(t, owner)
	accepted := testAcceptedImageRecord(record.Plan)
	if err := owner.store.recordImageProvenance(context.Background(), record.JobID, accepted.SourceTree); err != nil {
		t.Fatal(err)
	}
	set, err := gatecontract.BuildContainerShardSet(record.Plan, accepted.Image.PlatformManifestDigest, accepted.Image.ConfigDigest)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := owner.store.prepareShardAdmission(context.Background(), record, set)
	if err != nil {
		t.Fatal(err)
	}
	record, err = owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	return owner, runner, record, admission
}

func assertQueuedRecoveredShardGroup(
	t *testing.T,
	owner *coordinatorOwner,
	runner *recoveryShardRunner,
	record coordinatorJobRecord,
	admission coordinatorShardAdmission,
) {
	t.Helper()
	status, err := owner.schedulerClient.State(context.Background(), admission.WorkloadID)
	if err != nil || status != localci.WorkloadStatusQueued {
		t.Fatalf("replayed shard outbox status=%q err=%v", status, err)
	}
	stored, err := owner.store.shardAdmission(context.Background(), record.JobID)
	if err != nil || stored.Phase != shardAdmissionEnqueued || len(runner.recoveredRequests()) != 0 {
		t.Fatalf("outbox replay admission=%+v recovered=%d err=%v", stored, len(runner.recoveredRequests()), err)
	}
}

func TestRecoveredShardGroupRetriesReportCleanupAndCompleteCrashWindows(t *testing.T) {
	runner := newRecoveryShardRunner()
	owner, record, admission := recoveredShardGroupFixture(t, runner)
	identity, err := shardFailureBarrierIdentity(admission)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.schedulerClient.ReportShardFailure(context.Background(), admission.WorkloadID, admission.GroupIdentity, identity); err != nil {
		t.Fatal(err)
	}
	if err := owner.resumeRecoveredShardGroup(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	assertRecoveredShardTerminal(t, owner, record.JobID, admission.WorkloadID, localci.WorkloadStatusInfraFailed)
	persisted, err := owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.completeRecoveredShardSchedulerGroup(context.Background(), admission, localci.WorkloadStatusInfraFailed); err != nil {
		t.Fatalf("CompleteGroup terminal retry failed: %v", err)
	}
	if err := owner.resumeRecoveredShardGroup(context.Background(), persisted); err != nil {
		t.Fatalf("terminal recovery reran completed group: %v", err)
	}
}

func TestShardFinalizationDeadlineRetainsGangLease(t *testing.T) {
	runner := newRecoveryShardRunner()
	owner, record, admission := recoveredShardGroupFixture(t, runner)
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()

	err := owner.completeFailedShardGroup(ctx, record, admission, receiptExecution{}, jobStateInfraFailed, errors.New("execution failed"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("completeFailedShardGroup() error = %v, want deadline exceeded", err)
	}
	status, err := owner.schedulerClient.State(context.Background(), admission.WorkloadID)
	if err != nil {
		t.Fatal(err)
	}
	if status != localci.WorkloadStatusStarted {
		t.Fatalf("timed out finalization changed workload status to %q", status)
	}
	snapshot, err := owner.schedulerClient.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Leases) != gatecontract.MaxContainerShards {
		t.Fatalf("timed out finalization released leases: %#v", snapshot.Leases)
	}
	stored, err := owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != jobStateStarted {
		t.Fatalf("timed out finalization completed job: %#v", stored)
	}
}

func recoveredShardGroupFixture(
	t *testing.T,
	runner *recoveryShardRunner,
) (*coordinatorOwner, coordinatorJobRecord, coordinatorShardAdmission) {
	t.Helper()
	owner := startDeadlineOwner(t, fakeImageEnsurer{}, immediateFreshRunner{})
	owner.dependencies.RecoveryRunner = runner
	record, set, admission := prepareRecoveredShardAdmissionFixture(t, owner)
	startRecoveredShardSchedulerGroup(t, owner, record, set, admission)
	record = persistRecoveredShardStartedLifecycles(t, owner, runner, record, set)
	runner.groupStarted, runner.groupDeadline = record.StartedAt.UTC(), record.Deadline.UTC()
	runner.barrierCheck = func() bool {
		status, err := owner.schedulerClient.State(context.Background(), admission.WorkloadID)
		return err == nil && status == localci.WorkloadStatusCancelling
	}
	return owner, record, admission
}

func recoveredPreClockShardGroupFixture(
	t *testing.T,
	runner *recoveryShardRunner,
	phase localci.FreshContainerLifecyclePhase,
) (*coordinatorOwner, coordinatorJobRecord, coordinatorShardAdmission) {
	t.Helper()
	owner, record, admission := recoveredShardGroupFixture(t, runner)
	if _, err := owner.store.db.ExecContext(context.Background(), `UPDATE coordinator_jobs
		SET started_at = NULL, deadline_at = NULL WHERE job_id = ?`, record.JobID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.store.db.ExecContext(context.Background(), `UPDATE coordinator_job_shards
		SET container_phase = ?, started_at = NULL, deadline_at = NULL WHERE job_id = ?`, string(phase), record.JobID); err != nil {
		t.Fatal(err)
	}
	loaded, err := owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StartedAt != nil || loaded.Deadline != nil {
		t.Fatalf("recovered job clock=%v/%v, want nil", loaded.StartedAt, loaded.Deadline)
	}
	for _, shard := range loaded.ContainerShards {
		if shard.ContainerPhase != phase {
			t.Fatalf("shard %q phase=%q, want %q", shard.Shard.IdentityDigest, shard.ContainerPhase, phase)
		}
	}
	return owner, loaded, admission
}

func prepareRecoveredShardAdmissionFixture(
	t *testing.T,
	owner *coordinatorOwner,
) (coordinatorJobRecord, gatecontract.ContainerShardSet, coordinatorShardAdmission) {
	t.Helper()
	record := prepareStartedDeadlineJob(t, owner)
	accepted := testAcceptedImageRecord(record.Plan)
	if err := owner.store.recordImageProvenance(context.Background(), record.JobID, accepted.SourceTree); err != nil {
		t.Fatal(err)
	}
	set, err := gatecontract.BuildContainerShardSet(record.Plan, accepted.Image.PlatformManifestDigest, accepted.Image.ConfigDigest)
	if err != nil {
		t.Fatal(err)
	}
	admission, err := owner.store.prepareShardAdmission(context.Background(), record, set)
	if err != nil {
		t.Fatal(err)
	}
	return record, set, admission
}

func startRecoveredShardSchedulerGroup(
	t *testing.T,
	owner *coordinatorOwner,
	record coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
	admission coordinatorShardAdmission,
) {
	t.Helper()
	_, request, err := shardAdmissionForSet(record, set)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.schedulerClient.Enqueue(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if err := owner.store.markShardAdmissionEnqueued(context.Background(), record.JobID); err != nil {
		t.Fatal(err)
	}
	if err := owner.schedulerClient.Complete(context.Background(), record.JobID, localci.WorkloadStatusPassed); err != nil {
		t.Fatal(err)
	}
	reservations, err := owner.schedulerClient.ReserveRunnable(context.Background())
	if err != nil || len(reservations) != 1 || reservations[0].WorkloadID != admission.WorkloadID {
		t.Fatalf("reserve shard recovery group=%+v err=%v", reservations, err)
	}
}

func persistRecoveredShardStartedLifecycles(
	t *testing.T,
	owner *coordinatorOwner,
	runner *recoveryShardRunner,
	record coordinatorJobRecord,
	set gatecontract.ContainerShardSet,
) coordinatorJobRecord {
	t.Helper()
	runtimeRoot, err := coordinatorRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	snapshotDir := filepath.Join(runtimeRoot, "jobs", record.JobID, "snapshot")
	if err := os.MkdirAll(snapshotDir, 0o700); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Truncate(time.Microsecond)
	deadline := started.Add(coordinatorTimeout(record.Profile))
	witness, witnessDigest := testContainerResourceWitness()
	for index, shard := range set.Shards {
		labels := owner.shardContainerLabels(record, shard)
		for _, phase := range []localci.FreshContainerLifecyclePhase{
			localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating,
			localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseStarting, localci.FreshContainerPhaseStarted,
		} {
			event := coordinatorShardLifecycleEvent(shard, phase, started.Add(time.Duration(index)*time.Microsecond), deadline, snapshotDir, witness, witnessDigest)
			if err := owner.store.recordContainerShardLifecycle(context.Background(), record.JobID, shard.IdentityDigest, labels, event); err != nil {
				t.Fatal(err)
			}
		}
		runner.gates[coordinatorShardLifecycleEvent(shard, localci.FreshContainerPhaseStarted, started, deadline, snapshotDir, witness, witnessDigest).ContainerID] = append([]gatecontract.GateID(nil), shard.GateIDs...)
	}
	record, err = owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

type recoveryShardRunner struct {
	mu                         sync.Mutex
	gates                      map[string][]gatecontract.GateID
	recovered                  []localci.FreshContainerRecoveryRequest
	cleanupBeforeBarrier       bool
	failedIdentity             string
	missingIdentity            string
	absentIdentity             string
	cleanupFailureIdentity     string
	cleanupRequests            []string
	invalidGateEvidence        bool
	omitPassedRemovalLifecycle bool
	groupStarted               time.Time
	groupDeadline              time.Time
	barrierCheck               func() bool
}

func newRecoveryShardRunner() *recoveryShardRunner {
	return &recoveryShardRunner{gates: make(map[string][]gatecontract.GateID)}
}

func (runner *recoveryShardRunner) ProbeFreshContainerRecovery(
	_ context.Context,
	request localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerRecoveryObservation, error) {
	if request.ContainerLabels[coordinatorLabelShardIdentity] == runner.missingIdentity {
		return localci.FreshContainerRecoveryObservation{}, errors.New("container missing")
	}
	return localci.FreshContainerRecoveryObservation{ContainerID: request.ContainerID, Status: "running"}, nil
}

func (runner *recoveryShardRunner) RecoverFreshContainer(
	ctx context.Context,
	request localci.FreshContainerRecoveryRequest,
) (localci.FreshContainerResult, error) {
	runner.mu.Lock()
	runner.recovered = append(runner.recovered, request)
	runner.mu.Unlock()
	identity := request.ContainerLabels[coordinatorLabelShardIdentity]
	status := gatecontract.ResultStatusPassed
	var runErr error
	if identity == runner.failedIdentity {
		status, runErr = gatecontract.ResultStatusFailed, errors.New("recovered shard failed")
	}
	result, err := runner.finish(ctx, request, status)
	return result, errors.Join(runErr, err)
}

func (runner *recoveryShardRunner) CleanupUnprovedFreshContainer(
	ctx context.Context,
	request localci.FreshContainerCleanupRequest,
) (localci.FreshContainerResult, error) {
	identity := request.ContainerLabels[coordinatorLabelShardIdentity]
	runner.mu.Lock()
	runner.cleanupRequests = append(runner.cleanupRequests, identity)
	if runner.barrierCheck == nil || !runner.barrierCheck() {
		runner.cleanupBeforeBarrier = true
	}
	runner.mu.Unlock()
	if identity == runner.cleanupFailureIdentity {
		return localci.FreshContainerResult{}, errors.New("cleanup failed")
	}
	witness, witnessDigest := testContainerResourceWitness()
	result := localci.FreshContainerResult{Status: gatecontract.ResultStatusInfraFailed,
		ImageReference: request.ImageReference, ExitCode: -1,
		Container: gatecontract.ContainerEvidence{ContainerID: request.ContainerID, Removed: true,
			HostConfigDigest: coordinatorDigest("c"), ResourceWitness: witness, ResourceWitnessDigest: witnessDigest},
		RemovalProofDigest: coordinatorDigest("f")}
	if identity != runner.absentIdentity {
		pending := localci.FreshContainerLifecycleEvent{Phase: localci.FreshContainerPhaseRemovalPending,
			ContainerID: request.ContainerID, ImageReference: request.ImageReference, ConfigDigest: request.ConfigDigest,
			HostConfigDigest: coordinatorDigest("c"), ResourceWitness: witness, ResourceWitnessDigest: witnessDigest,
			SourceSnapshotDir: request.SourceSnapshotDir}
		if err := request.LifecycleHook(context.WithoutCancel(ctx), pending); err != nil {
			return localci.FreshContainerResult{}, err
		}
	}
	removed := localci.FreshContainerLifecycleEvent{Phase: localci.FreshContainerPhaseRemoved,
		ContainerID: request.ContainerID, ImageReference: request.ImageReference, ConfigDigest: request.ConfigDigest,
		HostConfigDigest: coordinatorDigest("c"), ResourceWitness: witness, ResourceWitnessDigest: witnessDigest,
		SourceSnapshotDir: request.SourceSnapshotDir, RemovalProofDigest: result.RemovalProofDigest}
	if err := request.LifecycleHook(context.WithoutCancel(ctx), removed); err != nil {
		return localci.FreshContainerResult{}, err
	}
	return result, nil
}

func (runner *recoveryShardRunner) finish(
	ctx context.Context,
	request localci.FreshContainerRecoveryRequest,
	status gatecontract.ResultStatus,
) (localci.FreshContainerResult, error) {
	witness, witnessDigest := testContainerResourceWitness()
	exitedAt := request.StartedAt.Add(45 * time.Second)
	completed := request.StartedAt.Add(time.Minute)
	exited := localci.FreshContainerLifecycleEvent{Phase: localci.FreshContainerPhaseExited,
		ContainerID: request.ContainerID, ImageReference: request.ImageReference, ConfigDigest: request.ConfigDigest,
		HostConfigDigest: coordinatorDigest("c"), ResourceWitness: witness, ResourceWitnessDigest: witnessDigest,
		SourceSnapshotDir: request.SourceSnapshotDir, ExitedAt: exitedAt, CompletedAt: completed}
	if status != gatecontract.ResultStatusPassed {
		exited.ExitCode = 1
	}
	lifecycleCtx := context.WithoutCancel(ctx)
	if err := request.LifecycleHook(lifecycleCtx, exited); err != nil {
		return localci.FreshContainerResult{}, err
	}
	proof := coordinatorDigest("f")
	removed := exited
	removed.Phase, removed.RemovalProofDigest = localci.FreshContainerPhaseRemoved, proof
	if status != gatecontract.ResultStatusPassed || !runner.omitPassedRemovalLifecycle {
		if err := request.LifecycleHook(lifecycleCtx, removed); err != nil {
			return localci.FreshContainerResult{}, err
		}
	}
	logOutput := []byte("recovered shard\n")
	logDigest := coordinatorLogDigest(logOutput)
	gates := runner.gates[request.ContainerID]
	if runner.invalidGateEvidence {
		gates = append(gates, gatecontract.GateID("forged"))
	}
	results := make([]localci.FreshPlanGateResult, len(gates))
	for index, gateID := range gates {
		argvDigest, err := testCanonicalGateArgvDigest(gateID)
		if err != nil {
			return localci.FreshContainerResult{}, err
		}
		results[index] = localci.FreshPlanGateResult{Status: status, LogOutput: logOutput,
			GateResult: gatecontract.GateResult{GateID: string(gateID), Status: gateStatusForResult(status),
				ExitCode: exited.ExitCode, StartedAt: request.StartedAt, CompletedAt: completed,
				ArgvDigest: argvDigest, LogDigest: logDigest}}
	}
	container := gatecontract.ContainerEvidence{ContainerID: request.ContainerID, NetworkID: "network-" + request.ContainerID[:12],
		HostConfigDigest: coordinatorDigest("c"), ResourceWitness: witness, ResourceWitnessDigest: witnessDigest,
		NetworkPolicyDigest: coordinatorDigest("6"), Removed: true, NetworkRemoved: true}
	return localci.FreshContainerResult{Status: status, ImageReference: request.ImageReference, Container: container,
		PlanGateResults: results, ExitCode: exited.ExitCode, StartedAt: request.StartedAt, ExitedAt: exitedAt, CompletedAt: completed,
		Deadline: request.Deadline, LogOutput: logOutput, LogDigest: logDigest, RemovalProofDigest: proof}, nil
}

func (runner *recoveryShardRunner) recoveredRequests() []localci.FreshContainerRecoveryRequest {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return append([]localci.FreshContainerRecoveryRequest(nil), runner.recovered...)
}

func (runner *recoveryShardRunner) cleanupCount() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return len(runner.cleanupRequests)
}

func recoveryTestShard(t *testing.T, record coordinatorJobRecord, identity string) coordinatorShardRecord {
	t.Helper()
	for _, shard := range record.ContainerShards {
		if shard.Shard.IdentityDigest == identity {
			return shard
		}
	}
	t.Fatalf("unknown recovery shard %q", identity)
	return coordinatorShardRecord{}
}

func assertRecoveredShardTerminal(
	t *testing.T,
	owner *coordinatorOwner,
	jobID string,
	workloadID string,
	want localci.WorkloadStatus,
) {
	t.Helper()
	status, err := owner.schedulerClient.State(context.Background(), workloadID)
	if err != nil || status != want {
		t.Fatalf("scheduler status=%q err=%v, want %q", status, err, want)
	}
	snapshot, err := owner.schedulerClient.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if schedulerSnapshotHasWorkloadLease(snapshot, workloadID) {
		t.Fatal("terminal shard group retained lease")
	}
	record, err := owner.store.job(context.Background(), jobID)
	if err != nil || !record.State.terminal() {
		t.Fatalf("durable job state=%q err=%v, want terminal", record.State, err)
	}
}
