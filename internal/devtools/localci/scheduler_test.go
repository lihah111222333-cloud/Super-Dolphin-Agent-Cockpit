package localci

import (
	"maps"
	"reflect"
	"testing"
)

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
	if len(reservations) != maxActiveWorkloads {
		t.Fatalf("reservations=%d want=%d", len(reservations), maxActiveWorkloads)
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

func newTestSchedulerKernel(t *testing.T) *schedulerKernel {
	t.Helper()

	identity, err := newDaemonIdentity("unix:///var/run/docker.sock", "", "daemon-test", 501)
	if err != nil {
		t.Fatalf("new daemon identity: %v", err)
	}
	kernel, err := newSchedulerKernel(identity)
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
