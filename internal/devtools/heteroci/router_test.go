package heteroci

import (
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestBuildRoutingDecisionAllLocalAssignmentsAreExactAndCanonical(t *testing.T) {
	manifest := testManifest(2)
	now := snapshotTestNow()
	snapshot := snapshotForManifest(manifest, now)
	units := []ExecutionUnit{
		routerTestLinuxUnit("local-b", 500, 256*1024*1024),
		routerTestLinuxUnit("local-a", 750, 512*1024*1024),
	}

	plan, decision := routerTestPlan(t, manifest, snapshot, units, now)
	if decision.SchemaVersion != "heteroci-routing-decision/v4" {
		t.Fatalf("routing decision schema = %q, want heteroci-routing-decision/v4", decision.SchemaVersion)
	}
	if len(plan.UnassignedUnitIDs) != 0 {
		t.Fatalf("all-local plan has unassigned units: %#v", plan.UnassignedUnitIDs)
	}
	if len(decision.Assignments) != len(units) {
		t.Fatalf("assignment count = %d, want %d", len(decision.Assignments), len(units))
	}
	routerTestAssertLocalAssignments(t, decision, snapshot)
	if err := decision.ValidateAgainst(manifest, snapshot, units, plan, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("RoutingDecision.ValidateAgainst() = %v", err)
	}
	built, err := BuildRoutingDecision(manifest, snapshot, units, plan, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("BuildRoutingDecision() = %v", err)
	}
	if !reflect.DeepEqual(built, decision) {
		t.Fatalf("BuildRoutingDecision() = %#v, PlanRoutingDecision() = %#v", built, decision)
	}
}

func TestRoutingDecisionRejectsLocalIdentityDriftAndDigestMutation(t *testing.T) {
	manifest := testManifest(1)
	now := snapshotTestNow()
	snapshot := snapshotForManifest(manifest, now)
	units := []ExecutionUnit{routerTestLinuxUnit("local", 500, 256*1024*1024)}
	plan, decision := routerTestPlan(t, manifest, snapshot, units, now)
	baselineDigest := decision.DecisionDigest
	cases := []struct {
		name   string
		mutate func(*RouteAssignment)
	}{
		{name: "node id", mutate: func(assignment *RouteAssignment) { assignment.NodeID = "heteroci-other" }},
		{name: "endpoint", mutate: func(assignment *RouteAssignment) { assignment.Endpoint = "https://other.invalid/runner" }},
		{name: "probe identity", mutate: func(assignment *RouteAssignment) { assignment.ProbeIdentity = snapshotTestWrongProbeIdentity }},
		{name: "probe boot id", mutate: func(assignment *RouteAssignment) { assignment.ProbeBootID = "boot-other" }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := decision
			candidate.Assignments = append([]RouteAssignment(nil), decision.Assignments...)
			testCase.mutate(&candidate.Assignments[0])
			candidateDigest, err := candidate.CanonicalDigest()
			if err != nil {
				t.Fatalf("mutated decision digest: %v", err)
			}
			if candidateDigest == baselineDigest {
				t.Fatalf("decision digest did not change after %s mutation", testCase.name)
			}
			if err := candidate.ValidateAgainst(manifest, snapshot, units, plan, now, snapshotTestHeartbeatTTL); err == nil {
				t.Fatalf("ValidateAgainst accepted local %s drift", testCase.name)
			}
		})
	}
}

func TestBuildRoutingDecisionRejectsOversizeAsCapacityBlocker(t *testing.T) {
	manifest, snapshot, now := routerTestZeroCapacityLinux(t)
	oversize := []ExecutionUnit{routerTestLinuxUnit("too-large", 8_001, 16*1024*1024*1024)}
	plan, decision, err := PlanRoutingDecision(manifest, snapshot, oversize, now, snapshotTestHeartbeatTTL)
	routerTestAssertCapacityBlocker(t, err, decision)
	if len(plan.UnassignedUnitIDs) != 1 || plan.UnassignedUnitIDs[0] != "too-large" {
		t.Fatalf("oversize plan = %#v, want replayable unassigned plan", plan)
	}
	if blockerErr := routerTestCapacityBlockerError(t, err); blockerErr.ValidateAgainstPlan(plan) != nil {
		t.Fatalf("oversize blocker digest does not replay against plan: %v", blockerErr)
	}

	plan, decision, err = PlanRoutingDecision(manifest, snapshot, []ExecutionUnit{routerTestLinuxUnit("linux", 5_000, 9*1024*1024*1024)}, now, snapshotTestHeartbeatTTL)
	routerTestAssertCapacityBlocker(t, err, decision)
	if len(plan.UnassignedUnitIDs) != 1 || plan.UnassignedUnitIDs[0] != "linux" {
		t.Fatalf("second capacity blocker plan = %#v, want replayable unassigned plan", plan)
	}
}

func TestBuildRoutingDecisionPreservesPlanForAnyCapacityBlocker(t *testing.T) {
	manifest := anchorWaveRoutingManifest()
	now := snapshotTestNow()
	snapshot := snapshotForManifest(manifest, now)
	for index := range snapshot.Nodes {
		if index == 1 {
			snapshot.Nodes[index].Available = ResourceCapacity{}
		}
	}
	units := []ExecutionUnit{
		routerTestLinuxUnit("local-linux", 500, 256*1024*1024),
		{UnitID: "blocked-wine", TargetPlatform: PlatformWindowsAMD64, RequiredRuntime: RuntimeUbuntuWineContainer, EstimatedDurationMS: 100, CPUMilli: 500, MemoryBytes: 256 * 1024 * 1024},
	}

	plan, decision, err := PlanRoutingDecision(manifest, snapshot, units, now, snapshotTestHeartbeatTTL)
	routerTestAssertCapacityBlocker(t, err, decision)
	if len(plan.UnassignedUnitIDs) != 1 || plan.UnassignedUnitIDs[0] != "blocked-wine" || len(plan.NodeShards) == 0 {
		t.Fatalf("partial-capacity plan = %#v, want assigned and unassigned replayable plan", plan)
	}
	blockerErr := routerTestCapacityBlockerError(t, err)
	if blockerErr.ValidateAgainstPlan(plan) != nil {
		t.Fatalf("partial blocker digest does not replay against plan: %v", blockerErr)
	}
	if len(blockerErr.Blockers) != 1 || blockerErr.Blockers[0].UnitID != "blocked-wine" {
		t.Fatalf("capacity blocker error = %#v, want only blocked-wine", err)
	}
}

func TestBuildRoutingDecisionCapacityBlockersAreSortedAndInputOrderIndependent(t *testing.T) {
	manifest, snapshot, now := routerTestZeroCapacityLinux(t)
	first := []ExecutionUnit{
		routerTestLinuxUnit("z-linux", 500, 256*1024*1024),
		routerTestLinuxUnit("a-linux", 500, 256*1024*1024),
	}
	second := []ExecutionUnit{first[1], first[0]}
	firstPlan, firstDecision, firstErr := PlanRoutingDecision(manifest, snapshot, first, now, snapshotTestHeartbeatTTL)
	secondPlan, secondDecision, secondErr := PlanRoutingDecision(manifest, snapshot, second, now, snapshotTestHeartbeatTTL)
	routerTestAssertCapacityBlocker(t, firstErr, firstDecision)
	routerTestAssertCapacityBlocker(t, secondErr, secondDecision)
	if firstErr.Error() != secondErr.Error() {
		t.Fatalf("capacity blocker error changed with input order:\nfirst=%v\nsecond=%v", firstErr, secondErr)
	}
	firstDigest, err := firstPlan.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	secondDigest, err := secondPlan.CanonicalDigest()
	if err != nil || firstDigest != secondDigest {
		t.Fatalf("capacity blocker plan digest changed with input order: %q/%q err=%v", firstDigest, secondDigest, err)
	}
	if blockerErr := routerTestCapacityBlockerError(t, firstErr); blockerErr.PackingPlanDigest != firstDigest || blockerErr.ValidateAgainstPlan(firstPlan) != nil {
		t.Fatalf("first blocker does not bind canonical plan: %#v", blockerErr)
	}
	blockers := routerTestCapacityBlockerError(t, firstErr)
	ids := []string{blockers.Blockers[0].UnitID, blockers.Blockers[1].UnitID}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("capacity blockers are not sorted: %#v", ids)
	}
}

func TestPlanRoutingDecisionIsDeterministicAcrossInputManifestSnapshotOrder(t *testing.T) {
	manifest := testManifest(2)
	now := snapshotTestNow()
	snapshot := snapshotForManifest(manifest, now)
	units := []ExecutionUnit{
		routerTestLinuxUnit("unit-b", 750, 512*1024*1024),
		routerTestLinuxUnit("unit-a", 500, 256*1024*1024),
	}
	firstPlan, firstDecision := routerTestPlan(t, manifest, snapshot, units, now)

	shuffledManifest := manifest
	shuffledManifest.Nodes = append([]NodeConfig(nil), manifest.Nodes...)
	shuffledManifest.Nodes[0], shuffledManifest.Nodes[1] = shuffledManifest.Nodes[1], shuffledManifest.Nodes[0]
	shuffledSnapshot := cloneSnapshotSet(snapshot)
	shuffledSnapshot.Nodes[0], shuffledSnapshot.Nodes[1] = shuffledSnapshot.Nodes[1], shuffledSnapshot.Nodes[0]
	shuffledUnits := []ExecutionUnit{units[1], units[0]}
	secondPlan, secondDecision := routerTestPlan(t, shuffledManifest, shuffledSnapshot, shuffledUnits, now)

	if !reflect.DeepEqual(firstPlan, secondPlan) {
		t.Fatalf("packing plan changed after input reorder:\nfirst=%#v\nsecond=%#v", firstPlan, secondPlan)
	}
	if !reflect.DeepEqual(firstDecision, secondDecision) {
		t.Fatalf("routing decision changed after input reorder:\nfirst=%#v\nsecond=%#v", firstDecision, secondDecision)
	}
	firstPackingDigest, err := firstPlan.CanonicalDigest()
	if err != nil {
		t.Fatalf("first plan digest: %v", err)
	}
	secondPackingDigest, err := secondPlan.CanonicalDigest()
	if err != nil {
		t.Fatalf("second plan digest: %v", err)
	}
	if firstPackingDigest != secondPackingDigest || firstDecision.PackingPlanDigest != firstPackingDigest || firstDecision.DecisionDigest == "" {
		t.Fatalf("unstable or incomplete digests: plan=%q/%q decision=%#v", firstPackingDigest, secondPackingDigest, firstDecision)
	}
	canonicalDecisionDigest, err := firstDecision.CanonicalDigest()
	if err != nil {
		t.Fatalf("decision canonical digest: %v", err)
	}
	if firstDecision.DecisionDigest != canonicalDecisionDigest {
		t.Fatalf("decision digest = %q, want %q", firstDecision.DecisionDigest, canonicalDecisionDigest)
	}
}

func TestBuildRoutingDecisionRejectsPackingPseudoUnassignedThatFits(t *testing.T) {
	manifest := testManifest(1)
	now := snapshotTestNow()
	snapshot := snapshotForManifest(manifest, now)
	units := []ExecutionUnit{routerTestLinuxUnit("fits", 500, 256*1024*1024)}
	plan := routerTestAnchorPlan(t, manifest, snapshot, units, now)
	plan.NodeShards = []NodeShard{}
	plan.UnassignedUnitIDs = []string{"fits"}
	if err := plan.ValidateAgainst(manifest, snapshot, units, now, snapshotTestHeartbeatTTL); err == nil || !strings.Contains(err.Error(), "fits a live node") {
		t.Fatalf("pseudo-unassigned plan error = %v, want live-node fit rejection", err)
	}
}

func TestBuildRoutingDecisionOfflineCapacityReturnsBlockerWhenAllMatchingNodesOffline(t *testing.T) {
	manifest := testManifest(4)
	now := snapshotTestNow()
	snapshot := snapshotForManifest(manifest, now)
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].Platform == PlatformLinuxAMD64 {
			routerTestMarkOffline(&snapshot.Nodes[index])
		}
	}
	unit := []ExecutionUnit{routerTestLinuxUnit("linux", 500, 256*1024*1024)}
	plan, decision, err := PlanRoutingDecision(manifest, snapshot, unit, now, snapshotTestHeartbeatTTL)
	routerTestAssertCapacityBlocker(t, err, decision)
	if len(plan.UnassignedUnitIDs) != 1 || plan.UnassignedUnitIDs[0] != "linux" {
		t.Fatalf("offline Linux plan = %#v, want replayable unassigned plan", plan)
	}
	if blockerErr := routerTestCapacityBlockerError(t, err); blockerErr.ValidateAgainstPlan(plan) != nil {
		t.Fatalf("offline blocker digest does not replay against plan: %v", blockerErr)
	}
}

func TestCapacityBlockerPlanDigestReplaysAcrossResourceDimensions(t *testing.T) {
	cases := []struct {
		name     string
		capacity ResourceCapacity
		unit     ExecutionUnit
	}{
		{name: "cpu", capacity: ResourceCapacity{CPUMilli: 100, MemoryBytes: 1 << 30, MaxConcurrency: 1}, unit: routerTestLinuxUnit("cpu-blocked", 101, 256*1024*1024)},
		{name: "memory", capacity: ResourceCapacity{CPUMilli: 1_000, MemoryBytes: 100, MaxConcurrency: 1}, unit: routerTestLinuxUnit("memory-blocked", 100, 101)},
		{name: "concurrency", capacity: ResourceCapacity{CPUMilli: 1_000, MemoryBytes: 1 << 30, MaxConcurrency: 0}, unit: routerTestLinuxUnit("concurrency-blocked", 100, 256*1024*1024)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { routerTestAssertResourceBlocker(t, tc.capacity, tc.unit) })
	}
}

func routerTestAssertResourceBlocker(t *testing.T, capacity ResourceCapacity, unit ExecutionUnit) {
	t.Helper()
	manifest := testManifest(1)
	now := snapshotTestNow()
	snapshot := snapshotForManifest(manifest, now)
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].Platform == PlatformLinuxAMD64 {
			snapshot.Nodes[index].Available = capacity
		}
	}
	plan, decision, err := PlanRoutingDecision(manifest, snapshot, []ExecutionUnit{unit}, now, snapshotTestHeartbeatTTL)
	routerTestAssertCapacityBlocker(t, err, decision)
	if len(plan.UnassignedUnitIDs) != 1 || plan.UnassignedUnitIDs[0] != unit.UnitID {
		t.Fatalf("resource blocker plan = %#v", plan)
	}
	blockerErr := routerTestCapacityBlockerError(t, err)
	if blockerErr.ValidateAgainstPlan(plan) != nil {
		t.Fatalf("resource blocker digest does not replay: %v", blockerErr)
	}
}

func TestPlanRoutingDecisionRejectsManifestWithoutNodes(t *testing.T) {
	manifest := Manifest{SchemaVersion: ManifestSchemaVersion, Authority: AuthorityLocalNonAuthoritative}
	unit := []ExecutionUnit{routerTestLinuxUnit("unit", 500, 256*1024*1024)}
	plan, decision, err := PlanRoutingDecision(manifest, SnapshotSet{}, unit, snapshotTestNow(), snapshotTestHeartbeatTTL)
	if err == nil || !reflect.DeepEqual(plan, PackingPlan{}) || !reflect.DeepEqual(decision, RoutingDecision{}) {
		t.Fatalf("empty-node route = plan:%#v decision:%#v err:%v, want fail-fast zero values", plan, decision, err)
	}
}

func TestRouterHasNoFallbackOrSecondExecutorSurface(t *testing.T) {
	assignmentType := reflect.TypeFor[RouteAssignment]()
	forbiddenFields := []string{"Fallback", "FallbackExecutor", "SecondExecutor", "ExecutorChain"}
	for _, fieldName := range forbiddenFields {
		if _, found := assignmentType.FieldByName(fieldName); found {
			t.Fatalf("route assignment exposes forbidden fallback/second-executor field %q", fieldName)
		}
	}
	if ExecutorKindLocalNode != "local_node" {
		t.Fatalf("unexpected local executor kind %q", ExecutorKindLocalNode)
	}
}

func routerTestPlan(t *testing.T, manifest Manifest, snapshot SnapshotSet, units []ExecutionUnit, now time.Time) (PackingPlan, RoutingDecision) {
	t.Helper()
	plan, decision, err := PlanRoutingDecision(manifest, snapshot, units, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("PlanRoutingDecision() = %v", err)
	}
	if err := plan.ValidateAgainst(manifest, snapshot, units, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("PackingPlan.ValidateAgainst() = %v", err)
	}
	if err := decision.ValidateAgainst(manifest, snapshot, units, plan, now, snapshotTestHeartbeatTTL); err != nil {
		t.Fatalf("RoutingDecision.ValidateAgainst() = %v", err)
	}
	return plan, decision
}

func routerTestAnchorPlan(t *testing.T, manifest Manifest, snapshot SnapshotSet, units []ExecutionUnit, now time.Time) PackingPlan {
	t.Helper()
	plan, err := BuildAnchorWavePlan(manifest, snapshot, units, now, snapshotTestHeartbeatTTL)
	if err != nil {
		t.Fatalf("BuildAnchorWavePlan() = %v", err)
	}
	return plan
}

func routerTestZeroCapacityLinux(t *testing.T) (Manifest, SnapshotSet, time.Time) {
	t.Helper()
	manifest := testManifest(1)
	now := snapshotTestNow()
	snapshot := snapshotForManifest(manifest, now)
	for index := range snapshot.Nodes {
		if snapshot.Nodes[index].Platform == PlatformLinuxAMD64 {
			snapshot.Nodes[index].Available = ResourceCapacity{}
		}
	}
	return manifest, snapshot, now
}

func routerTestLinuxUnit(id string, cpu, memory int64) ExecutionUnit {
	return ExecutionUnit{UnitID: id, TargetPlatform: PlatformLinuxAMD64, RequiredRuntime: RuntimeUbuntuContainer, EstimatedDurationMS: 100, CPUMilli: cpu, MemoryBytes: memory}
}

func routerTestAssertLocalAssignments(t *testing.T, decision RoutingDecision, snapshot SnapshotSet) {
	t.Helper()
	seen := make(map[string]struct{}, len(decision.Assignments))
	previousUnitID := ""
	for index, assignment := range decision.Assignments {
		if index > 0 && assignment.UnitID <= previousUnitID {
			t.Fatalf("assignments are not strictly sorted: %#v", decision.Assignments)
		}
		if _, exists := seen[assignment.UnitID]; exists {
			t.Fatalf("assignment %q appears more than once", assignment.UnitID)
		}
		seen[assignment.UnitID] = struct{}{}
		if assignment.Executor != ExecutorKindLocalNode || assignment.NodeID == "" {
			t.Fatalf("assignment %d is not a local-only binding: %#v", index, assignment)
		}
		routerTestAssertLocalAssignment(t, assignment, snapshot)
		previousUnitID = assignment.UnitID
	}
}

func routerTestAssertLocalAssignment(t *testing.T, assignment RouteAssignment, snapshot SnapshotSet) {
	t.Helper()
	for _, node := range snapshot.Nodes {
		if node.NodeID != assignment.NodeID {
			continue
		}
		if assignment.Endpoint != node.Endpoint || assignment.ProbeIdentity != node.ProbeIdentity || assignment.ProbeBootID != node.ProbeBootID {
			t.Fatalf("local assignment %q identity = %#v, want snapshot node %#v", assignment.UnitID, assignment, node)
		}
		return
	}
	t.Fatalf("assignment %q references unknown local node %q", assignment.UnitID, assignment.NodeID)
}

func routerTestAssertCapacityBlocker(t *testing.T, err error, decision RoutingDecision) {
	t.Helper()
	if err == nil {
		t.Fatal("route unexpectedly succeeded; want CapacityBlockerError")
	}
	blockerErr := routerTestCapacityBlockerError(t, err)
	if len(blockerErr.Blockers) == 0 {
		t.Fatalf("capacity blocker list is empty: %v", err)
	}
	if !reflect.DeepEqual(decision, RoutingDecision{}) {
		t.Fatalf("decision on blocker = %#v, want zero decision", decision)
	}
	for _, blocker := range blockerErr.Blockers {
		if blocker.UnitID == "" || blocker.TargetPlatform == "" || blocker.RequiredRuntime == "" || blocker.ReasonCode == "" || blocker.PackingPlanDigest == "" {
			t.Fatalf("incomplete capacity blocker: %#v", blocker)
		}
	}
}

func routerTestCapacityBlockerError(t *testing.T, err error) CapacityBlockerError {
	t.Helper()
	if value, ok := errors.AsType[CapacityBlockerError](err); ok {
		return value
	}
	if pointer, ok := errors.AsType[*CapacityBlockerError](err); ok && pointer != nil {
		return *pointer
	}
	t.Fatalf("route error type = %T (%v), want CapacityBlockerError", err, err)
	return CapacityBlockerError{}
}

func routerTestMarkOffline(node *NodeSnapshot) {
	node.Status = NodeStatusOffline
	node.ProbeBootID = ""
	node.ActiveCapabilities = []ActiveCapabilitySnapshot{}
	node.HeartbeatUnixMilli = 0
	node.Total = ResourceCapacity{}
	node.Available = ResourceCapacity{}
}
