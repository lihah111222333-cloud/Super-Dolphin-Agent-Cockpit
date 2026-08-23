package heteroci

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRoutingLeaseBookPlanAndReserveRejectsInvalidCapacityBasisWithoutMutation(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		basis SnapshotCapacityBasis
	}{
		{name: "missing", basis: ""},
		{name: "unknown", basis: SnapshotCapacityBasis("unknown-capacity-basis/v1")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newLeaseBookSingleLocalFixture(t)
			fixture.snapshot.CapacityBasis = testCase.basis
			book := NewRoutingLeaseBook()
			result, err := book.PlanAndReserve("run-invalid-basis", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
			if err == nil {
				t.Fatalf("PlanAndReserve(%s basis) unexpectedly succeeded: %#v", testCase.name, result)
			}
			if !reflect.DeepEqual(result, ReservedRoutingPlan{}) {
				t.Fatalf("PlanAndReserve(%s basis) result = %#v, want zero result", testCase.name, result)
			}
			if len(book.runs) != 0 || len(book.reservations) != 0 {
				t.Fatalf("PlanAndReserve(%s basis) mutated book: runs=%d reservations=%d", testCase.name, len(book.runs), len(book.reservations))
			}
			if _, snapshotErr := book.Snapshot("run-invalid-basis"); snapshotErr == nil {
				t.Fatalf("PlanAndReserve(%s basis) left a run snapshot", testCase.name)
			}
		})
	}
}

func TestRoutingLeaseBookCapacityBasisPreservesGrossRefreshedLinuxAvailability(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	fixture.snapshot.CapacityBasis = SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations
	book := NewRoutingLeaseBook()
	reserveAndStartCapacityBasisRun(t, book, fixture, "run-capacity-a")

	refreshed := refreshCapacityBasisSnapshot(fixture.snapshot, fixture.now.Add(2*time.Second))
	if refreshed.Nodes[0].Available != fixture.snapshot.Nodes[0].Available {
		t.Fatalf("refreshed source changed gross basis availability: got=%#v want=%#v", refreshed.Nodes[0].Available, fixture.snapshot.Nodes[0].Available)
	}
	second, err := book.PlanAndReserve("run-capacity-b", fixture.manifest, refreshed, fixture.units, fixture.now.Add(2*time.Second), fixture.ttl)
	if err == nil {
		t.Fatalf("run B PlanAndReserve() unexpectedly succeeded after full reservation: %#v", second)
	}
	if _, ok := errors.AsType[*CapacityBlockerError](err); !ok {
		t.Fatalf("run B error = %T %v, want CapacityBlockerError", err, err)
	}
	if !reflect.DeepEqual(second, ReservedRoutingPlan{}) {
		t.Fatalf("run B result = %#v, want zero result", second)
	}
}

func TestRoutingLeaseBookCapacityBasisBlockerLeavesZeroState(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	fixture.snapshot.CapacityBasis = SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations
	book := NewRoutingLeaseBook()
	reserveAndStartCapacityBasisRun(t, book, fixture, "run-platform-a")
	refreshed := refreshCapacityBasisSnapshot(fixture.snapshot, fixture.now.Add(2*time.Second))
	result, err := book.PlanAndReserve("run-platform-b", fixture.manifest, refreshed, fixture.units, fixture.now.Add(2*time.Second), fixture.ttl)
	if err == nil {
		t.Fatalf("run B unexpectedly succeeded after started reservation: %#v", result)
	}
	if _, ok := errors.AsType[*CapacityBlockerError](err); !ok {
		t.Fatalf("run B error = %T %v, want CapacityBlockerError", err, err)
	}
	if !reflect.DeepEqual(result, ReservedRoutingPlan{}) {
		t.Fatalf("run B result = %#v, want zero result", result)
	}
}

func TestRoutingLeaseBookCapacityBasisRejectsSourceAvailableBelowBookReservation(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	fixture.snapshot.CapacityBasis = SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations
	book := NewRoutingLeaseBook()
	reserveAndStartCapacityBasisRun(t, book, fixture, "run-double-a")

	refreshed := refreshCapacityBasisSnapshot(fixture.snapshot, fixture.now.Add(2*time.Second))
	refreshed.Nodes[0].Available = ResourceCapacity{}
	result, err := book.PlanAndReserve("run-double-b", fixture.manifest, refreshed, fixture.units, fixture.now.Add(2*time.Second), fixture.ttl)
	if err == nil {
		t.Fatalf("source available below book reservation unexpectedly succeeded: %#v", result)
	}
	if _, ok := errors.AsType[*CapacityBlockerError](err); !ok && !strings.Contains(err.Error(), "underflow") {
		t.Fatalf("source available below book reservation error = %T %v, want typed blocker or underflow", err, err)
	}
	if !reflect.DeepEqual(result, ReservedRoutingPlan{}) {
		t.Fatalf("source available below book reservation result = %#v, want zero result", result)
	}
	if len(book.runs) != 1 || len(book.reservations) != 1 {
		t.Fatalf("source available below book reservation mutated book: runs=%d reservations=%d", len(book.runs), len(book.reservations))
	}
	if _, snapshotErr := book.Snapshot("run-double-b"); snapshotErr == nil {
		t.Fatal("source available below book reservation left a partial run")
	}
}

func refreshCapacityBasisSnapshot(source SnapshotSet, capturedAt time.Time) SnapshotSet {
	refreshed := cloneRoutingSnapshotSet(source)
	refreshed.CapacityBasis = SnapshotCapacityBasisExternalAvailableExcludesRoutingBookReservations
	refreshed.CapturedAtUnixMilli = capturedAt.UnixMilli()
	for index := range refreshed.Nodes {
		refreshed.Nodes[index].HeartbeatUnixMilli = capturedAt.UnixMilli()
	}
	return refreshed
}

func reserveAndStartCapacityBasisRun(t *testing.T, book *RoutingLeaseBook, fixture leaseBookFixture, runID string) ReservedRoutingPlan {
	t.Helper()
	reserved, err := book.PlanAndReserve(runID, fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("run %s PlanAndReserve() unexpected error: %v", runID, err)
	}
	if len(reserved.Decision.Assignments) != 1 || reserved.Decision.Assignments[0].Executor != ExecutorKindLocalNode {
		t.Fatalf("run %s did not reserve the full local node: %#v", runID, reserved.Decision.Assignments)
	}
	assignment := reserved.Decision.Assignments[0]
	currentSnapshot := refreshCapacityBasisSnapshot(fixture.snapshot, fixture.now.Add(time.Second))
	if _, err := book.Start(runID, assignment.UnitID, currentSnapshot, fixture.now.Add(time.Second)); err != nil {
		t.Fatalf("run %s Start() unexpected error: %v", runID, err)
	}
	return reserved
}

func assertCapacityBasisPlatformBlocker(t *testing.T, book *RoutingLeaseBook, fixture leaseBookFixture, refreshed SnapshotSet, name string) {
	t.Helper()
	beforeRuns, beforeReservations := len(book.runs), len(book.reservations)
	result, err := book.PlanAndReserve("run-platform-b", fixture.manifest, refreshed, fixture.units, fixture.now.Add(2*time.Second), fixture.ttl)
	if err == nil {
		t.Fatalf("run B %s PlanAndReserve() unexpectedly succeeded: %#v", name, result)
	}
	if _, ok := errors.AsType[*CapacityBlockerError](err); !ok {
		t.Fatalf("run B %s error = %T %v, want CapacityBlockerError", name, err, err)
	}
	if !reflect.DeepEqual(result, ReservedRoutingPlan{}) {
		t.Fatalf("run B %s result = %#v, want zero result", name, result)
	}
	if len(book.runs) != beforeRuns || len(book.reservations) != beforeReservations {
		t.Fatalf("run B %s blocker mutated book: runs=%d/%d reservations=%d/%d", name, len(book.runs), beforeRuns, len(book.reservations), beforeReservations)
	}
	if _, snapshotErr := book.Snapshot("run-platform-b"); snapshotErr == nil {
		t.Fatalf("run B %s blocker left a partial run", name)
	}
}
