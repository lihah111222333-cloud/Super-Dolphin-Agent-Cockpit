package localci

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"testing"

	"golang.org/x/sync/errgroup"
)

const testMaxActiveWorkloads = 3

func TestDaemonIdentityNormalizesContextAliases(t *testing.T) {
	t.Parallel()

	first, err := newDaemonIdentity("unix:///var/run/docker.sock", "", "daemon-1", 501)
	if err != nil {
		t.Fatalf("new first daemon identity: %v", err)
	}
	second, err := newDaemonIdentity("unix:///var/run/../run/docker.sock", "", "daemon-1", 501)
	if err != nil {
		t.Fatalf("new second daemon identity: %v", err)
	}
	if first.key != second.key {
		t.Fatalf("context aliases produced different keys: %q != %q", first.key, second.key)
	}
}

func TestDaemonIdentityRejectsIncompleteIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		endpoint    string
		fingerprint string
		daemonID    string
		ownerUID    int
	}{
		{name: "missing endpoint", daemonID: "daemon-1", ownerUID: 501},
		{name: "missing daemon id", endpoint: "unix:///var/run/docker.sock", ownerUID: 501},
		{name: "invalid owner", endpoint: "unix:///var/run/docker.sock", daemonID: "daemon-1", ownerUID: -1},
		{name: "tcp without tls fingerprint", endpoint: "tcp://127.0.0.1:2376", daemonID: "daemon-1", ownerUID: 501},
		{name: "unix endpoint with host", endpoint: "unix://docker.example/var/run/docker.sock", daemonID: "daemon-1", ownerUID: 501},
		{name: "unix endpoint with tls fingerprint", endpoint: "unix:///var/run/docker.sock", fingerprint: "sha256:tls", daemonID: "daemon-1", ownerUID: 501},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := newDaemonIdentity(tc.endpoint, tc.fingerprint, tc.daemonID, tc.ownerUID); err == nil {
				t.Fatal("expected identity validation error")
			}
		})
	}
}

func TestSchedulerRejectsDuplicateWorkloadID(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, workloadSpec{id: "duplicate", invocationID: "inv-1", enqueueSeq: 1, kind: workloadJob})
	if err := kernel.enqueue(workloadSpec{id: "duplicate", invocationID: "inv-2", enqueueSeq: 2, kind: workloadJob}); err == nil {
		t.Fatal("expected duplicate workload ID to fail")
	}
}

func TestSchedulerRejectsUnknownDependencyAndCycle(t *testing.T) {
	t.Parallel()

	t.Run("unknown dependency", func(t *testing.T) {
		kernel := newTestSchedulerKernel(t)
		mustEnqueue(t, kernel, workloadSpec{
			id:           "job",
			invocationID: "inv-1",
			enqueueSeq:   1,
			kind:         workloadJob,
			dependencies: []workloadID{"missing"},
		})
		if _, err := kernel.reserveRunnable(); err == nil {
			t.Fatal("expected unknown dependency to fail")
		}
	})

	t.Run("dependency cycle", func(t *testing.T) {
		kernel := newTestSchedulerKernel(t)
		mustEnqueue(t, kernel, workloadSpec{
			id:           "first",
			invocationID: "inv-1",
			enqueueSeq:   1,
			kind:         workloadJob,
			dependencies: []workloadID{"second"},
		})
		mustEnqueue(t, kernel, workloadSpec{
			id:           "second",
			invocationID: "inv-2",
			enqueueSeq:   2,
			kind:         workloadJob,
			dependencies: []workloadID{"first"},
		})
		if _, err := kernel.reserveRunnable(); err == nil {
			t.Fatal("expected dependency cycle to fail")
		}
	})
}

func TestSchedulerFourthWorkloadStaysQueued(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	for i := 1; i <= 4; i++ {
		mustEnqueue(t, kernel, workloadSpec{
			id:           workloadID(rune('0' + i)),
			invocationID: invocationID(rune('0' + i)),
			enqueueSeq:   uint64(i),
			kind:         workloadJob,
		})
	}

	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve runnable: %v", err)
	}
	if len(reservations) != testMaxActiveWorkloads {
		t.Fatalf("reservations=%d want=%d", len(reservations), testMaxActiveWorkloads)
	}
	if got := kernel.state(workloadID('4')); got != stateQueued {
		t.Fatalf("fourth state=%s want=%s", got, stateQueued)
	}
}

func TestSchedulerBuildJobAndServiceShareCapacity(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, workloadSpec{id: "build", invocationID: "inv-build", enqueueSeq: 1, kind: workloadBuild})
	mustEnqueue(t, kernel, workloadSpec{id: "job", invocationID: "inv-job", enqueueSeq: 2, kind: workloadJob})
	mustEnqueue(t, kernel, workloadSpec{id: "service", invocationID: "inv-service", enqueueSeq: 3, kind: workloadService})
	mustEnqueue(t, kernel, workloadSpec{id: "later", invocationID: "inv-later", enqueueSeq: 4, kind: workloadJob})

	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve runnable: %v", err)
	}
	if len(reservations) != 3 {
		t.Fatalf("reservations=%d want=3", len(reservations))
	}
	if got := kernel.state("later"); got != stateQueued {
		t.Fatalf("later state=%s want=%s", got, stateQueued)
	}
}

func TestSchedulerLimitsImageBuildsToOne(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, workloadSpec{id: "build-1", invocationID: "inv-1", enqueueSeq: 1, kind: workloadBuild})
	mustEnqueue(t, kernel, workloadSpec{id: "build-2", invocationID: "inv-2", enqueueSeq: 2, kind: workloadBuild})
	mustEnqueue(t, kernel, workloadSpec{id: "job", invocationID: "inv-3", enqueueSeq: 3, kind: workloadJob})

	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve runnable: %v", err)
	}
	if len(reservations) != 2 {
		t.Fatalf("reservations=%d want=2", len(reservations))
	}
	if got := kernel.state("build-2"); got != stateQueued {
		t.Fatalf("second build state=%s want=%s", got, stateQueued)
	}
}

func TestSchedulerDAGRunnableFIFOAndBlockedBuildDependency(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, workloadSpec{
		id:           "job-waits-for-build",
		invocationID: "inv-1",
		enqueueSeq:   1,
		kind:         workloadJob,
		dependencies: []workloadID{"build"},
	})
	mustEnqueue(t, kernel, workloadSpec{id: "build", invocationID: "inv-1", enqueueSeq: 1, subSeq: 0, kind: workloadBuild})
	mustEnqueue(t, kernel, workloadSpec{id: "later-job", invocationID: "inv-2", enqueueSeq: 2, kind: workloadJob})

	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve runnable: %v", err)
	}
	assertReservationIDs(t, reservations, "build", "later-job")
	if got := kernel.state("job-waits-for-build"); got != stateQueued {
		t.Fatalf("blocked job state=%s want=%s", got, stateQueued)
	}
}

func TestSchedulerCompletesQueuedWorkloadAfterDependencyFailure(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, workloadSpec{
		id: "job", invocationID: "inv", enqueueSeq: 1, subSeq: 1, kind: workloadJob,
		dependencies: []workloadID{"build"},
	})
	mustEnqueue(t, kernel, workloadSpec{
		id: "build", invocationID: "inv", enqueueSeq: 1, kind: workloadBuild,
	})
	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatal(err)
	}
	assertReservationIDs(t, reservations, "build")
	if err := kernel.complete("build", stateInfraFailed); err != nil {
		t.Fatal(err)
	}
	if err := kernel.complete("job", stateInfraFailed); err != nil {
		t.Fatal(err)
	}
	if got := kernel.state("job"); got != stateInfraFailed {
		t.Fatalf("blocked job state=%s want=%s", got, stateInfraFailed)
	}
}

func TestSchedulerGangReservationIsAtomic(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, workloadSpec{
		id:           "gang",
		invocationID: "inv-gang",
		enqueueSeq:   1,
		kind:         workloadJob,
		serviceCount: 2,
	})
	mustEnqueue(t, kernel, workloadSpec{id: "later", invocationID: "inv-later", enqueueSeq: 2, kind: workloadJob})

	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve runnable: %v", err)
	}
	if len(reservations) != 1 || len(reservations[0].leases) != 3 {
		t.Fatalf("gang reservations=%+v want one atomic 3-slot reservation", reservations)
	}
	if got := kernel.state("later"); got != stateQueued {
		t.Fatalf("later state=%s want=%s", got, stateQueued)
	}
}

func TestSchedulerGangWaitsAtomicallyBehindActiveSlots(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, workloadSpec{id: "active", invocationID: "inv-active", enqueueSeq: 1, kind: workloadJob})
	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve active workload: %v", err)
	}
	assertReservationIDs(t, reservations, "active")

	mustEnqueue(t, kernel, workloadSpec{
		id:           "gang",
		invocationID: "inv-gang",
		enqueueSeq:   2,
		kind:         workloadJob,
		serviceCount: 2,
	})
	reservations, err = kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve blocked gang: %v", err)
	}
	if len(reservations) != 0 {
		t.Fatalf("partially reserved gang: %+v", reservations)
	}
	if got := kernel.state("gang"); got != stateQueued {
		t.Fatalf("gang state=%s want=%s", got, stateQueued)
	}
}

func TestSchedulerBlockedGangHasBoundedBypassAndCannotStarve(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, workloadSpec{id: "active", invocationID: "inv-active", enqueueSeq: 1, kind: workloadJob})
	if _, err := kernel.reserveRunnable(); err != nil {
		t.Fatalf("reserve active workload: %v", err)
	}
	mustEnqueue(t, kernel, workloadSpec{
		id:           "gang",
		invocationID: "inv-gang",
		enqueueSeq:   2,
		kind:         workloadJob,
		serviceCount: 2,
	})
	mustEnqueue(t, kernel, workloadSpec{id: "bypass-1", invocationID: "inv-bypass-1", enqueueSeq: 3, kind: workloadJob})
	mustEnqueue(t, kernel, workloadSpec{id: "bypass-2", invocationID: "inv-bypass-2", enqueueSeq: 4, kind: workloadJob})

	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve bounded bypass: %v", err)
	}
	assertReservationIDs(t, reservations, "bypass-1")
	if got := kernel.state("bypass-2"); got != stateQueued {
		t.Fatalf("second bypass state=%s want=%s", got, stateQueued)
	}

	if err := kernel.complete("active", statePassed); err != nil {
		t.Fatalf("complete active workload: %v", err)
	}
	reservations, err = kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve while one bypass active: %v", err)
	}
	if len(reservations) != 0 {
		t.Fatalf("later work bypassed starving gang: %+v", reservations)
	}

	if err := kernel.complete("bypass-1", statePassed); err != nil {
		t.Fatalf("complete bypass workload: %v", err)
	}
	reservations, err = kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve gang after capacity return: %v", err)
	}
	assertReservationIDs(t, reservations, "gang")
}

func TestSchedulerCompletionReturnsSlots(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	for i := 1; i <= 4; i++ {
		mustEnqueue(t, kernel, workloadSpec{
			id:           workloadID(rune('0' + i)),
			invocationID: invocationID(rune('0' + i)),
			enqueueSeq:   uint64(i),
			kind:         workloadJob,
		})
	}
	if _, err := kernel.reserveRunnable(); err != nil {
		t.Fatalf("reserve initial workloads: %v", err)
	}
	if err := kernel.complete("1", statePassed); err != nil {
		t.Fatalf("complete first workload: %v", err)
	}
	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve after completion: %v", err)
	}
	assertReservationIDs(t, reservations, "4")
}

func TestSchedulerCompletionLeaseMismatchLeavesStateUnchanged(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		mutate func(*schedulerKernel)
	}{
		{
			name: "missing lease",
			mutate: func(kernel *schedulerKernel) {
				delete(kernel.leases, "job/3")
			},
		},
		{
			name: "extra lease",
			mutate: func(kernel *schedulerKernel) {
				kernel.leases["job/extra"] = slotLease{
					id:         "job/extra",
					workloadID: "job",
					kind:       workloadService,
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kernel := newTestSchedulerKernel(t)
			mustEnqueue(t, kernel, workloadSpec{id: "job", invocationID: "inv-1", enqueueSeq: 1, kind: workloadJob})
			if _, err := kernel.reserveRunnable(); err != nil {
				t.Fatalf("reserve job: %v", err)
			}
			tc.mutate(kernel)
			before := cloneLeases(kernel.leases)

			if err := kernel.complete("job", statePassed); err == nil {
				t.Fatal("expected lease mismatch to fail")
			}
			if got := kernel.state("job"); got != stateStarted {
				t.Fatalf("job state=%s want=%s", got, stateStarted)
			}
			if !reflect.DeepEqual(kernel.leases, before) {
				t.Fatalf("leases changed on failed completion: got=%+v want=%+v", kernel.leases, before)
			}
		})
	}
}

func cloneLeases(source map[string]slotLease) map[string]slotLease {
	cloned := make(map[string]slotLease, len(source))
	maps.Copy(cloned, source)
	return cloned
}

func TestSchedulerRejectsOversizedGang(t *testing.T) {
	t.Parallel()

	kernel := newTestSchedulerKernel(t)
	err := kernel.enqueue(workloadSpec{
		id:           "oversized",
		invocationID: "inv-oversized",
		enqueueSeq:   1,
		kind:         workloadJob,
		serviceCount: 3,
	})
	if err == nil {
		t.Fatal("expected oversized gang to fail")
	}
}

func TestSchedulerShardGroupExactAdmissionCapacityOneTwoThree(t *testing.T) {
	t.Parallel()
	for size := 1; size <= testMaxActiveWorkloads; size++ {
		t.Run(fmt.Sprintf("capacity-%d", size), func(t *testing.T) {
			kernel := newTestSchedulerKernel(t)
			spec := testShardGroupSpec(size)
			mustEnqueue(t, kernel, spec)
			reservations, err := kernel.reserveRunnable()
			if err != nil {
				t.Fatalf("reserve group size %d: %v", size, err)
			}
			if len(reservations) != 1 || len(reservations[0].leases) != size {
				t.Fatalf("reservations=%+v want one atomic %d-slot group", reservations, size)
			}
			for index, lease := range reservations[0].leases {
				if lease.groupID != spec.groupID || lease.shardID != spec.shardIDs[index] {
					t.Fatalf("lease[%d]=%+v identity binding drifted", index, lease)
				}
			}
		})
	}
}

func TestSchedulerShardGroupIdentityFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*workloadSpec)
	}{
		{name: "missing-group", mutate: func(spec *workloadSpec) { spec.groupID = "" }},
		{name: "missing-shard", mutate: func(spec *workloadSpec) { spec.shardIDs[1] = "" }},
		{name: "duplicate-shard", mutate: func(spec *workloadSpec) { spec.shardIDs[1] = spec.shardIDs[0] }},
		{name: "size-mismatch", mutate: func(spec *workloadSpec) { spec.groupSize = 1 }},
		{name: "wrong-kind", mutate: func(spec *workloadSpec) { spec.kind = workloadJob }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			kernel := newTestSchedulerKernel(t)
			spec := testShardGroupSpec(3)
			test.mutate(&spec)
			if err := kernel.enqueue(spec); err == nil {
				t.Fatal("invalid shard group was accepted")
			}
		})
	}
	kernel := newTestSchedulerKernel(t)
	first := testShardGroupSpec(2)
	mustEnqueue(t, kernel, first)
	second := testShardGroupSpec(2)
	second.id = "other"
	second.invocationID = "other-invocation"
	if err := kernel.enqueue(second); err == nil {
		t.Fatal("duplicate group identity was accepted")
	}
}

func TestSchedulerShardFailureCancellationBarrierRetainsCapacity(t *testing.T) {
	t.Parallel()
	kernel := newTestSchedulerKernel(t)
	spec := testShardGroupSpec(3)
	mustEnqueue(t, kernel, spec)
	if _, err := kernel.reserveRunnable(); err != nil {
		t.Fatalf("reserve group: %v", err)
	}
	siblings, err := kernel.reportShardFailure(spec.id, spec.groupID, spec.shardIDs[1])
	if err != nil {
		t.Fatalf("report shard failure: %v", err)
	}
	if !slices.Equal(siblings, []string{spec.shardIDs[0], spec.shardIDs[2]}) {
		t.Fatalf("siblings=%v", siblings)
	}
	if kernel.state(spec.id) != stateCancelling || len(kernel.leases) != 3 {
		t.Fatalf("cancellation barrier released capacity early: state=%s leases=%d", kernel.state(spec.id), len(kernel.leases))
	}
	if err := kernel.completeGroup(spec.id, stateFailed); err != nil {
		t.Fatalf("complete cancelled group: %v", err)
	}
	if len(kernel.leases) != 0 {
		t.Fatalf("completed group retained %d leases", len(kernel.leases))
	}
}

func TestSchedulerShardGroupConcurrentPeakIsThree(t *testing.T) {
	t.Parallel()
	kernel := newTestSchedulerKernel(t)
	spec := testShardGroupSpec(3)
	mustEnqueue(t, kernel, spec)
	mustEnqueue(t, kernel, workloadSpec{
		id: "later", invocationID: "later-invocation", enqueueSeq: 2, kind: workloadJob,
	})
	reservations, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve group: %v", err)
	}
	peak, err := runConcurrentTestLeases(reservations[0].leases)
	if err != nil {
		t.Fatalf("run concurrent leases: %v", err)
	}
	if peak != testMaxActiveWorkloads {
		t.Fatalf("concurrent peak=%d want=%d", peak, testMaxActiveWorkloads)
	}
	if queued, err := kernel.reserveRunnable(); err != nil || len(queued) != 0 {
		t.Fatalf("fourth workload admitted while group active: reservations=%+v err=%v", queued, err)
	}
	if err := kernel.complete(spec.id, statePassed); err != nil {
		t.Fatalf("complete group: %v", err)
	}
	next, err := kernel.reserveRunnable()
	if err != nil {
		t.Fatalf("reserve fourth workload: %v", err)
	}
	assertReservationIDs(t, next, "later")
}

func runConcurrentTestLeases(leases []slotLease) (int, error) {
	started := make(chan struct{}, len(leases))
	release := make(chan struct{})
	var group errgroup.Group
	var mu sync.Mutex
	running, peak := 0, 0
	for range leases {
		group.Go(func() error {
			mu.Lock()
			running++
			peak = max(peak, running)
			mu.Unlock()
			started <- struct{}{}
			<-release
			mu.Lock()
			running--
			mu.Unlock()
			return nil
		})
	}
	for range leases {
		<-started
	}
	close(release)
	if err := group.Wait(); err != nil {
		return 0, err
	}
	return peak, nil
}

func TestSchedulerGroupCompletionStateMatrix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		fromCancelling bool
		terminal       workloadState
		wantError      bool
	}{
		{name: "started-passed", terminal: statePassed},
		{name: "started-failed", terminal: stateFailed, wantError: true},
		{name: "started-infra-failed", terminal: stateInfraFailed, wantError: true},
		{name: "started-cancelled", terminal: stateCancelled, wantError: true},
		{name: "cancelling-passed", fromCancelling: true, terminal: statePassed, wantError: true},
		{name: "cancelling-failed", fromCancelling: true, terminal: stateFailed},
		{name: "cancelling-infra-failed", fromCancelling: true, terminal: stateInfraFailed},
		{name: "cancelling-cancelled", fromCancelling: true, terminal: stateCancelled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runGroupCompletionTransitionCase(t, test.fromCancelling, test.terminal, test.wantError)
		})
	}
}

func runGroupCompletionTransitionCase(
	t *testing.T,
	fromCancelling bool,
	terminal workloadState,
	wantError bool,
) {
	t.Helper()
	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, testShardGroupSpec(3))
	if _, err := kernel.reserveRunnable(); err != nil {
		t.Fatalf("reserve group: %v", err)
	}
	if fromCancelling {
		if _, err := kernel.reportShardFailure("group", "group-identity", "shard-1"); err != nil {
			t.Fatalf("establish cancellation barrier: %v", err)
		}
	}
	err := kernel.completeGroup("group", terminal)
	if (err != nil) != wantError {
		t.Fatalf("completeGroup error=%v wantError=%v", err, wantError)
	}
	if wantError {
		assertRejectedGroupCompletion(t, kernel, fromCancelling)
		return
	}
	if got := kernel.state("group"); got != terminal || len(kernel.leases) != 0 {
		t.Fatalf("completed transition state=%s leases=%d want=%s/0", got, len(kernel.leases), terminal)
	}
}

func assertRejectedGroupCompletion(t *testing.T, kernel *schedulerKernel, fromCancelling bool) {
	t.Helper()
	wantState := stateStarted
	if fromCancelling {
		wantState = stateCancelling
	}
	if got := kernel.state("group"); got != wantState || len(kernel.leases) != 3 {
		t.Fatalf("rejected transition state=%s leases=%d want=%s/3", got, len(kernel.leases), wantState)
	}
}

func TestSchedulerShardFailureRetryIsStable(t *testing.T) {
	t.Parallel()
	kernel := newTestSchedulerKernel(t)
	mustEnqueue(t, kernel, testShardGroupSpec(3))
	if _, err := kernel.reserveRunnable(); err != nil {
		t.Fatalf("reserve group: %v", err)
	}
	want := []string{"shard-0", "shard-2"}
	for attempt := 1; attempt <= 2; attempt++ {
		got, err := kernel.reportShardFailure("group", "group-identity", "shard-1")
		if err != nil {
			t.Fatalf("report shard failure attempt %d: %v", attempt, err)
		}
		if !slices.Equal(got, want) {
			t.Fatalf("attempt %d siblings=%v want=%v", attempt, got, want)
		}
	}
	if _, err := kernel.reportShardFailure("group", "group-identity", "shard-2"); err == nil {
		t.Fatal("conflicting failed shard retry was accepted")
	}
	if kernel.nodes["group"].failedShardID != "shard-1" || len(kernel.leases) != 3 {
		t.Fatalf("failure barrier drifted: %+v leases=%d", kernel.nodes["group"], len(kernel.leases))
	}
}

func testShardGroupSpec(size int) workloadSpec {
	shardIDs := make([]string, size)
	for index := range shardIDs {
		shardIDs[index] = fmt.Sprintf("shard-%d", index)
	}
	return workloadSpec{
		id: "group", invocationID: "invocation", enqueueSeq: 1, kind: workloadShard,
		serviceCount: size - 1, groupID: "group-identity", groupSize: size, shardIDs: shardIDs,
	}
}

func newTestSchedulerKernel(t *testing.T) *schedulerKernel {
	t.Helper()

	identity, err := newDaemonIdentity("unix:///var/run/docker.sock", "", "daemon-test", 501)
	if err != nil {
		t.Fatalf("new daemon identity: %v", err)
	}
	kernel, err := newSchedulerKernel(identity, testMaxActiveWorkloads)
	if err != nil {
		t.Fatalf("new scheduler kernel: %v", err)
	}
	return kernel
}

func mustEnqueue(t *testing.T, kernel *schedulerKernel, spec workloadSpec) {
	t.Helper()
	if err := kernel.enqueue(spec); err != nil {
		t.Fatalf("enqueue %s: %v", spec.id, err)
	}
}

func assertReservationIDs(t *testing.T, reservations []reservation, want ...workloadID) {
	t.Helper()
	if len(reservations) != len(want) {
		t.Fatalf("reservation count=%d want=%d", len(reservations), len(want))
	}
	for i := range want {
		if reservations[i].workloadID != want[i] {
			t.Fatalf("reservation[%d]=%s want=%s", i, reservations[i].workloadID, want[i])
		}
	}
}
