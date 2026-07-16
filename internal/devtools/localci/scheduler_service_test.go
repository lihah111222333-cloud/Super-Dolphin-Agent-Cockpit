//go:build unix

package localci

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
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

func TestSchedulerServiceRejectsUnknownAndDuplicateInput(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheduler := mustOpenSchedulerService(t, ctx, newSchedulerServiceConfig(t, "validation"))
	defer closeSchedulerService(t, scheduler)
	cases := []WorkloadRequest{
		{InvocationID: "inv", EnqueueSequence: 1, Kind: WorkloadKindJob},
		{ID: "job", InvocationID: "inv", EnqueueSequence: 1, Kind: "unknown"},
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
		Kind: WorkloadKindJob, ServiceCount: 1, Dependencies: []string{"dependency"},
	}
	spec, err := request.toSpec()
	if err != nil {
		t.Fatalf("map request: %v", err)
	}
	if spec.id != "job" || spec.invocationID != "inv" || spec.enqueueSeq != 7 || spec.subSeq != 3 {
		t.Fatalf("identity or sequence fields were not mapped: %+v", spec)
	}
	if spec.kind != workloadJob || spec.serviceCount != 1 || !reflect.DeepEqual(spec.dependencies, []workloadID{"dependency"}) {
		t.Fatalf("kind, service, or dependency fields were not mapped: %+v", spec)
	}
	request.Dependencies[0] = "mutated"
	if spec.dependencies[0] != "dependency" {
		t.Fatalf("mapped dependencies share request backing storage: %v", spec.dependencies)
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

func TestSchedulerServiceFieldRegistry(t *testing.T) {
	t.Parallel()
	assertSchedulerStructFields(t, reflect.TypeOf(SchedulerConfig{}), []string{
		"Endpoint", "TLSFingerprint", "DaemonID", "OwnerUID",
	})
	assertSchedulerStructFields(t, reflect.TypeOf(WorkloadRequest{}), []string{
		"ID", "InvocationID", "EnqueueSequence", "Subsequence", "Kind", "ServiceCount", "Dependencies",
	})
	assertSchedulerStructFields(t, reflect.TypeOf(Lease{}), []string{"ID", "WorkloadID", "Kind"})
	assertSchedulerStructFields(t, reflect.TypeOf(WorkloadReservation{}), []string{"WorkloadID", "Leases"})
	assertSchedulerStructFields(t, reflect.TypeOf(WorkloadSnapshot{}), []string{"Request", "Status"})
	assertSchedulerStructFields(t, reflect.TypeOf(SchedulerSnapshot{}), []string{"Workloads", "Leases"})
}

func assertSchedulerStructFields(t *testing.T, structType reflect.Type, want []string) {
	t.Helper()
	got := make([]string, structType.NumField())
	for index := range structType.NumField() {
		got[index] = structType.Field(index).Name
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s fields=%v want=%v", structType.Name(), got, want)
	}
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
