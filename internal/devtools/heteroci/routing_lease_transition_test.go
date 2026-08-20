package heteroci

import (
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRoutingLeaseBookTransitionStartsExactLocalAndECIAssignments(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	book := NewRoutingLeaseBook()
	reserveTransitionRun(t, book, "run-transition-start", fixture)
	for index, assignment := range fixture.decision.Assignments {
		at := fixture.now.Add(time.Duration(index+1) * time.Millisecond)
		got, err := book.Start("run-transition-start", assignment.UnitID, fixture.snapshot, at)
		if err != nil {
			t.Fatalf("Start(%q) unexpected error: %v", assignment.UnitID, err)
		}
		assertTransitionLease(t, got, assignment, LeaseStateStarted, fixture.now, at.UnixMilli(), 0)
	}
}

func TestRoutingLeaseBookTransitionRejectsInvalidStartWithoutMutation(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	cases := []struct {
		name      string
		forbidden []string
		mutate    func(*SnapshotSet)
	}{
		{name: "node drift", mutate: func(value *SnapshotSet) { value.Nodes[0].NodeID = "node-drift" }},
		{name: "endpoint drift", forbidden: []string{"https://attacker.invalid/runner"}, mutate: func(value *SnapshotSet) { value.Nodes[0].Endpoint = "https://attacker.invalid/runner" }},
		{name: "probe identity drift", forbidden: []string{"probe-token"}, mutate: func(value *SnapshotSet) { value.Nodes[0].ProbeIdentity = "probe-token" }},
		{name: "probe boot id drift", forbidden: []string{"boot-secret"}, mutate: func(value *SnapshotSet) { value.Nodes[0].ProbeBootID = "boot-secret" }},
		{name: "capture older", mutate: func(value *SnapshotSet) {
			value.CapturedAtUnixMilli = fixture.now.Add(-time.Millisecond).UnixMilli()
			for index := range value.Nodes {
				value.Nodes[index].HeartbeatUnixMilli = value.CapturedAtUnixMilli
			}
		}},
		{name: "heartbeat stale", mutate: func(value *SnapshotSet) {
			value.Nodes[0].HeartbeatUnixMilli = fixture.now.Add(-fixture.ttl - time.Millisecond).UnixMilli()
		}},
		{name: "heartbeat future", mutate: func(value *SnapshotSet) {
			value.Nodes[0].HeartbeatUnixMilli = fixture.now.Add(time.Millisecond).UnixMilli()
		}},
		{name: "offline", mutate: func(value *SnapshotSet) {
			value.Nodes[0].Status = NodeStatusOffline
			value.Nodes[0].ProbeBootID = ""
			value.Nodes[0].ActiveCapabilities = []ActiveCapabilitySnapshot{}
			value.Nodes[0].HeartbeatUnixMilli = 0
			value.Nodes[0].Available = ResourceCapacity{}
		}},
		{name: "capability loss", mutate: func(value *SnapshotSet) { value.Nodes[0].ActiveCapabilities = []ActiveCapabilitySnapshot{} }},
		{name: "capability strict mismatch", mutate: func(value *SnapshotSet) { value.Nodes[0].ActiveCapabilities[0].ImageDigest = "sha256:capability-drift" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runID := "run-invalid-start-" + tc.name
			book := NewRoutingLeaseBook()
			reserveTransitionRun(t, book, runID, fixture)
			before := transitionSnapshot(t, book, runID)
			candidate := cloneSnapshotSet(fixture.snapshot)
			tc.mutate(&candidate)
			_, err := book.Start(runID, "unit-local", candidate, fixture.now.Add(time.Millisecond))
			assertTransitionStartErrorDoesNotLeak(t, err, tc.forbidden...)
			after := transitionSnapshot(t, book, runID)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("invalid Start() mutated state: before=%#v after=%#v", before, after)
			}
		})
	}

}

func TestRoutingLeaseBookTransitionECIStartRejectsInvalidSnapshotWithoutMutation(t *testing.T) {
	fixture := newLeaseBookECIFixture(t)
	book := NewRoutingLeaseBook()
	runID := "run-invalid-eci-snapshot"
	reserveTransitionRun(t, book, runID, fixture)
	invalid := cloneSnapshotSet(fixture.snapshot)
	invalid.Nodes[0].HeartbeatUnixMilli = fixture.now.Add(time.Second).UnixMilli()
	before := transitionSnapshot(t, book, runID)
	if _, err := book.Start(runID, "unit-eci", invalid, fixture.now.Add(time.Millisecond)); err == nil {
		t.Fatal("ECI Start() accepted invalid current snapshot")
	}
	after := transitionSnapshot(t, book, runID)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid ECI Start() mutated state: before=%#v after=%#v", before, after)
	}
}

func TestRoutingLeaseBookTransitionRejectsUnknownRunAndDuplicateStart(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	assignment := transitionAssignment(t, fixture.decision, "unit-local")
	book := NewRoutingLeaseBook()
	runID := "run-invalid-start-unknown-run"
	reserveTransitionRun(t, book, runID, fixture)
	before := transitionSnapshot(t, book, runID)
	if _, err := book.Start("run-unknown", assignment.UnitID, fixture.snapshot, fixture.now.Add(time.Millisecond)); err == nil {
		t.Fatal("unknown-run Start() unexpectedly succeeded")
	}
	if after := transitionSnapshot(t, book, runID); !reflect.DeepEqual(after, before) {
		t.Fatalf("unknown-run Start() mutated existing state: before=%#v after=%#v", before, after)
	}

	started, err := book.Start(runID, assignment.UnitID, fixture.snapshot, fixture.now.Add(time.Millisecond))
	if err != nil {
		t.Fatalf("valid Start() unexpected error: %v", err)
	}
	before = transitionSnapshot(t, book, runID)
	if _, err := book.Start(runID, assignment.UnitID, fixture.snapshot, fixture.now.Add(2*time.Millisecond)); err == nil {
		t.Fatal("duplicate Start() unexpectedly succeeded")
	}
	after := transitionSnapshot(t, book, runID)
	current := transitionLease(t, after, assignment.UnitID)
	if !reflect.DeepEqual(after, before) || current.State != started.State || current.StartedAtUnixMilli != started.StartedAtUnixMilli {
		t.Fatalf("duplicate Start() mutated state: before=%#v after=%#v", before, after)
	}
}

func TestRoutingLeaseBookTransitionRejectsInvalidFinishWithoutMutation(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	assignment := transitionAssignment(t, fixture.decision, "unit-local")
	book := NewRoutingLeaseBook()
	runID := "run-invalid-finish"
	reserveTransitionRun(t, book, runID, fixture)
	before := transitionSnapshot(t, book, runID)
	if _, err := book.Finish(runID, assignment.UnitID, LeaseStateSucceeded, fixture.now.Add(time.Millisecond)); err == nil {
		t.Fatal("Finish() before Start() unexpectedly succeeded")
	}
	if after := transitionSnapshot(t, book, runID); !reflect.DeepEqual(after, before) {
		t.Fatalf("Finish() before Start() mutated state: before=%#v after=%#v", before, after)
	}
	if _, err := book.Start(runID, assignment.UnitID, fixture.snapshot, fixture.now.Add(time.Millisecond)); err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}
	started := transitionSnapshot(t, book, runID)
	for _, terminal := range []RoutingLeaseState{LeaseStatePlanned, LeaseStateStarted, RoutingLeaseState("unknown")} {
		if _, err := book.Finish(runID, assignment.UnitID, terminal, fixture.now.Add(2*time.Millisecond)); err == nil {
			t.Fatalf("Finish(%q) unexpectedly succeeded", terminal)
		}
		if after := transitionSnapshot(t, book, runID); !reflect.DeepEqual(after, started) {
			t.Fatalf("invalid Finish(%q) mutated state: before=%#v after=%#v", terminal, started, after)
		}
	}
	if _, err := book.Finish(runID, assignment.UnitID, LeaseStateSucceeded, fixture.now.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("valid Finish() unexpected error: %v", err)
	}
	finished := transitionSnapshot(t, book, runID)
	if _, err := book.Finish(runID, assignment.UnitID, LeaseStateFailed, fixture.now.Add(3*time.Millisecond)); err == nil {
		t.Fatal("duplicate Finish() unexpectedly succeeded")
	}
	if after := transitionSnapshot(t, book, runID); !reflect.DeepEqual(after, finished) {
		t.Fatalf("duplicate Finish() mutated state: before=%#v after=%#v", finished, after)
	}
}

func TestRoutingLeaseBookTransitionRejectsStartAfterSucceededOrFailed(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	for _, terminal := range []RoutingLeaseState{LeaseStateSucceeded, LeaseStateFailed} {
		t.Run(string(terminal), func(t *testing.T) {
			book := NewRoutingLeaseBook()
			runID := "run-terminal-" + string(terminal)
			reserveTransitionRun(t, book, runID, fixture)
			assignment := transitionAssignment(t, fixture.decision, "unit-local")
			startedAt := fixture.now.Add(time.Millisecond)
			if _, err := book.Start(runID, assignment.UnitID, fixture.snapshot, startedAt); err != nil {
				t.Fatalf("Start() unexpected error: %v", err)
			}
			if _, err := book.Finish(runID, assignment.UnitID, terminal, fixture.now.Add(2*time.Millisecond)); err != nil {
				t.Fatalf("Finish(%q) unexpected error: %v", terminal, err)
			}
			before := transitionSnapshot(t, book, runID)
			if _, err := book.Start(runID, assignment.UnitID, fixture.snapshot, fixture.now.Add(3*time.Millisecond)); err == nil {
				t.Fatal("Start() after terminal state unexpectedly succeeded")
			}
			after := transitionSnapshot(t, book, runID)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("terminal Start() mutated state: before=%#v after=%#v", before, after)
			}
		})
	}
}

func TestRoutingLeaseBookTransitionCancelsOnlyPlannedLeases(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	book := NewRoutingLeaseBook()
	runID := "run-cancel-planned"
	reserveTransitionRun(t, book, runID, fixture)
	local := transitionAssignment(t, fixture.decision, "unit-local")
	cancelledAt := fixture.now.Add(time.Millisecond)
	got, err := book.CancelPlanned(runID, local.UnitID, cancelledAt)
	if err != nil {
		t.Fatalf("CancelPlanned() unexpected error: %v", err)
	}
	assertTransitionLease(t, got, local, LeaseStateCancelled, fixture.now, 0, cancelledAt.UnixMilli())
	before := transitionSnapshot(t, book, runID)
	if _, err := book.CancelPlanned(runID, local.UnitID, fixture.now.Add(2*time.Millisecond)); err == nil {
		t.Fatal("duplicate CancelPlanned() unexpectedly succeeded")
	}
	if after := transitionSnapshot(t, book, runID); !reflect.DeepEqual(after, before) {
		t.Fatalf("duplicate CancelPlanned() mutated state: before=%#v after=%#v", before, after)
	}

	eci := transitionAssignment(t, fixture.decision, "unit-eci")
	if _, err := book.Start(runID, eci.UnitID, fixture.snapshot, fixture.now.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("Start(ECI) unexpected error: %v", err)
	}
	before = transitionSnapshot(t, book, runID)
	if _, err := book.CancelPlanned(runID, eci.UnitID, fixture.now.Add(3*time.Millisecond)); err == nil {
		t.Fatal("CancelPlanned() on started lease unexpectedly succeeded")
	}
	if after := transitionSnapshot(t, book, runID); !reflect.DeepEqual(after, before) {
		t.Fatalf("CancelPlanned() on started lease mutated state: before=%#v after=%#v", before, after)
	}
}

func TestRoutingLeaseBookTransitionReleasesMixedRunOnlyAfterAllUnitsTerminal(t *testing.T) {
	fixture := newLeaseBookMixedFullLocalFixture(t)
	book := NewRoutingLeaseBook()
	firstRun := "run-release-first"
	secondRun := "run-release-second"
	reserveTransitionRun(t, book, firstRun, fixture)
	if _, err := book.Reserve(secondRun, fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl); err == nil {
		t.Fatal("second Reserve() unexpectedly ignored held local capacity")
	}
	local := transitionAssignment(t, fixture.decision, "unit-local")
	eci := transitionAssignment(t, fixture.decision, "unit-eci")
	if _, err := book.CancelPlanned(firstRun, local.UnitID, fixture.now.Add(time.Millisecond)); err != nil {
		t.Fatalf("CancelPlanned(local) unexpected error: %v", err)
	}
	if _, err := book.Reserve(secondRun, fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now.Add(2*time.Millisecond), fixture.ttl); err == nil {
		t.Fatal("second Reserve() succeeded before all units became terminal")
	}
	if _, err := book.Start(firstRun, eci.UnitID, fixture.snapshot, fixture.now.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("Start(ECI) unexpected error: %v", err)
	}
	if _, err := book.Finish(firstRun, eci.UnitID, LeaseStateSucceeded, fixture.now.Add(2*time.Millisecond)); err != nil {
		t.Fatalf("Finish(ECI) unexpected error: %v", err)
	}
	if _, err := book.Reserve(secondRun, fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now.Add(3*time.Millisecond), fixture.ttl); err != nil {
		t.Fatalf("second Reserve() after release unexpected error: %v", err)
	}
	history := transitionSnapshot(t, book, firstRun)
	if transitionState(history, "unit-local") != LeaseStateCancelled || transitionState(history, "unit-eci") != LeaseStateSucceeded {
		t.Fatalf("historical terminal states not retained: %#v", history)
	}
}

func TestRoutingLeaseBookTransitionSnapshotIsSortedAndIndependent(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	book := NewRoutingLeaseBook()
	runID := "run-transition-snapshot"
	reserveTransitionRun(t, book, runID, fixture)
	original := transitionSnapshot(t, book, runID)
	if !sort.SliceIsSorted(original, func(left, right int) bool { return original[left].UnitID < original[right].UnitID }) {
		t.Fatalf("Snapshot() is not sorted: %#v", original)
	}
	mutated := append([]RoutingLease(nil), original...)
	mutated[0], mutated[1] = mutated[1], mutated[0]
	mutated[0].State = LeaseStateFailed
	mutated[0].StartedAtUnixMilli = fixture.now.UnixMilli() + 99
	got := transitionSnapshot(t, book, runID)
	if !reflect.DeepEqual(got, original) {
		t.Fatalf("Snapshot() returned aliased state: got=%#v want=%#v", got, original)
	}
}

func TestRoutingLeaseBookTransitionConcurrentStartSameUnitHasOneWinner(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	book := NewRoutingLeaseBook()
	runID := "run-concurrent-start"
	reserveTransitionRun(t, book, runID, fixture)
	assignment := transitionAssignment(t, fixture.decision, "unit-local")
	const contenders = 8
	ready := make(chan struct{}, contenders)
	barrier := make(chan struct{})
	results := make(chan error, contenders)
	var group sync.WaitGroup
	for range contenders {
		group.Go(func() {
			ready <- struct{}{}
			<-barrier
			_, err := book.Start(runID, assignment.UnitID, fixture.snapshot, fixture.now.Add(time.Millisecond))
			results <- err
		})
	}
	for range contenders {
		<-ready
	}
	close(barrier)
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent Start() winners = %d, want exactly one", winners)
	}
	state := transitionSnapshot(t, book, runID)
	if len(state) != 1 || state[0].State != LeaseStateStarted {
		t.Fatalf("concurrent Start() final state = %#v, want one started lease", state)
	}
}

func reserveTransitionRun(t *testing.T, book *RoutingLeaseBook, runID string, fixture leaseBookFixture) []RoutingLease {
	t.Helper()
	leases, err := book.Reserve(runID, fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("Reserve(%q) unexpected error: %v", runID, err)
	}
	return leases
}

func transitionAssignment(t *testing.T, decision RoutingDecision, unitID string) RouteAssignment {
	t.Helper()
	for _, assignment := range decision.Assignments {
		if assignment.UnitID == unitID {
			return assignment
		}
	}
	t.Fatalf("assignment %q not found in decision", unitID)
	return RouteAssignment{}
}

func transitionSnapshot(t *testing.T, book *RoutingLeaseBook, runID string) []RoutingLease {
	t.Helper()
	leases, err := book.Snapshot(runID)
	if err != nil {
		t.Fatalf("Snapshot(%q) unexpected error: %v", runID, err)
	}
	return leases
}

func assertTransitionLease(t *testing.T, got RoutingLease, assignment RouteAssignment, state RoutingLeaseState, reservedAt time.Time, startedAt, finishedAt int64) {
	t.Helper()
	assertTransitionLeaseAssignment(t, got, assignment)
	if got.State != state || got.ReservedAtUnixMilli != reservedAt.UnixMilli() || got.StartedAtUnixMilli != startedAt || got.FinishedAtUnixMilli != finishedAt {
		t.Fatalf("lease lifecycle = %#v, want state=%q reserved=%d started=%d finished=%d", got, state, reservedAt.UnixMilli(), startedAt, finishedAt)
	}
}

func assertTransitionLeaseAssignment(t *testing.T, got RoutingLease, assignment RouteAssignment) {
	t.Helper()
	if got.UnitID != assignment.UnitID || got.Executor != assignment.Executor || got.NodeID != assignment.NodeID || got.Endpoint != assignment.Endpoint || got.ProbeIdentity != assignment.ProbeIdentity || got.ProbeBootID != assignment.ProbeBootID || got.ECITier != assignment.ECITier {
		t.Fatalf("lease assignment = %#v, want %#v", got, assignment)
	}
}

func assertTransitionStartErrorDoesNotLeak(t *testing.T, err error, forbidden ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("invalid Start() unexpectedly succeeded")
	}
	message := err.Error()
	for _, value := range forbidden {
		if strings.Contains(message, value) {
			t.Fatalf("Start() error leaked forbidden value %q: %q", value, message)
		}
	}
}

func transitionState(leases []RoutingLease, unitID string) RoutingLeaseState {
	return transitionLeaseValue(leases, unitID).State
}

func transitionLease(t *testing.T, leases []RoutingLease, unitID string) RoutingLease {
	t.Helper()
	for _, lease := range leases {
		if lease.UnitID == unitID {
			return lease
		}
	}
	t.Fatalf("lease %q not found in snapshot", unitID)
	return RoutingLease{}
}

func transitionLeaseValue(leases []RoutingLease, unitID string) RoutingLease {
	for _, lease := range leases {
		if lease.UnitID == unitID {
			return lease
		}
	}
	return RoutingLease{}
}

func newLeaseBookMixedFullLocalFixture(t *testing.T) leaseBookFixture {
	t.Helper()
	fixture := newLeaseBookMixedFixture(t)
	fixture.units = append([]ExecutionUnit(nil), fixture.units...)
	fixture.units[0].CPUMilli = fixture.snapshot.Nodes[0].Available.CPUMilli
	fixture.units[0].MemoryBytes = fixture.snapshot.Nodes[0].Available.MemoryBytes
	plan, decision, err := PlanRoutingDecision(fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("PlanRoutingDecision(full local fixture) unexpected error: %v", err)
	}
	fixture.plan = plan
	fixture.decision = decision
	return fixture
}
