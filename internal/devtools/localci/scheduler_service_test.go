//go:build unix

package localci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sync/errgroup"
)

func TestSchedulerServiceSingletonCloseAndReopen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := newSchedulerServiceConfig(t, "singleton")
	first := mustOpenSchedulerService(t, ctx, config)
	alias := config
	alias.Endpoint = "unix:///var/run/../run/docker.sock"
	if _, err := openSchedulerWithRuntimeRoot(ctx, alias.SchedulerConfig, alias.runtimeRoot); !errors.Is(err, ErrSchedulerOwned) {
		t.Fatalf("second open error=%v want=%v", err, ErrSchedulerOwned)
	}
	assertSchedulerAliasPaths(t, config, alias)
	different := config
	different.DaemonID = config.DaemonID + "-other"
	independent := mustOpenSchedulerService(t, ctx, different)
	closeSchedulerService(t, independent)
	if err := first.Close(); err != nil {
		t.Fatalf("close first scheduler: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	reopened := mustOpenSchedulerService(t, ctx, config)
	closeSchedulerService(t, reopened)
}

func TestSchedulerServiceEnqueueReserveCompleteCrashReopen(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := newSchedulerServiceConfig(t, "crash-reopen")
	scheduler := mustOpenSchedulerService(t, ctx, config)
	mustServiceEnqueue(t, scheduler, WorkloadRequest{
		ID: "build", InvocationID: "inv-1", EnqueueSequence: 1, Kind: WorkloadKindBuild,
	})
	mustServiceEnqueue(t, scheduler, WorkloadRequest{
		ID: "job", InvocationID: "inv-1", EnqueueSequence: 2, Kind: WorkloadKindJob,
		Dependencies: []string{"build"},
	})
	assertSingleReservation(t, scheduler, "build")
	closeSchedulerService(t, scheduler)

	scheduler = mustOpenSchedulerService(t, ctx, config)
	assertSnapshotCounts(t, scheduler, 2, 1)
	assertServiceState(t, scheduler, "build", WorkloadStatusStarted)
	assertServiceState(t, scheduler, "job", WorkloadStatusQueued)
	if err := scheduler.Complete(ctx, "build", WorkloadStatusPassed); err != nil {
		t.Fatalf("complete build: %v", err)
	}
	assertSingleReservation(t, scheduler, "job")
	closeSchedulerService(t, scheduler)

	scheduler = mustOpenSchedulerService(t, ctx, config)
	closeSchedulerService(t, scheduler)
	assertServiceStateAfterReopen(t, ctx, config, "build", WorkloadStatusPassed)
}

func TestSchedulerServicePersistenceFailureDoesNotCommitMemory(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := newSchedulerServiceConfig(t, "save-failure")
	scheduler := mustOpenSchedulerService(t, ctx, config)
	injected := errors.New("injected scheduler save failure")
	scheduler.mu.Lock()
	scheduler.saveKernel = func(context.Context, *schedulerKernel) error { return injected }
	scheduler.mu.Unlock()
	err := scheduler.Enqueue(ctx, WorkloadRequest{
		ID: "not-committed", InvocationID: "inv-1", EnqueueSequence: 1, Kind: WorkloadKindJob,
	})
	if !errors.Is(err, ErrSchedulerPersistence) || !errors.Is(err, injected) {
		t.Fatalf("enqueue error=%v want persistence and injected errors", err)
	}
	if _, err := scheduler.State("not-committed"); !errors.Is(err, ErrWorkloadNotFound) {
		t.Fatalf("state after failed save error=%v want=%v", err, ErrWorkloadNotFound)
	}
	closeSchedulerService(t, scheduler)
	scheduler = mustOpenSchedulerService(t, ctx, config)
	defer closeSchedulerService(t, scheduler)
	if _, err := scheduler.State("not-committed"); !errors.Is(err, ErrWorkloadNotFound) {
		t.Fatalf("reopened state error=%v want=%v", err, ErrWorkloadNotFound)
	}
}

func TestSchedulerServiceThreeSlotsFourthQueuedAndConcurrentCalls(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	config := newSchedulerServiceConfig(t, "concurrent")
	scheduler := mustOpenSchedulerService(t, ctx, config)
	defer closeSchedulerService(t, scheduler)

	group, groupContext := errgroup.WithContext(ctx)
	for index := 1; index <= 4; index++ {
		index := index
		group.Go(func() error {
			return scheduler.Enqueue(groupContext, WorkloadRequest{
				ID: fmt.Sprintf("job-%d", index), InvocationID: fmt.Sprintf("inv-%d", index),
				EnqueueSequence: uint64(index), Kind: WorkloadKindJob,
			})
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatalf("concurrent enqueue: %v", err)
	}
	reservations, err := scheduler.ReserveRunnable(ctx)
	if err != nil {
		t.Fatalf("reserve runnable: %v", err)
	}
	if len(reservations) != 3 {
		t.Fatalf("reservations=%d want=3", len(reservations))
	}
	assertServiceState(t, scheduler, "job-4", WorkloadStatusQueued)
}

func TestSchedulerCompleteRejectsChangedStatePathBeforeLeaseMutation(t *testing.T) {
	t.Parallel()
	assertSchedulerCompletionRejectsChangedStatePath(t, "remove-recreate", removeAndRecreateSchedulerStatePath)
}

func TestSchedulerCompleteRejectsSymlinkedStatePathBeforeLeaseMutation(t *testing.T) {
	t.Parallel()
	assertSchedulerCompletionRejectsChangedStatePath(t, "replace-symlink", replaceSchedulerStatePathWithSymlink)
}

func assertSchedulerCompletionRejectsChangedStatePath(t *testing.T, name string, changePath func(*testing.T, string)) {
	t.Helper()
	ctx := context.Background()
	config := newSchedulerServiceConfig(t, name)
	identity, err := newDaemonIdentity(config.Endpoint, config.TLSFingerprint, config.DaemonID, config.OwnerUID)
	if err != nil {
		t.Fatalf("derive scheduler identity: %v", err)
	}
	_, statePath, err := deriveSchedulerRuntimePaths(config.runtimeRoot, identity)
	if err != nil {
		t.Fatalf("derive scheduler state path: %v", err)
	}
	scheduler := mustOpenSchedulerService(t, ctx, config)
	defer closeSchedulerService(t, scheduler)
	mustServiceEnqueue(t, scheduler, WorkloadRequest{
		ID: "job", InvocationID: "inv-1", EnqueueSequence: 1, Kind: WorkloadKindJob,
	})
	assertSingleReservation(t, scheduler, "job")

	changePath(t, statePath)
	err = scheduler.Complete(ctx, "job", WorkloadStatusPassed)
	if !errors.Is(err, ErrSchedulerPersistence) {
		t.Fatalf("Complete() error=%v, want scheduler persistence failure", err)
	}
	if !strings.Contains(err.Error(), "scheduler state path identity changed") {
		t.Errorf("Complete() error=%v, want explicit state path identity failure before SQLite mutation", err)
	}
	assertSchedulerStateUnchangedAfterRejectedCompletion(t, scheduler)
}

func removeAndRecreateSchedulerStatePath(t *testing.T, statePath string) {
	t.Helper()
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("remove live scheduler state path: %v", err)
	}
	if err := os.WriteFile(statePath, nil, privateSchedulerFileMode); err != nil {
		t.Fatalf("replace scheduler state path: %v", err)
	}
}

func replaceSchedulerStatePathWithSymlink(t *testing.T, statePath string) {
	t.Helper()
	targetPath := statePath + ".replacement"
	if err := os.WriteFile(targetPath, nil, privateSchedulerFileMode); err != nil {
		t.Fatalf("write scheduler symlink target: %v", err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatalf("remove live scheduler state path: %v", err)
	}
	if err := os.Symlink(targetPath, statePath); err != nil {
		t.Fatalf("replace scheduler state path with symlink: %v", err)
	}
}

func assertSchedulerStateUnchangedAfterRejectedCompletion(t *testing.T, scheduler *Scheduler) {
	t.Helper()
	snapshot, err := scheduler.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() after rejected completion: %v", err)
	}
	if len(snapshot.Workloads) != 1 || snapshot.Workloads[0].Status != WorkloadStatusStarted || len(snapshot.Leases) != 1 {
		t.Fatalf("snapshot after rejected completion = %#v, want started workload with retained lease", snapshot)
	}
}

func TestSchedulerServiceRejectsUnknownAndDuplicateInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "validation"))
	defer closeSchedulerService(t, scheduler)
	cases := []WorkloadRequest{
		{InvocationID: "inv", EnqueueSequence: 1, Kind: WorkloadKindJob},
		{ID: "job", InvocationID: "inv", EnqueueSequence: 1, Kind: "unknown"},
		{ID: "shard", InvocationID: "inv", EnqueueSequence: 1, Kind: WorkloadKindShard},
		{ID: "job", InvocationID: "inv", EnqueueSequence: 1, Kind: WorkloadKindJob, Dependencies: []string{"dep", "dep"}},
		{ID: "job", InvocationID: "inv", EnqueueSequence: 1, Kind: WorkloadKindJob, Dependencies: []string{"missing"}},
	}
	for _, request := range cases {
		if err := scheduler.Enqueue(ctx, request); !errors.Is(err, ErrInvalidSchedulerInput) {
			t.Fatalf("enqueue request=%+v error=%v want=%v", request, err, ErrInvalidSchedulerInput)
		}
	}
	if err := scheduler.Complete(ctx, "job", "unknown"); !errors.Is(err, ErrInvalidSchedulerInput) {
		t.Fatalf("unknown completion error=%v want=%v", err, ErrInvalidSchedulerInput)
	}
}

func TestWorkloadRequestMapsEveryFieldAndCopiesDependencies(t *testing.T) {
	t.Parallel()
	request := WorkloadRequest{
		ID: "job", InvocationID: "inv", EnqueueSequence: 7, Subsequence: 3,
		Kind: WorkloadKindShard, ServiceCount: 1, GroupIdentity: "group", GroupSize: 2,
		ShardIdentities: []string{"shard-0", "shard-1"}, Dependencies: []string{"dependency"},
	}
	spec, err := request.toSpec()
	if err != nil {
		t.Fatalf("map request: %v", err)
	}
	assertMappedWorkloadSpec(t, spec)
	request.Dependencies[0] = "mutated"
	request.ShardIdentities[0] = "mutated"
	if spec.dependencies[0] != "dependency" || spec.shardIDs[0] != "shard-0" {
		t.Fatalf("mapped slices share request backing storage: %+v", spec)
	}
}

func assertMappedWorkloadSpec(t *testing.T, spec workloadSpec) {
	t.Helper()
	want := workloadSpec{
		id: "job", invocationID: "inv", enqueueSeq: 7, subSeq: 3,
		kind: workloadShard, serviceCount: 1, groupID: "group", groupSize: 2,
		shardIDs: []string{"shard-0", "shard-1"}, dependencies: []workloadID{"dependency"},
	}
	if !reflect.DeepEqual(spec, want) {
		t.Fatalf("mapped spec=%+v want=%+v", spec, want)
	}
}

func TestSchedulerServiceGangAndSnapshotAreDeepCopies(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "gang-copy"))
	defer closeSchedulerService(t, scheduler)
	mustServiceEnqueue(t, scheduler, WorkloadRequest{
		ID: "gang", InvocationID: "inv-gang", EnqueueSequence: 1, Kind: WorkloadKindJob, ServiceCount: 2,
	})
	mustServiceEnqueue(t, scheduler, WorkloadRequest{
		ID: "later", InvocationID: "inv-later", EnqueueSequence: 2, Kind: WorkloadKindJob,
	})
	reservations, err := scheduler.ReserveRunnable(ctx)
	if err != nil {
		t.Fatalf("reserve gang: %v", err)
	}
	if len(reservations) != 1 || len(reservations[0].Leases) != 3 {
		t.Fatalf("gang reservations=%+v want one atomic three-slot reservation", reservations)
	}
	assertServiceState(t, scheduler, "later", WorkloadStatusQueued)
	snapshot, err := scheduler.Snapshot()
	if err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	reservations[0].Leases[0].ID = "mutated"
	snapshot.Workloads[0].Request.ID = "mutated"
	snapshot.Leases[0].ID = "mutated"
	second, err := scheduler.Snapshot()
	if err != nil {
		t.Fatalf("second snapshot: %v", err)
	}
	if second.Workloads[0].Request.ID != "gang" || second.Leases[0].ID == "mutated" {
		t.Fatalf("snapshot shares mutable storage: %+v", second)
	}
}

func TestSchedulerServiceShardGroupFailureBarrierRecoversAndCompletes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	config := newSchedulerServiceConfig(t, "group-recovery")
	scheduler := mustOpenSchedulerService(t, ctx, config)
	request := testServiceShardGroupRequest()
	startFailingServiceShardGroup(t, ctx, scheduler, request)
	closeSchedulerService(t, scheduler)
	assertRecoveredServiceShardGroup(t, ctx, config, request)
}

func TestSchedulerServiceReconcileRecoveryPreservesCancellingShardGroup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "reconcile-cancelling-group"))
	defer closeSchedulerService(t, scheduler)
	request := testServiceShardGroupRequest()
	startFailingServiceShardGroup(t, ctx, scheduler, request)
	if err := scheduler.ReconcileRecovery(ctx, []RecoveryWorkload{{Request: request, Status: WorkloadStatusCancelling}}); err != nil {
		t.Fatalf("reconcile cancelling shard group: %v", err)
	}
	assertServiceState(t, scheduler, request.ID, WorkloadStatusCancelling)
	assertSnapshotCounts(t, scheduler, 1, 3)
}

func TestSchedulerServiceReconcileRecoveryRejectsStartedToCancellingPromotion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "reject-started-cancelling"))
	defer closeSchedulerService(t, scheduler)
	request := testServiceShardGroupRequest()
	mustServiceEnqueue(t, scheduler, request)
	assertSingleReservation(t, scheduler, request.ID)
	if err := scheduler.ReconcileRecovery(ctx, []RecoveryWorkload{{Request: request, Status: WorkloadStatusCancelling}}); err == nil {
		t.Fatal("reconcile promoted started shard group to cancelling")
	}
	assertServiceState(t, scheduler, request.ID, WorkloadStatusStarted)
	assertSnapshotCounts(t, scheduler, 1, 3)
}

func TestSchedulerServiceReconcileRecoveryRejectsOrphanShardGroup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "reject-orphan-shard-group"))
	defer closeSchedulerService(t, scheduler)
	request := testServiceShardGroupRequest()
	mustServiceEnqueue(t, scheduler, request)
	if err := scheduler.ReconcileRecovery(ctx, nil); err == nil {
		t.Fatal("reconcile accepted orphan shard group")
	}
	assertServiceState(t, scheduler, request.ID, WorkloadStatusQueued)
}

func TestSchedulerServiceReconcileRecoveryRestoresBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "recover-build"))
	defer closeSchedulerService(t, scheduler)
	request := WorkloadRequest{ID: "build", InvocationID: "invocation", EnqueueSequence: 1, Kind: WorkloadKindBuild}
	if err := scheduler.ReconcileRecovery(ctx, []RecoveryWorkload{{Request: request, Status: WorkloadStatusQueued}}); err != nil {
		t.Fatalf("reconcile build: %v", err)
	}
	assertServiceState(t, scheduler, request.ID, WorkloadStatusQueued)
}

func TestSchedulerServiceReconcileRecoveryRejectsService(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "reject-recovery-service"))
	defer closeSchedulerService(t, scheduler)
	request := WorkloadRequest{ID: "unsupported", InvocationID: "invocation", EnqueueSequence: 1, Kind: WorkloadKindService}
	err := scheduler.ReconcileRecovery(ctx, []RecoveryWorkload{{Request: request, Status: WorkloadStatusQueued}})
	if !errors.Is(err, ErrInvalidSchedulerInput) {
		t.Fatalf("reconcile service error=%v want=%v", err, ErrInvalidSchedulerInput)
	}
	if _, err := scheduler.State(request.ID); !errors.Is(err, ErrWorkloadNotFound) {
		t.Fatalf("rejected service recovery committed state: %v", err)
	}
}

func testServiceShardGroupRequest() WorkloadRequest {
	return WorkloadRequest{
		ID: "group", InvocationID: "invocation", EnqueueSequence: 1,
		Kind: WorkloadKindShard, ServiceCount: 2,
		GroupIdentity: "group-identity", GroupSize: 3,
		ShardIdentities: []string{"shard-0", "shard-1", "shard-2"},
		Dependencies:    []string{},
	}
}

func startFailingServiceShardGroup(
	t *testing.T,
	ctx context.Context,
	scheduler *Scheduler,
	request WorkloadRequest,
) {
	t.Helper()
	mustServiceEnqueue(t, scheduler, request)
	reservations, err := scheduler.ReserveRunnable(ctx)
	if err != nil {
		t.Fatalf("reserve shard group: %v", err)
	}
	if len(reservations) != 1 || reservations[0].GroupIdentity != request.GroupIdentity {
		t.Fatalf("reservation group binding=%+v", reservations)
	}
	siblings, err := scheduler.ReportShardFailure(ctx, request.ID, request.GroupIdentity, "shard-1")
	if err != nil {
		t.Fatalf("report shard failure: %v", err)
	}
	if !slices.Equal(siblings, []string{"shard-0", "shard-2"}) {
		t.Fatalf("cancel siblings=%v", siblings)
	}
	assertServiceState(t, scheduler, request.ID, WorkloadStatusCancelling)
	assertSnapshotCounts(t, scheduler, 1, 3)
}

func assertRecoveredServiceShardGroup(
	t *testing.T,
	ctx context.Context,
	config schedulerServiceTestConfig,
	request WorkloadRequest,
) {
	t.Helper()
	recovered := mustOpenSchedulerService(t, ctx, config)
	defer closeSchedulerService(t, recovered)
	assertServiceState(t, recovered, request.ID, WorkloadStatusCancelling)
	snapshot, err := recovered.Snapshot()
	if err != nil {
		t.Fatalf("snapshot recovered group: %v", err)
	}
	if !reflect.DeepEqual(snapshot.Workloads[0].Request, request) || len(snapshot.Leases) != 3 {
		t.Fatalf("recovered group identity drifted: %+v", snapshot)
	}
	siblings, err := recovered.ReportShardFailure(ctx, request.ID, request.GroupIdentity, "shard-1")
	if err != nil {
		t.Fatalf("retry persisted shard failure after lost response: %v", err)
	}
	if !slices.Equal(siblings, []string{"shard-0", "shard-2"}) {
		t.Fatalf("retried cancel siblings=%v", siblings)
	}
	if _, err := recovered.ReportShardFailure(ctx, request.ID, request.GroupIdentity, "shard-2"); err == nil {
		t.Fatal("conflicting failed shard retry was accepted")
	}
	if err := recovered.CompleteGroup(ctx, request.ID, request.GroupIdentity, WorkloadStatusPassed); err == nil {
		t.Fatal("cancelling group completed as passed")
	}
	assertServiceState(t, recovered, request.ID, WorkloadStatusCancelling)
	assertSnapshotCounts(t, recovered, 1, 3)
	if err := recovered.CompleteGroup(ctx, request.ID, request.GroupIdentity, WorkloadStatusFailed); err != nil {
		t.Fatalf("complete recovered group: %v", err)
	}
	assertSnapshotCounts(t, recovered, 1, 0)
}

func TestSchedulerServiceGroupIdentityAndQueuedCancellationFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "group-cancel"))
	defer closeSchedulerService(t, scheduler)
	request := WorkloadRequest{
		ID: "group", InvocationID: "invocation", EnqueueSequence: 1,
		Kind: WorkloadKindShard, GroupIdentity: "group-identity",
		GroupSize: 1, ShardIdentities: []string{"shard-0"},
	}
	mustServiceEnqueue(t, scheduler, request)
	if err := scheduler.CancelGroup(ctx, request.ID, "unknown"); err == nil {
		t.Fatal("unknown group identity was accepted")
	}
	if err := scheduler.Complete(ctx, request.ID, WorkloadStatusFailed); err == nil {
		t.Fatal("standalone completion accepted grouped workload")
	}
	if err := scheduler.CancelGroup(ctx, request.ID, request.GroupIdentity); err != nil {
		t.Fatalf("cancel queued group: %v", err)
	}
	assertServiceState(t, scheduler, request.ID, WorkloadStatusCancelled)
}

func TestSchedulerServiceGroupFailureCannotBypassCancellationBarrier(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "group-failure-barrier"))
	defer closeSchedulerService(t, scheduler)
	request := testServiceShardGroupRequest()
	mustServiceEnqueue(t, scheduler, request)
	if _, err := scheduler.ReserveRunnable(ctx); err != nil {
		t.Fatalf("reserve shard group: %v", err)
	}
	for _, status := range []WorkloadStatus{WorkloadStatusFailed, WorkloadStatusInfraFailed, WorkloadStatusCancelled} {
		if err := scheduler.CompleteGroup(ctx, request.ID, request.GroupIdentity, status); err == nil {
			t.Fatalf("started group completed directly as %s", status)
		}
		assertServiceState(t, scheduler, request.ID, WorkloadStatusStarted)
		assertSnapshotCounts(t, scheduler, 1, 3)
	}
}

func TestSchedulerServiceFieldRegistry(t *testing.T) {
	t.Parallel()
	assertSchedulerStructFields(t, reflect.TypeFor[SchedulerConfig](), []string{
		"Endpoint", "TLSFingerprint", "DaemonID", "OwnerUID",
	})
	assertSchedulerStructFields(t, reflect.TypeFor[WorkloadRequest](), []string{
		"ID", "InvocationID", "EnqueueSequence", "Subsequence", "Kind", "ServiceCount",
		"GroupIdentity", "GroupSize", "ShardIdentities", "Dependencies",
	})
	assertSchedulerStructFields(t, reflect.TypeFor[Lease](), []string{
		"ID", "WorkloadID", "Kind", "GroupIdentity", "ShardIdentity",
	})
	assertSchedulerStructFields(t, reflect.TypeFor[WorkloadReservation](), []string{
		"WorkloadID", "GroupIdentity", "Leases",
	})
	assertSchedulerStructFields(t, reflect.TypeFor[WorkloadSnapshot](), []string{"Request", "Status"})
	assertSchedulerStructFields(t, reflect.TypeFor[SchedulerSnapshot](), []string{"Workloads", "Leases"})
}

type schedulerServiceTestConfig struct {
	SchedulerConfig
	runtimeRoot string
}

func newSchedulerServiceConfig(t *testing.T, name string) schedulerServiceTestConfig {
	t.Helper()
	privateDir := canonicalPrivateTempDir(t)
	return schedulerServiceTestConfig{
		SchedulerConfig: SchedulerConfig{
			Endpoint: "unix:///var/run/docker.sock",
			DaemonID: "daemon-" + name,
			OwnerUID: os.Geteuid(),
		},
		runtimeRoot: privateDir,
	}
}

func assertSchedulerAliasPaths(t *testing.T, first, alias schedulerServiceTestConfig) {
	t.Helper()
	firstIdentity, err := newDaemonIdentity(first.Endpoint, first.TLSFingerprint, first.DaemonID, first.OwnerUID)
	if err != nil {
		t.Fatalf("normalize first identity: %v", err)
	}
	aliasIdentity, err := newDaemonIdentity(alias.Endpoint, alias.TLSFingerprint, alias.DaemonID, alias.OwnerUID)
	if err != nil {
		t.Fatalf("normalize alias identity: %v", err)
	}
	firstLock, firstState, err := deriveSchedulerRuntimePaths(first.runtimeRoot, firstIdentity)
	if err != nil {
		t.Fatalf("derive first paths: %v", err)
	}
	aliasLock, aliasState, err := deriveSchedulerRuntimePaths(alias.runtimeRoot, aliasIdentity)
	if err != nil {
		t.Fatalf("derive alias paths: %v", err)
	}
	if firstIdentity.key != aliasIdentity.key || firstLock != aliasLock || firstState != aliasState {
		t.Fatalf("normalized alias paths diverged")
	}
}

func mustOpenSchedulerService(t *testing.T, ctx context.Context, config schedulerServiceTestConfig) *Scheduler {
	t.Helper()
	scheduler, err := openSchedulerWithRuntimeRoot(ctx, config.SchedulerConfig, config.runtimeRoot)
	if err != nil {
		t.Fatalf("open scheduler service: %v", err)
	}
	return scheduler
}

func closeSchedulerService(t *testing.T, scheduler *Scheduler) {
	t.Helper()
	if err := scheduler.Close(); err != nil {
		t.Fatalf("close scheduler service: %v", err)
	}
}

func mustServiceEnqueue(t *testing.T, scheduler *Scheduler, request WorkloadRequest) {
	t.Helper()
	if err := scheduler.Enqueue(context.Background(), request); err != nil {
		t.Fatalf("enqueue %q: %v", request.ID, err)
	}
}

func assertSingleReservation(t *testing.T, scheduler *Scheduler, wantID string) {
	t.Helper()
	reservations, err := scheduler.ReserveRunnable(context.Background())
	if err != nil {
		t.Fatalf("reserve %q: %v", wantID, err)
	}
	if len(reservations) != 1 || reservations[0].WorkloadID != wantID {
		t.Fatalf("reservations=%+v want=%q", reservations, wantID)
	}
}

func assertSnapshotCounts(t *testing.T, scheduler *Scheduler, workloads, leases int) {
	t.Helper()
	snapshot, err := scheduler.Snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(snapshot.Workloads) != workloads || len(snapshot.Leases) != leases {
		t.Fatalf("snapshot workloads=%d leases=%d want=%d/%d", len(snapshot.Workloads), len(snapshot.Leases), workloads, leases)
	}
}

func assertServiceState(t *testing.T, scheduler *Scheduler, id string, want WorkloadStatus) {
	t.Helper()
	got, err := scheduler.State(id)
	if err != nil {
		t.Fatalf("state %q: %v", id, err)
	}
	if got != want {
		t.Fatalf("state %q=%s want=%s", id, got, want)
	}
}

func assertServiceStateAfterReopen(
	t *testing.T,
	ctx context.Context,
	config schedulerServiceTestConfig,
	id string,
	want WorkloadStatus,
) {
	t.Helper()
	scheduler := mustOpenSchedulerService(t, ctx, config)
	defer closeSchedulerService(t, scheduler)
	assertServiceState(t, scheduler, id, want)
}
