package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gatecontract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/localci"
	"golang.org/x/sync/errgroup"
)

type deadlineBlockingImageEnsurer struct{}

func (deadlineBlockingImageEnsurer) EnsureImage(ctx context.Context, _ imageEnsureRequest) (ensuredImage, error) {
	<-ctx.Done()
	return ensuredImage{}, ctx.Err()
}

func TestReservationWorkloadKindValidatesExactShardLeaseGroup(t *testing.T) {
	valid := localci.WorkloadReservation{WorkloadID: "job", GroupIdentity: "group", Leases: []localci.Lease{
		{ID: "job/shard", WorkloadID: "job", Kind: localci.WorkloadKindShard, GroupIdentity: "group", ShardIdentity: "one"},
		{ID: "job/service/1", WorkloadID: "job", Kind: localci.WorkloadKindService, GroupIdentity: "group", ShardIdentity: "two"},
		{ID: "job/service/2", WorkloadID: "job", Kind: localci.WorkloadKindService, GroupIdentity: "group", ShardIdentity: "three"},
	}}
	if kind, err := reservationWorkloadKind(valid); err != nil || kind != localci.WorkloadKindShard {
		t.Fatalf("valid shard reservation kind=%q err=%v", kind, err)
	}
	for _, mutate := range []func(*localci.WorkloadReservation){
		func(value *localci.WorkloadReservation) { value.Leases[1].GroupIdentity = "forged" },
		func(value *localci.WorkloadReservation) {
			value.Leases[2].ShardIdentity = value.Leases[1].ShardIdentity
		},
		func(value *localci.WorkloadReservation) {
			value.Leases[1], value.Leases[2] = value.Leases[2], value.Leases[1]
		},
		func(value *localci.WorkloadReservation) { value.Leases = value.Leases[:2] },
	} {
		candidate := valid
		candidate.Leases = append([]localci.Lease(nil), valid.Leases...)
		mutate(&candidate)
		if _, err := reservationWorkloadKind(candidate); err == nil {
			t.Fatal("reservationWorkloadKind() accepted forged shard lease group")
		}
	}
}

func TestShardReceiptExecutionPreservesObservedNetworkEvidence(t *testing.T) {
	container := gatecontract.ContainerEvidence{
		ContainerID: "container-id", NetworkID: "network-id", HostConfigDigest: coordinatorDigest("host"),
		ResourceWitness: localci.ExpectedFreshContainerResourceWitness(), ResourceWitnessDigest: coordinatorDigest("witness"),
		NetworkPolicyDigest: coordinatorDigest("network"), Removed: true, NetworkRemoved: true,
	}
	execution, err := shardReceiptExecution(gatecontract.AcceptedImageRecord{}, gatecontract.ContainerShardSet{}, []gatecontract.ContainerShardReceipt{{
		ContainerID: "container-id", Container: container,
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(execution.Containers) != 1 || execution.Containers[0] != container {
		t.Fatalf("shard receipt rewrote observed container evidence: %#v", execution.Containers)
	}
}

func TestPersistShardGateLogsRejectsGateOutsideShard(t *testing.T) {
	owner := &coordinatorOwner{}
	err := owner.persistShardGateLogs(context.Background(), "job", gatecontract.ContainerShard{
		IdentityDigest: "shard", GateIDs: []gatecontract.GateID{gatecontract.GateIDFrontendLint},
	}, localci.FreshContainerResult{PlanGateResults: []localci.FreshPlanGateResult{{
		GateResult: gatecontract.GateResult{GateID: string(gatecontract.GateIDFrontendTest)},
	}}})
	if err == nil {
		t.Fatal("persistShardGateLogs accepted a gate outside its shard identity")
	}
}

func TestCoordinatorLifecycleStartingUsesProfileExecutionDeadline(t *testing.T) {
	tests := []struct {
		name    string
		profile gatecontract.Profile
		budget  time.Duration
	}{
		{name: "normal", profile: gatecontract.ProfileLocalFast, budget: coordinatorNormalTimeout},
		{name: "release", profile: gatecontract.ProfileRelease, budget: coordinatorReleaseTimeout},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			started := time.Now().UTC()
			deadline := started.Add(test.budget)
			record := coordinatorJobRecord{State: jobStateStarted, Profile: test.profile}
			if err := validateLifecycleClock(record, localci.FreshContainerLifecycleEvent{
				Phase: localci.FreshContainerPhaseStarting, StartedAt: started, Deadline: deadline,
			}); err != nil {
				t.Fatalf("validateLifecycleClock() error = %v", err)
			}
		})
	}
}

func TestCoordinatorLifecycleStartingRejectsDriftedDeadline(t *testing.T) {
	started := time.Now().UTC()
	deadline := started.Add(coordinatorNormalTimeout)
	if err := validateLifecycleClock(
		coordinatorJobRecord{State: jobStateStarted, Profile: gatecontract.ProfileLocalFast},
		localci.FreshContainerLifecycleEvent{
			Phase: localci.FreshContainerPhaseStarting, StartedAt: started, Deadline: deadline.Add(time.Second),
		},
	); err == nil {
		t.Fatal("validateLifecycleClock() accepted drifted deadline")
	}
}

func TestCoordinatorStoreStartJobDefersExecutionClock(t *testing.T) {
	store := openActionGrantTestStore(t, filepath.Join(t.TempDir(), "coordinator.db"))
	defer store.db.Close()
	fixed := time.Date(2026, 7, 18, 3, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return fixed }
	authority := manualSubmissionAuthority()
	_, err := store.db.Exec(`INSERT INTO coordinator_jobs (
invocation_id, job_id, repository_root, entrypoint, authority_owner, authority_attestation,
plan_json, profile, job_source_tree_sha, state, submitted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"hook-"+coordinatorDigest("deadline"), "job-deadline", "/repo",
		authority.Entrypoint, authority.Owner, authority.Attestation, []byte(`{}`),
		gatecontract.ProfileRelease, coordinatorDigest("tree"), jobStateQueued, fixed.Add(-time.Minute).Format(timeFormat),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.startJob(context.Background(), "job-deadline"); err != nil {
		t.Fatalf("startJob() error = %v", err)
	}
	var state jobState
	var clockAbsent bool
	if err := store.db.QueryRow(`SELECT state, started_at IS NULL AND deadline_at IS NULL FROM coordinator_jobs WHERE job_id = ?`,
		"job-deadline").Scan(&state, &clockAbsent); err != nil {
		t.Fatal(err)
	}
	if state != jobStateStarted || !clockAbsent {
		t.Fatalf("persisted start = %q, clock absent %v", state, clockAbsent)
	}
}

func TestShardDeadlineClaimIsConcurrentAndDurable(t *testing.T) {
	store := openActionGrantTestStore(t, filepath.Join(t.TempDir(), "coordinator.db"))
	defer store.db.Close()
	store.db.SetMaxOpenConns(1)
	authority := manualSubmissionAuthority()
	if _, err := store.db.Exec(`INSERT INTO coordinator_jobs (
invocation_id, job_id, repository_root, entrypoint, authority_owner, authority_attestation,
plan_json, profile, job_source_tree_sha, state, submitted_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"deadline-claim", "deadline-claim-job", "/repo", authority.Entrypoint, authority.Owner, authority.Attestation,
		[]byte(`{}`), gatecontract.ProfileRelease, coordinatorDigest("tree"), jobStateQueued, time.Now().UTC().Format(timeFormat)); err != nil {
		t.Fatal(err)
	}
	if err := store.startJob(context.Background(), "deadline-claim-job"); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	deadlines := make(chan time.Time, 3)
	errs := make(chan error, 3)
	var workers sync.WaitGroup
	for range 3 {
		workers.Go(func() {
			deadline, err := store.claimShardExecutionDeadline(context.Background(), "deadline-claim-job", started)
			deadlines <- deadline
			errs <- err
		})
	}
	workers.Wait()
	close(deadlines)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first time.Time
	for deadline := range deadlines {
		if first.IsZero() {
			first = deadline
		} else if !deadline.Equal(first) {
			t.Fatalf("shard deadlines differ: %v and %v", first, deadline)
		}
	}
	if !first.Equal(started.Add(coordinatorReleaseTimeout)) {
		t.Fatalf("deadline = %v, want %v", first, started.Add(coordinatorReleaseTimeout))
	}
}

func TestCoordinatorProvisioningDeadlineIsInfraFailedWithoutExecutionClock(t *testing.T) {
	owner := startDeadlineOwner(t, deadlineBlockingImageEnsurer{}, immediateFreshRunner{})
	record := reserveDeadlineJob(t, owner, "provision-timeout")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := owner.executeJob(ctx, record.JobID); err != nil {
		t.Fatalf("executeJob() error = %v", err)
	}
	completed, err := owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != jobStateInfraFailed || completed.StartedAt != nil || completed.Deadline != nil {
		t.Fatalf("provision timeout state = %q, started %v, deadline %v", completed.State, completed.StartedAt, completed.Deadline)
	}
	if !strings.Contains(completed.Error, "provisioning timeout") || !strings.Contains(completed.Error, context.DeadlineExceeded.Error()) {
		t.Fatalf("provision timeout error = %q", completed.Error)
	}
}

func TestCoordinatorPreparationDefersClockAndContainerStartToShardReservation(t *testing.T) {
	runner := &deadlineCapturingRunner{}
	owner := startDeadlineOwner(t, fakeImageEnsurer{}, runner)
	record := reserveDeadlineJob(t, owner, "execution-clock")
	if err := owner.executeJob(context.Background(), record.JobID); err != nil {
		t.Fatalf("executeJob() error = %v", err)
	}
	assertCoordinatorPreparationDidNotStartContainer(t, runner.request)
	assertCoordinatorPreparationDefersExecutionClock(t, owner, record)
}

func assertCoordinatorPreparationDidNotStartContainer(t *testing.T, request freshContainerRequest) {
	t.Helper()
	if !request.Deadline.IsZero() || request.PlanExecution || len(request.ShardGateIDs) != 0 {
		t.Fatalf("preparation unexpectedly started a container: %#v", request)
	}
}

func assertCoordinatorPreparationDefersExecutionClock(t *testing.T, owner *coordinatorOwner, record coordinatorJobRecord) {
	t.Helper()
	completed, err := owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.StartedAt != nil || completed.Deadline != nil || len(completed.ContainerShards) != 3 {
		t.Fatalf("preparation state = start %v deadline %v shards %d", completed.StartedAt, completed.Deadline, len(completed.ContainerShards))
	}
	assertCoordinatorPreparationShardsHaveNoContainerEvidence(t, completed.ContainerShards)
}

func assertCoordinatorPreparationShardsHaveNoContainerEvidence(t *testing.T, shards []coordinatorShardRecord) {
	t.Helper()
	for _, shard := range shards {
		if shard.ContainerPhase != "" || shard.ContainerID != "" {
			t.Fatalf("preparation wrote container evidence: %#v", shard)
		}
	}
}

func TestCoordinatorRecoveryClockMatrix(t *testing.T) {
	owner := &coordinatorOwner{daemonIdentityKey: "daemon"}
	for _, phase := range []localci.FreshContainerLifecyclePhase{"", localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating, localci.FreshContainerPhaseCreated} {
		record := coordinatorJobRecord{State: jobStateStarted, Profile: gatecontract.ProfileLocalFast, ContainerPhase: phase}
		if err := validateCoordinatorClock(record); err != nil {
			t.Fatalf("pre-execution phase %q clock validation = %v", phase, err)
		}
		if owner.recoveryRecordObservable(record) {
			t.Fatalf("pre-execution phase %q was observable", phase)
		}
	}
	if err := validateCoordinatorClock(coordinatorJobRecord{
		State: jobStateStarted, Profile: gatecontract.ProfileLocalFast,
		ContainerPhase: localci.FreshContainerPhaseStarting,
	}); err == nil {
		t.Fatal("Starting recovery accepted a missing execution clock")
	}
	startedAt := time.Now().UTC()
	deadline := startedAt.Add(coordinatorNormalTimeout)
	plan := mustTestGatePlan(t, "f")
	record := coordinatorJobRecord{
		State: jobStateStarted, Profile: plan.Profile, Plan: plan,
		StartedAt: &startedAt, Deadline: &deadline, ActiveGateID: plan.Gates[0].ID,
		ContainerPhase: localci.FreshContainerPhaseStarting, ContainerID: strings.Repeat("a", 64),
		ContainerImageReference: "test@" + coordinatorDigest("1"), ContainerConfigDigest: coordinatorDigest("2"),
		SourceSnapshotDir: t.TempDir(), ContainerLabels: map[string]string{"daemon": "identity"},
	}
	request, err := owner.recoveryRequest(record)
	if err != nil {
		t.Fatalf("recoveryRequest() error = %v", err)
	}
	if !request.StartedAt.Equal(startedAt) || !request.Deadline.Equal(deadline) {
		t.Fatalf("recovery recomputed clock = %v, %v", request.StartedAt, request.Deadline)
	}
}

type deadlineCapturingRunner struct {
	request freshContainerRequest
}

func (runner *deadlineCapturingRunner) RunFreshContainer(
	ctx context.Context,
	request freshContainerRequest,
) (localci.FreshContainerResult, error) {
	runner.request = request
	return successfulFreshContainerResult(ctx, request)
}

func TestCoordinatorDeadlineCleanupPersistsTimeoutAndGateLog(t *testing.T) {
	owner := startDeadlineCleanupOwner(t)
	record := prepareStartedDeadlineJob(t, owner)
	expired, gateID, logData, logDigest := persistDeadlineTimeout(t, owner, record)
	assertDeadlineJobTimedOut(t, owner, record)
	assertDeadlineLogPersisted(t, owner, record, gateID, logData, logDigest)
	assertDeadlineSchedulerCompleted(t, owner, record)
	assertBoundedCleanupContext(t, expired)
}

func persistDeadlineTimeout(
	t *testing.T,
	owner *coordinatorOwner,
	record coordinatorJobRecord,
) (context.Context, gatecontract.GateID, []byte, string) {
	t.Helper()
	logData := []byte("gate exceeded the durable normal deadline\n")
	logDigest := coordinatorLogDigest(logData)
	gateID := record.Plan.Gates[0].ID
	execution, observed := persistDeadlineTimeoutLifecycle(t, owner.store, record, gateID, logData, logDigest)
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := owner.persistObservedGateLog(expired, record.JobID, gateID, observed); err != nil {
		t.Fatalf("persistObservedGateLog() after deadline error = %v", err)
	}
	if err := owner.completeExecution(
		expired, record, execution, jobStateTimeout, context.DeadlineExceeded,
	); err != nil {
		t.Fatalf("completeExecution() after deadline error = %v", err)
	}
	return expired, gateID, logData, logDigest
}

func persistDeadlineTimeoutLifecycle(
	t *testing.T,
	store *coordinatorStore,
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	logData []byte,
	logDigest string,
) (receiptExecution, localci.FreshContainerResult) {
	t.Helper()
	startedAt := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	deadline := startedAt.Add(coordinatorTimeout(record.Profile))
	exitedAt := deadline.Add(time.Nanosecond)
	completedAt := exitedAt.Add(time.Nanosecond)
	removalProof := coordinatorDigest("deadline-timeout-removal")
	for _, phase := range []localci.FreshContainerLifecyclePhase{
		localci.FreshContainerPhasePrepared, localci.FreshContainerPhaseCreating,
		localci.FreshContainerPhaseCreated, localci.FreshContainerPhaseStarting,
		localci.FreshContainerPhaseStarted, localci.FreshContainerPhaseExited,
		localci.FreshContainerPhaseRemoved,
	} {
		event := missingExitLifecycleEvent(startedAt, deadline, phase)
		event.ExitCode = 137
		if phase == localci.FreshContainerPhaseExited || phase == localci.FreshContainerPhaseRemoved {
			event.ExitedAt, event.CompletedAt = exitedAt, completedAt
		}
		if phase == localci.FreshContainerPhaseRemoved {
			event.RemovalProofDigest = removalProof
		}
		if err := store.recordContainerLifecycle(
			context.Background(), record.JobID, gateID, legacyLifecycleLabels(record), event,
		); err != nil {
			t.Fatalf("record deadline timeout lifecycle %q: %v", phase, err)
		}
	}
	result := gatecontract.GateResult{
		GateID: string(gateID), Status: gatecontract.GateStatusTimeout, ExitCode: 137,
		StartedAt: startedAt, CompletedAt: completedAt,
		ArgvDigest: coordinatorDigest("deadline-timeout-argv"), LogDigest: logDigest,
	}
	observed := localci.FreshContainerResult{
		Status: gatecontract.ResultStatusTimeout, ExitCode: 137, GateResult: &result,
		StartedAt: startedAt, ExitedAt: exitedAt, CompletedAt: completedAt, Deadline: deadline,
		LogOutput: append([]byte(nil), logData...), LogDigest: logDigest,
		RemovalProofDigest: removalProof,
	}
	execution := receiptExecution{
		Results:           []gatecontract.GateResult{result},
		ContainerObserved: true, ContainerRemovalProven: true,
		ContainerRemovalProofDigest: removalProof,
		StartedAt:                   startedAt, CompletedAt: completedAt, Deadline: deadline,
	}
	return execution, observed
}

func assertDeadlineJobTimedOut(t *testing.T, owner *coordinatorOwner, record coordinatorJobRecord) {
	t.Helper()
	completed, err := owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != jobStateTimeout || completed.Error != context.DeadlineExceeded.Error() {
		t.Fatalf("completed job = state %q error %q", completed.State, completed.Error)
	}
}

func assertDeadlineLogPersisted(
	t *testing.T,
	owner *coordinatorOwner,
	record coordinatorJobRecord,
	gateID gatecontract.GateID,
	logData []byte,
	logDigest string,
) {
	t.Helper()
	persisted, err := owner.store.gateLog(context.Background(), record.JobID, gateID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.LogDigest != logDigest || persisted.Log != string(logData) {
		t.Fatalf("persisted gate log = %#v", persisted)
	}
}

func assertDeadlineSchedulerCompleted(t *testing.T, owner *coordinatorOwner, record coordinatorJobRecord) {
	t.Helper()
	schedulerState, err := owner.schedulerClient.State(context.Background(), record.JobID)
	if err != nil || schedulerState != localci.WorkloadStatusFailed {
		t.Fatalf("scheduler state = %q, %v", schedulerState, err)
	}
}

func assertBoundedCleanupContext(t *testing.T, expired context.Context) {
	t.Helper()
	cleanupCtx, cleanupCancel := coordinatorCleanupContext(expired)
	defer cleanupCancel()
	deadline, ok := cleanupCtx.Deadline()
	if !ok || time.Until(deadline) <= 0 || time.Until(deadline) > coordinatorCleanupTimeout {
		t.Fatalf("cleanup deadline = %v, %v", deadline, ok)
	}
}

func startDeadlineCleanupOwner(t *testing.T) *coordinatorOwner {
	t.Helper()
	return startDeadlineOwner(t, fakeImageEnsurer{}, immediateFreshRunner{})
}

func startDeadlineOwner(t *testing.T, imageEnsurer ImageEnsurer, runner FreshContainerRunner) *coordinatorOwner {
	t.Helper()
	owner, err := openCoordinatorOwner(context.Background(), coordinatorTestCheckpoint(t), coordinatorDependencies{
		ImageEnsurer: imageEnsurer, CandidateBuilder: fakeCandidateBuildService{},
		PromotionWatcher: fakePromotionWatcher{}, SourceMaterializer: fakeSourceMaterializer{},
		FreshRunner: runner, RecoveryRunner: &capturingFreshContainerRunner{},
		ReceiptSigner: mustTestResultReceiptSigner(t),
	})
	if err != nil {
		t.Fatalf("openCoordinatorOwner() error = %v", err)
	}
	serveCtx, stopServe := context.WithCancel(context.Background())
	group := errgroup.Group{}
	group.Go(func() error { return owner.schedulerOwner.Serve(serveCtx) })
	t.Cleanup(func() {
		stopServe()
		serveErr := group.Wait()
		if errors.Is(serveErr, context.Canceled) {
			serveErr = nil
		}
		if err := errors.Join(serveErr, owner.Close()); err != nil {
			t.Errorf("close deadline cleanup owner: %v", err)
		}
	})
	return owner
}

func prepareStartedDeadlineJob(t *testing.T, owner *coordinatorOwner) coordinatorJobRecord {
	t.Helper()
	record := reserveDeadlineJob(t, owner, "timeout-cleanup")
	if err := owner.store.startJob(context.Background(), record.JobID); err != nil {
		t.Fatal(err)
	}
	record, err := owner.store.job(context.Background(), record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func reserveDeadlineJob(t *testing.T, owner *coordinatorOwner, suffix string) coordinatorJobRecord {
	t.Helper()
	record, err := owner.store.createJob(
		context.Background(), "inv-"+suffix, "job-"+suffix, t.TempDir(),
		mustTestGatePlan(t, "f"), localci.PromotionCandidatePlan{}, manualSubmissionAuthority(),
	)
	if err != nil {
		t.Fatal(err)
	}
	request := localci.WorkloadRequest{
		ID: record.JobID, InvocationID: record.InvocationID, EnqueueSequence: record.EnqueueSequence,
		Subsequence: record.SchedulerSubsequence, Kind: localci.WorkloadKindJob,
		Dependencies: append([]string(nil), record.SchedulerDependencies...),
	}
	if err := owner.schedulerClient.Enqueue(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	reservations, err := owner.schedulerClient.ReserveRunnable(context.Background())
	if err != nil || len(reservations) != 1 {
		t.Fatalf("ReserveRunnable() = %#v, %v", reservations, err)
	}
	return record
}

func TestJoinOwnerErrorsKeepsPrimaryAndDropsCompanionCancellation(t *testing.T) {
	primary := errors.New("scheduler persistence failed")
	result := joinOwnerErrors(context.Background(), primary, errors.Join(context.Canceled))
	if !errors.Is(result, primary) {
		t.Fatalf("joinOwnerErrors() lost primary error: %v", result)
	}
	if errors.Is(result, context.Canceled) {
		t.Fatalf("joinOwnerErrors() retained companion cancellation: %v", result)
	}
	standalone := joinOwnerErrors(context.Background(), errors.Join(context.Canceled))
	if !errors.Is(standalone, context.Canceled) {
		t.Fatalf("joinOwnerErrors() dropped standalone cancellation: %v", standalone)
	}
}

func TestJoinOwnerErrorsDropsSchedulerClosedOnlyDuringShutdown(t *testing.T) {
	shutdownCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if result := joinOwnerErrors(shutdownCtx, errors.Join(localci.ErrSchedulerClosed)); result != nil {
		t.Fatalf("joinOwnerErrors() retained scheduler close during shutdown: %v", result)
	}
	standalone := joinOwnerErrors(context.Background(), errors.Join(localci.ErrSchedulerClosed))
	if !errors.Is(standalone, localci.ErrSchedulerClosed) {
		t.Fatalf("joinOwnerErrors() dropped scheduler close without shutdown: %v", standalone)
	}
}
