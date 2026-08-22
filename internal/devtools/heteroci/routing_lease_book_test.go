package heteroci

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"
)

const leaseBookGiB = int64(1 << 30)

type leaseBookFixture struct {
	now      time.Time
	ttl      time.Duration
	manifest Manifest
	snapshot SnapshotSet
	units    []ExecutionUnit
	plan     PackingPlan
	decision RoutingDecision
}

func TestRoutingLeaseBookPlanAndReserveValidatesPlanRouteAndLeases(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	book := NewRoutingLeaseBook()
	reserved, err := book.PlanAndReserve(
		"run-plan",
		fixture.manifest,
		fixture.snapshot,
		fixture.units,
		fixture.now,
		fixture.ttl,
	)
	if err != nil {
		t.Fatalf("PlanAndReserve() unexpected error: %v", err)
	}
	if !reflect.DeepEqual(reserved.SourceSnapshot, fixture.snapshot) {
		t.Fatalf("returned source snapshot is not an input-preserving copy: got=%#v want=%#v", reserved.SourceSnapshot, fixture.snapshot)
	}
	if err := reserved.Plan.ValidateAgainst(fixture.manifest, reserved.EffectiveSnapshot, fixture.units, fixture.now, fixture.ttl); err != nil {
		t.Fatalf("returned packing plan is invalid: %v", err)
	}
	if err := reserved.Decision.ValidateAgainst(fixture.manifest, reserved.EffectiveSnapshot, fixture.units, reserved.Plan, fixture.now, fixture.ttl); err != nil {
		t.Fatalf("returned routing decision is invalid: %v", err)
	}
	assertLeaseBookLeasesMatchDecision(t, reserved.Leases, reserved.Decision, fixture.now)
	got, err := book.Snapshot("run-plan")
	if err != nil {
		t.Fatalf("Snapshot() unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, reserved.Leases) {
		t.Fatalf("Snapshot() = %#v, want %#v", got, reserved.Leases)
	}
}

func TestRoutingLeaseBookReserveRejectsOversizeWithCapacityBlocker(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	fixture.units[0].CPUMilli = fixture.snapshot.Nodes[0].Available.CPUMilli + 1
	book := NewRoutingLeaseBook()
	if _, err := book.PlanAndReserve("run-reserve", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl); err == nil {
		t.Fatal("oversize PlanAndReserve unexpectedly succeeded")
	} else if _, ok := errors.AsType[*CapacityBlockerError](err); !ok {
		t.Fatalf("oversize PlanAndReserve error = %T %v, want CapacityBlockerError", err, err)
	}
}

func TestRoutingLeaseBookDuplicateRunIDDoesNotMutateState(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	book := NewRoutingLeaseBook()
	if _, err := book.Reserve("duplicate-run", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl); err != nil {
		t.Fatalf("initial Reserve() unexpected error: %v", err)
	}
	before := leaseBookSnapshot(t, book, "duplicate-run")
	assertLeaseBookLeasesMatchDecision(t, before, fixture.decision, fixture.now)
	if _, err := book.Reserve("duplicate-run", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now.Add(time.Millisecond), fixture.ttl); err == nil {
		t.Fatal("duplicate run_id Reserve() unexpectedly succeeded")
	}
	after := leaseBookSnapshot(t, book, "duplicate-run")
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("duplicate run_id mutated state: before=%#v after=%#v", before, after)
	}
}

func TestRoutingLeaseBookSerialFullPeakReserveRejectsSecondRunWithoutPartial(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	book := NewRoutingLeaseBook()
	if _, err := book.Reserve("run-serial-first", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl); err != nil {
		t.Fatalf("first Reserve() unexpected error: %v", err)
	}
	if _, err := book.Reserve("run-serial-second", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl); err == nil {
		t.Fatal("second full-peak Reserve() unexpectedly succeeded")
	}
	if _, err := book.Snapshot("run-serial-second"); err == nil {
		t.Fatal("rejected second run left a partial snapshot")
	}
	assertLeaseBookLeasesMatchDecision(t, leaseBookSnapshot(t, book, "run-serial-first"), fixture.decision, fixture.now)
}

func TestRoutingLeaseBookConcurrentFullPeakReserveHasOneWinner(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	book := NewRoutingLeaseBook()
	runIDs := []string{"run-concurrent-a", "run-concurrent-b"}
	winner, loser := concurrentLeaseBookReserveResults(t, book, fixture, runIDs)
	if winner.err != nil || winner.runID == "" || loser.err == nil || loser.runID == "" {
		t.Fatalf("concurrent Reserve() did not produce exactly one success: winner=%#v loser=%#v", winner, loser)
	}
	assertLeaseBookLeasesMatchDecision(t, winner.leases, fixture.decision, fixture.now)
	if _, err := book.Snapshot(loser.runID); err == nil {
		t.Fatalf("losing run %q left a partial snapshot", loser.runID)
	}
}

func TestRoutingLeaseBookPlanAndReserveReturnsCapacityBlocker(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	fixture.units[0].CPUMilli = fixture.snapshot.Nodes[0].Available.CPUMilli + 1
	book := NewRoutingLeaseBook()
	if _, err := book.PlanAndReserve("run-plan", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl); err == nil {
		t.Fatal("oversize PlanAndReserve unexpectedly succeeded")
	} else if _, ok := errors.AsType[*CapacityBlockerError](err); !ok {
		t.Fatalf("oversize PlanAndReserve error = %T %v, want CapacityBlockerError", err, err)
	}
}

func TestRoutingLeaseBookFixedWindowsUseRemainingNodeCapacity(t *testing.T) {
	fixture := newLeaseBookPlatformFixture(t, PlatformWindowsAMD64, RuntimeWindowsNative)
	book := NewRoutingLeaseBook()
	first, err := book.PlanAndReserve("run-platform-first", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("first PlanAndReserve() unexpected error: %v", err)
	}
	second, err := book.PlanAndReserve("run-platform-second", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("second PlanAndReserve() should use remaining Windows node: %v", err)
	}
	for _, plan := range []ReservedRoutingPlan{first, second} {
		if len(plan.Plan.NodeShards) != 1 || plan.Decision.Assignments[0].Executor != ExecutorKindLocalNode {
			t.Fatalf("fixed Windows plan is not local-only: %#v", plan)
		}
		assertLeaseBookLeasesMatchDecision(t, plan.Leases, plan.Decision, fixture.now)
	}
}

func TestRoutingLeaseBookPlanAndReserveCallerMutationDoesNotChangeSnapshotOrCapacity(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	book := NewRoutingLeaseBook()
	freshSnapshot := cloneSnapshotSet(fixture.snapshot)
	reserved, err := book.PlanAndReserve("run-copy", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("PlanAndReserve() unexpected error: %v", err)
	}
	sourceBefore := cloneSnapshotSet(fixture.snapshot)
	wantLeases := append([]RoutingLease(nil), reserved.Leases...)
	assertLeaseBookLeasesMatchDecision(t, reserved.Leases, reserved.Decision, fixture.now)
	assignment := mutateReservedRoutingPlan(t, &reserved)
	assertReservedPlanInputSnapshotUnchanged(t, fixture.snapshot, sourceBefore)
	assertReservedPlanSnapshotUnchanged(t, book, wantLeases)
	assertReservedPlanCapacityUnchanged(t, book, fixture)
	mutateLeaseBookCallerInputs(&fixture)
	assertReservedPlanTransitionsAreDetached(t, book, reserved, assignment, freshSnapshot, fixture.now)
}

func mutateReservedRoutingPlan(t *testing.T, reserved *ReservedRoutingPlan) RouteAssignment {
	t.Helper()
	assignment := reserved.Decision.Assignments[0]
	reserved.SourceSnapshot.Nodes[0].Available.CPUMilli = 1
	reserved.EffectiveSnapshot.Nodes[0].Available.MemoryBytes = 1
	reserved.Plan.NodeShards[0].Waves[0].UnitIDs[0] = "mutated-unit"
	reserved.Plan.UnassignedUnitIDs = append(reserved.Plan.UnassignedUnitIDs, "mutated-unit")
	reserved.Decision.Assignments[0].UnitID = "mutated-unit"
	reserved.Leases[0].UnitID = "mutated-unit"
	return assignment
}

func assertReservedPlanInputSnapshotUnchanged(t *testing.T, got, want SnapshotSet) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutating returned source snapshot polluted input snapshot: got=%#v want=%#v", got, want)
	}
}

func assertReservedPlanSnapshotUnchanged(t *testing.T, book *RoutingLeaseBook, want []RoutingLease) {
	t.Helper()
	got, err := book.Snapshot("run-copy")
	if err != nil {
		t.Fatalf("Snapshot() unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mutating returned wrapper polluted book snapshot: got=%#v want=%#v", got, want)
	}
}

func assertReservedPlanCapacityUnchanged(t *testing.T, book *RoutingLeaseBook, fixture leaseBookFixture) {
	t.Helper()
	second, err := book.PlanAndReserve("run-copy-second", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
	if err == nil {
		t.Fatalf("second PlanAndReserve unexpectedly succeeded after full local reservation: %#v", second)
	}
	if _, ok := errors.AsType[*CapacityBlockerError](err); !ok {
		t.Fatalf("second PlanAndReserve error = %T %v, want CapacityBlockerError", err, err)
	}
	if !reflect.DeepEqual(second, ReservedRoutingPlan{}) {
		t.Fatalf("second PlanAndReserve result = %#v, want zero result", second)
	}
}

func assertReservedPlanTransitionsAreDetached(t *testing.T, book *RoutingLeaseBook, reserved ReservedRoutingPlan, assignment RouteAssignment, currentSnapshot SnapshotSet, now time.Time) {
	t.Helper()
	if _, err := book.Start("run-copy", assignment.UnitID, currentSnapshot, now.Add(time.Second)); err != nil {
		t.Fatalf("Start() unexpected error: %v", err)
	}
	if reserved.Leases[0].State != LeaseStatePlanned {
		t.Fatalf("returned lease wrapper tracked Start() mutation: %#v", reserved.Leases[0])
	}
	if _, err := book.Finish("run-copy", assignment.UnitID, LeaseStateSucceeded, now.Add(2*time.Second)); err != nil {
		t.Fatalf("Finish() unexpected error: %v", err)
	}
	finished := leaseBookSnapshot(t, book, "run-copy")
	if finished[0].State != LeaseStateSucceeded {
		t.Fatalf("Snapshot() did not track completed transition: %#v", finished)
	}
	if reserved.Leases[0].State != LeaseStatePlanned {
		t.Fatalf("returned lease wrapper tracked Finish() mutation: %#v", reserved.Leases[0])
	}
}

func mutateLeaseBookCallerInputs(fixture *leaseBookFixture) {
	fixture.manifest.Nodes[0].NodeID = "mutated-node"
	fixture.manifest.Nodes[0].Capabilities[0].Runtime = RuntimeDarwinNative
	fixture.units[0].UnitID = "mutated-unit"
	fixture.units[0].RequiredRuntime = RuntimeDarwinNative
}

func TestRoutingLeaseBookReserveLaterNodeFailureLeavesNoPartial(t *testing.T) {
	fixture := newLeaseBookTwoNodeFixture(t)
	firstSnapshot := cloneSnapshotSet(fixture.snapshot)
	firstSnapshot.Nodes[0].Available = ResourceCapacity{CPUMilli: 1, MemoryBytes: 1, MaxConcurrency: 1}
	firstUnits := []ExecutionUnit{fixture.units[1]}
	firstPlan, firstDecision, err := PlanRoutingDecision(fixture.manifest, firstSnapshot, firstUnits, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("PlanRoutingDecision(first node) unexpected error: %v", err)
	}
	if len(firstDecision.Assignments) != 1 || firstDecision.Assignments[0].NodeID != fixture.manifest.Nodes[1].NodeID {
		t.Fatalf("first reservation did not target later node: %#v", firstDecision.Assignments)
	}
	book := NewRoutingLeaseBook()
	firstLeases, err := book.Reserve("run-node-two", firstDecision, fixture.manifest, fixture.snapshot, firstUnits, firstPlan, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("Reserve(first node) unexpected error: %v", err)
	}
	assertLeaseBookLeasesMatchDecision(t, firstLeases, firstDecision, fixture.now)
	if _, err := book.Reserve("run-both", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl); err == nil {
		t.Fatal("Reserve() unexpectedly succeeded after later node capacity was consumed")
	}
	if _, err := book.Snapshot("run-both"); err == nil {
		t.Fatal("later-node Reserve() left a partial run")
	}
	unitOne := []ExecutionUnit{fixture.units[0]}
	planOne, decisionOne, err := PlanRoutingDecision(fixture.manifest, fixture.snapshot, unitOne, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("PlanRoutingDecision(node one) unexpected error: %v", err)
	}
	if _, err := book.Reserve("run-node-one-after", decisionOne, fixture.manifest, fixture.snapshot, unitOne, planOne, fixture.now, fixture.ttl); err != nil {
		t.Fatalf("later-node failure partially consumed node one capacity: %v", err)
	}
	afterLeases := leaseBookSnapshot(t, book, "run-node-one-after")
	assertLeaseBookLeasesMatchDecision(t, afterLeases, decisionOne, fixture.now)
}

func TestRoutingLeaseBookConcurrentPlanAndReserveSameRunIDHasOneWinner(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	book := NewRoutingLeaseBook()
	results := concurrentLeaseBookPlanAndReserveResults(t, book, fixture, []string{"run-same", "run-same"})
	successes := 0
	for _, result := range results {
		if result.err == nil {
			successes++
			assertLeaseBookLeasesMatchDecision(t, result.reserved.Leases, result.reserved.Decision, fixture.now)
			continue
		}
		if !reflect.DeepEqual(result.reserved, ReservedRoutingPlan{}) {
			t.Fatalf("losing same-run result is not zero: %#v", result.reserved)
		}
	}
	if successes != 1 {
		t.Fatalf("same run_id competition produced %d successes, want exactly one: %#v", successes, results)
	}
	if _, err := book.Snapshot("run-same"); err != nil {
		t.Fatalf("winning same-run reservation missing: %v", err)
	}
}

func concurrentLeaseBookReserveResults(t *testing.T, book *RoutingLeaseBook, fixture leaseBookFixture, runIDs []string) (leaseBookReserveResult, leaseBookReserveResult) {
	t.Helper()
	results := make(chan leaseBookReserveResult, len(runIDs))
	ready := make(chan struct{}, len(runIDs))
	barrier := make(chan struct{})
	var group sync.WaitGroup
	for _, runID := range runIDs {
		group.Go(func() {
			ready <- struct{}{}
			<-barrier
			leases, err := book.Reserve(runID, fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl)
			results <- leaseBookReserveResult{runID: runID, leases: leases, err: err}
		})
	}
	<-ready
	<-ready
	close(barrier)
	group.Wait()
	close(results)
	return classifyConcurrentLeaseBookResults(t, results)
}

func classifyConcurrentLeaseBookResults(t *testing.T, results <-chan leaseBookReserveResult) (leaseBookReserveResult, leaseBookReserveResult) {
	t.Helper()
	var winner, loser leaseBookReserveResult
	for result := range results {
		if result.err == nil {
			if winner.err == nil && winner.runID != "" {
				t.Fatalf("both concurrent Reserve() calls succeeded: %#v and %#v", winner, result)
			}
			winner = result
			continue
		}
		if loser.err == nil && loser.runID != "" {
			t.Fatalf("more than one concurrent Reserve() call failed unexpectedly: %#v and %#v", loser, result)
		}
		loser = result
	}
	return winner, loser
}

func TestRoutingLeaseBookDifferentProbeBootIDsDoNotShareLocalCapacityKey(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	book := NewRoutingLeaseBook()
	oldReserved, err := book.PlanAndReserve("run-old-boot", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("PlanAndReserve(old boot) unexpected error: %v", err)
	}
	newSnapshot := cloneSnapshotSet(fixture.snapshot)
	newSnapshot.Nodes[0].ProbeBootID = "boot-new-generation"
	if err := newSnapshot.ValidateAgainst(fixture.manifest, fixture.now, fixture.ttl); err != nil {
		t.Fatalf("new probe boot snapshot is not valid: %v", err)
	}
	newReserved, err := book.PlanAndReserve("run-new-boot", fixture.manifest, newSnapshot, fixture.units, fixture.now, fixture.ttl)
	if err != nil {
		t.Fatalf("PlanAndReserve(new boot) unexpectedly conflicted with old boot: %v", err)
	}
	if len(oldReserved.Plan.NodeShards) == 0 || len(newReserved.Plan.NodeShards) == 0 {
		t.Fatalf("old/new boot plans unexpectedly lost local placement: old=%#v new=%#v", oldReserved.Plan, newReserved.Plan)
	}
	assertLeaseBookLeasesMatchDecision(t, oldReserved.Leases, oldReserved.Decision, fixture.now)
	assertLeaseBookLeasesMatchDecision(t, newReserved.Leases, newReserved.Decision, fixture.now)
	if newReserved.EffectiveSnapshot.Nodes[0].Available != fixture.snapshot.Nodes[0].Available {
		t.Fatalf("new boot effective snapshot shared old reservation: got=%#v want=%#v", newReserved.EffectiveSnapshot.Nodes[0].Available, fixture.snapshot.Nodes[0].Available)
	}
}

func TestRoutingLeaseBookSnapshotIsDeepCopyAndSorted(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	book := NewRoutingLeaseBook()
	if _, err := book.Reserve("run-snapshot", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl); err != nil {
		t.Fatalf("Reserve() unexpected error: %v", err)
	}
	original := leaseBookSnapshot(t, book, "run-snapshot")
	assertLeaseBookLeasesMatchDecision(t, original, fixture.decision, fixture.now)
	if !sort.SliceIsSorted(original, func(left, right int) bool { return original[left].UnitID < original[right].UnitID }) {
		t.Fatalf("Snapshot() is not sorted by unit_id: %#v", original)
	}
	expected := append([]RoutingLease(nil), original...)
	original[0], original[1] = original[1], original[0]
	original[0].State = LeaseStateFailed
	got, err := book.Snapshot("run-snapshot")
	if err != nil {
		t.Fatalf("Snapshot(second) unexpected error: %v", err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("Snapshot() returned aliased or unsorted state: got=%#v want=%#v", got, expected)
	}
}

func TestRoutingLeaseBookRejectsCapacityOverflow(t *testing.T) {
	fixture := newLeaseBookSingleLocalFixture(t)
	fixture.units[0].CPUMilli = fixture.snapshot.Nodes[0].Available.CPUMilli + 1
	book := NewRoutingLeaseBook()
	if _, err := book.PlanAndReserve("run-overflow", fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl); err == nil {
		t.Fatal("capacity overflow Reserve unexpectedly succeeded")
	} else if _, ok := errors.AsType[*CapacityBlockerError](err); !ok {
		t.Fatalf("capacity overflow error = %T %v, want CapacityBlockerError", err, err)
	}
}

func TestRoutingLeaseBookReserveErrorsDoNotPartiallyWrite(t *testing.T) {
	fixture := newLeaseBookMixedFixture(t)
	book := NewRoutingLeaseBook()
	invalid := fixture.decision
	invalid.Assignments = append([]RouteAssignment(nil), fixture.decision.Assignments...)
	invalid.Assignments[0].UnitID = "unit-unknown"
	if _, err := book.Reserve("run-invalid", invalid, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl); err == nil {
		t.Fatal("Reserve() unexpectedly accepted invalid assignment coverage")
	}
	if _, err := book.Snapshot("run-invalid"); err == nil {
		t.Fatal("invalid Reserve() left a partial run snapshot")
	}
	if _, err := book.Reserve("", fixture.decision, fixture.manifest, fixture.snapshot, fixture.units, fixture.plan, fixture.now, fixture.ttl); err == nil {
		t.Fatal("Reserve() unexpectedly accepted empty run_id")
	}
}

type leaseBookReserveResult struct {
	runID  string
	leases []RoutingLease
	err    error
}

type reservedPlanResult struct {
	runID    string
	reserved ReservedRoutingPlan
	err      error
}

func concurrentLeaseBookPlanAndReserveResults(t *testing.T, book *RoutingLeaseBook, fixture leaseBookFixture, runIDs []string) []reservedPlanResult {
	t.Helper()
	results := make(chan reservedPlanResult, len(runIDs))
	ready := make(chan struct{}, len(runIDs))
	barrier := make(chan struct{})
	var group sync.WaitGroup
	for _, runID := range runIDs {
		group.Go(func() {
			ready <- struct{}{}
			<-barrier
			reserved, err := book.PlanAndReserve(runID, fixture.manifest, fixture.snapshot, fixture.units, fixture.now, fixture.ttl)
			results <- reservedPlanResult{runID: runID, reserved: reserved, err: err}
		})
	}
	for range runIDs {
		<-ready
	}
	close(barrier)
	group.Wait()
	close(results)
	ordered := make([]reservedPlanResult, 0, len(runIDs))
	for result := range results {
		ordered = append(ordered, result)
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].runID < ordered[right].runID })
	return ordered
}

func newLeaseBookMixedFixture(t *testing.T) leaseBookFixture {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	ttl := time.Minute
	manifest := testManifest(1)
	snapshot := snapshotForManifest(manifest, now)
	snapshot.Nodes[0].Available = ResourceCapacity{CPUMilli: 2_000, MemoryBytes: 4 * leaseBookGiB, MaxConcurrency: 1}
	units := []ExecutionUnit{
		{UnitID: "unit-local", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 500, MemoryBytes: 64 * 1024 * 1024},
		{UnitID: "unit-second", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: 2_000, CPUMilli: 500, MemoryBytes: 64 * 1024 * 1024},
	}
	plan, decision, err := PlanRoutingDecision(manifest, snapshot, units, now, ttl)
	if err != nil {
		t.Fatalf("PlanRoutingDecision(mixed fixture) unexpected error: %v", err)
	}
	return leaseBookFixture{now: now, ttl: ttl, manifest: manifest, snapshot: snapshot, units: units, plan: plan, decision: decision}
}

func newLeaseBookSingleLocalFixture(t *testing.T) leaseBookFixture {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	ttl := time.Minute
	manifest := testManifest(1)
	snapshot := snapshotForManifest(manifest, now)
	snapshot.Nodes[0].Available = ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 * leaseBookGiB, MaxConcurrency: 1}
	for index := 1; index < len(snapshot.Nodes); index++ {
		snapshot.Nodes[index].Available = ResourceCapacity{}
	}
	units := []ExecutionUnit{{UnitID: "unit-local", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 4_000, MemoryBytes: 8 * leaseBookGiB}}
	plan, decision, err := PlanRoutingDecision(manifest, snapshot, units, now, ttl)
	if err != nil {
		t.Fatalf("PlanRoutingDecision(local fixture) unexpected error: %v", err)
	}
	return leaseBookFixture{now: now, ttl: ttl, manifest: manifest, snapshot: snapshot, units: units, plan: plan, decision: decision}
}

func newLeaseBookPlatformFixture(t *testing.T, platform Platform, runtime RuntimeKind) leaseBookFixture {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	ttl := time.Minute
	manifest := testManifest(2)
	manifest.Nodes[0].Capabilities = append([]CapabilityConfig(nil), manifest.Nodes[0].Capabilities...)
	manifest.Nodes[1].Platform = platform
	manifest.Nodes[1].Capabilities = []CapabilityConfig{{Runtime: runtime}}
	snapshot := snapshotForManifest(manifest, now)
	snapshot.Nodes[1].Available = ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 * leaseBookGiB, MaxConcurrency: 1}
	unit := ExecutionUnit{UnitID: "unit-platform", TargetPlatform: platform, RequiredRuntime: runtime, EstimatedDurationMS: 1_000, CPUMilli: 4_000, MemoryBytes: 8 * leaseBookGiB}
	plan, decision, err := PlanRoutingDecision(manifest, snapshot, []ExecutionUnit{unit}, now, ttl)
	if err != nil {
		t.Fatalf("PlanRoutingDecision(%s fixture) unexpected error: %v", platform, err)
	}
	return leaseBookFixture{now: now, ttl: ttl, manifest: manifest, snapshot: snapshot, units: []ExecutionUnit{unit}, plan: plan, decision: decision}
}

func newLeaseBookTwoNodeFixture(t *testing.T) leaseBookFixture {
	t.Helper()
	now := time.UnixMilli(1_700_000_000_000).UTC()
	ttl := time.Minute
	manifest := testManifest(2)
	snapshot := snapshotForManifest(manifest, now)
	for index := range snapshot.Nodes {
		snapshot.Nodes[index].Available = ResourceCapacity{CPUMilli: 4_000, MemoryBytes: 8 * leaseBookGiB, MaxConcurrency: 1}
	}
	units := []ExecutionUnit{
		{UnitID: "unit-node-one", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 4_000, MemoryBytes: 8 * leaseBookGiB},
		{UnitID: "unit-node-two", TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: 1_000, CPUMilli: 4_000, MemoryBytes: 8 * leaseBookGiB},
	}
	plan, decision, err := PlanRoutingDecision(manifest, snapshot, units, now, ttl)
	if err != nil {
		t.Fatalf("PlanRoutingDecision(two-node fixture) unexpected error: %v", err)
	}
	return leaseBookFixture{now: now, ttl: ttl, manifest: manifest, snapshot: snapshot, units: units, plan: plan, decision: decision}
}

func assertLeaseBookLeasesMatchDecision(t *testing.T, leases []RoutingLease, decision RoutingDecision, now time.Time) {
	t.Helper()
	if len(leases) != len(decision.Assignments) {
		t.Fatalf("lease count = %d, want %d", len(leases), len(decision.Assignments))
	}
	if !sort.SliceIsSorted(leases, func(left, right int) bool { return leases[left].UnitID < leases[right].UnitID }) {
		t.Fatalf("leases are not sorted: %#v", leases)
	}
	for index, assignment := range decision.Assignments {
		assertLeaseBookLease(t, index, leases[index], assignment, decision.DecisionDigest, now.UnixMilli())
	}
}

func assertLeaseBookLease(t *testing.T, index int, lease RoutingLease, assignment RouteAssignment, decisionDigest string, reservedAtUnixMilli int64) {
	t.Helper()
	assertLeaseBookAssignment(t, index, lease, assignment)
	assertLeaseBookState(t, index, lease, decisionDigest, reservedAtUnixMilli)
}

func assertLeaseBookAssignment(t *testing.T, index int, lease RoutingLease, assignment RouteAssignment) {
	t.Helper()
	if lease.UnitID != assignment.UnitID || lease.Executor != assignment.Executor || lease.NodeID != assignment.NodeID || lease.Endpoint != assignment.Endpoint || lease.ProbeIdentity != assignment.ProbeIdentity || lease.ProbeBootID != assignment.ProbeBootID {
		t.Fatalf("lease[%d] = %#v, want exact assignment %#v", index, lease, assignment)
	}
}

func assertLeaseBookLocalIdentity(t *testing.T, lease RoutingLease, config NodeConfig, snapshot NodeSnapshot) {
	t.Helper()
	if lease.Executor != ExecutorKindLocalNode || lease.NodeID != config.NodeID || lease.Endpoint != config.Endpoint || lease.ProbeIdentity != config.ProbeIdentity || lease.ProbeBootID != snapshot.ProbeBootID {
		t.Fatalf("local lease does not preserve exact assignment: %#v", lease)
	}
}

func assertLeaseBookState(t *testing.T, index int, lease RoutingLease, decisionDigest string, reservedAtUnixMilli int64) {
	t.Helper()
	if lease.SchemaVersion != RoutingLeaseSchemaVersion || lease.Authority != AuthorityLocalNonAuthoritative || lease.DecisionDigest != decisionDigest || lease.State != LeaseStatePlanned || lease.ReservedAtUnixMilli != reservedAtUnixMilli {
		t.Fatalf("lease[%d] identity/state = %#v", index, lease)
	}
	if lease.StartedAtUnixMilli != 0 || lease.FinishedAtUnixMilli != 0 {
		t.Fatalf("planned lease[%d] has terminal timestamps: %#v", index, lease)
	}
}

func leaseBookSnapshot(t *testing.T, book *RoutingLeaseBook, runID string) []RoutingLease {
	t.Helper()
	leases, err := book.Snapshot(runID)
	if err != nil {
		t.Fatalf("Snapshot(%q) unexpected error: %v", runID, err)
	}
	return leases
}
