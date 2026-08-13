package gate

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCompilePackingChoosesCanonicalLayoutAtOptimalMakespan(t *testing.T) {
	units := make([]compilePlanningUnit, 0, 4)
	for _, item := range []struct {
		id   string
		cost int64
	}{
		{id: "w0", cost: 1},
		{id: "w1", cost: 1},
		{id: "w2", cost: 2},
		{id: "w3", cost: 1},
	} {
		units = append(units, compilePackingTestUnit(item.id, item.cost))
	}

	shards, err := provenCompileUnitPacking(units, 4)
	if err != nil {
		t.Fatal(err)
	}
	if got := compilePackingTestLayout(shards); got != "w0,w1,w3|w2" {
		t.Fatalf("canonical compile packing layout = %q, want w0,w1,w3|w2", got)
	}
	if len(shards) != 2 || compilePackingMakespan(shards) != 3 {
		t.Fatalf("compile packing objective = %#v, want two shards at 3ms", shards)
	}
}

func TestCompilePackingLargeInputUsesVariableBinBFD(t *testing.T) {
	units := make([]compilePlanningUnit, 0, 20)
	for index := range 20 {
		units = append(units, compilePackingTestUnit(fmt.Sprintf("unit-%02d", index), 1))
	}
	shards, err := provenCompileUnitPacking(units, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 2 {
		t.Fatalf("large compile BFD shard count = %d, want 2", len(shards))
	}
	for _, shard := range shards {
		if shard.EstimatedDurationMS != 10 {
			t.Fatalf("large compile BFD shard duration = %d, want 10", shard.EstimatedDurationMS)
		}
	}
}

func TestCompilePackingBFDUsesBestFitResidualTieBreak(t *testing.T) {
	units := make([]compilePlanningUnit, 0, 5)
	for index, cost := range []int64{7, 6, 3, 2, 2} {
		units = append(units, compilePackingTestUnit(fmt.Sprintf("bfd-%d", index), cost))
	}
	shards, err := compileVariableBinBFD(units, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 2 {
		t.Fatalf("compile BFD shard count = %d, want 2: %#v", len(shards), shards)
	}
	for _, shard := range shards {
		if shard.EstimatedDurationMS > 10 {
			t.Fatalf("compile BFD exceeded target: %#v", shard)
		}
	}
}

func TestCompilePackingLargeHeuristicIsPermutationDeterministic(t *testing.T) {
	left := make([]compilePlanningUnit, 0, 20)
	for index := range 20 {
		left = append(left, compilePackingTestUnit(fmt.Sprintf("perm-%02d", index), int64((index%5)+1)))
	}
	right := append([]compilePlanningUnit(nil), left...)
	slices.Reverse(right)
	leftShards, err := provenCompileUnitPacking(left, 10)
	if err != nil {
		t.Fatal(err)
	}
	rightShards, err := provenCompileUnitPacking(right, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := compilePackingTestLayout(leftShards), compilePackingTestLayout(rightShards); got != want {
		t.Fatalf("large compile heuristic changed under permutation: left=%q right=%q", got, want)
	}
}

func TestCompilePackingThresholdCountsPackableUnitsNotIsolatedGroups(t *testing.T) {
	units := make([]compilePlanningUnit, 0, 25)
	for index := range 5 {
		units = append(units, compilePackingTestUnit(fmt.Sprintf("ordinary-%02d", index), 1))
	}
	for index := range 20 {
		id := fmt.Sprintf("isolated-%02d", index)
		group := &CompileGroup{GroupID: "group-" + id, SemanticKey: "exclusive"}
		units = append(units, compilePlanningUnit{workloads: []PlannedWorkload{compilePackingTestWorkload(id, 1)}, group: group, costMS: 1, affinityKey: id, sortID: id})
	}
	shards, err := provenCompileUnitPacking(units, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 21 {
		t.Fatalf("mixed packable/isolated shard count = %d, want exact packable one plus 20 isolated", len(shards))
	}
}

func TestCompilePackingAssignmentsRejectMissingSecondaryWorkload(t *testing.T) {
	unit := compilePackingTestUnit("present", 1)
	unit.workloads = append(unit.workloads, compilePackingTestWorkload("missing", 1))
	shards := []ShardPlan{{Index: 0, Workloads: []PlannedWorkload{compilePackingTestWorkload("present", 1)}, EstimatedDurationMS: 1}}
	if _, ok := compilePackingAssignments([]compilePlanningUnit{unit}, shards); ok {
		t.Fatal("compile packing assignment accepted missing secondary workload")
	}
}

func TestCompilePackingCanonicalLayoutIsPermutationDeterministic(t *testing.T) {
	left := []compilePlanningUnit{
		compilePackingTestUnit("w0", 1),
		compilePackingTestUnit("w1", 1),
		compilePackingTestUnit("w2", 2),
		compilePackingTestUnit("w3", 1),
	}
	right := []compilePlanningUnit{left[3], left[1], left[0], left[2]}
	leftShards, err := provenCompileUnitPacking(left, 4)
	if err != nil {
		t.Fatal(err)
	}
	rightShards, err := provenCompileUnitPacking(right, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(leftShards, rightShards, func(left, right ShardPlan) bool {
		return compilePackingShardKey(left) == compilePackingShardKey(right) && left.EstimatedDurationMS == right.EstimatedDurationMS
	}) {
		t.Fatalf("compile packing changed under input permutation: left=%#v right=%#v", leftShards, rightShards)
	}
}

func TestCompilePackingChoosesCanonicalLayoutForNineUnits(t *testing.T) {
	units := make([]compilePlanningUnit, 0, 9)
	for _, item := range []struct {
		id   string
		cost int64
	}{
		{id: "a", cost: 1},
		{id: "b", cost: 3},
		{id: "c", cost: 3},
		{id: "d", cost: 4},
		{id: "e", cost: 5},
		{id: "f", cost: 1},
		{id: "g", cost: 3},
		{id: "h", cost: 5},
		{id: "i", cost: 6},
	} {
		units = append(units, compilePackingTestUnit(item.id, item.cost))
	}

	shards, err := provenCompileUnitPacking(units, 11)
	if err != nil {
		t.Fatal(err)
	}
	if got := compilePackingTestLayout(shards); got != "a,b,c,d|e,f,g|h,i" {
		t.Fatalf("canonical nine-unit compile packing layout = %q, want a,b,c,d|e,f,g|h,i", got)
	}
	if len(shards) != 3 || compilePackingMakespan(shards) != 11 {
		t.Fatalf("nine-unit compile packing objective = %#v, want three shards at 11ms", shards)
	}
}

func TestCompilePackingIsolationAwareLowerBoundAvoidsGreedyExplosion(t *testing.T) {
	const (
		specialCount  = 500
		ordinaryCount = 500
		target        = int64(100)
	)
	units := make([]compilePlanningUnit, 0, specialCount+ordinaryCount)
	for index := range ordinaryCount {
		id := fmt.Sprintf("ordinary-%04d", index)
		units = append(units, compilePackingTestUnit(id, target))
	}
	for index := range specialCount {
		id := fmt.Sprintf("special-%04d", index)
		group := CompileGroup{
			GroupID:           id,
			PackageTarget:     AtomicArchtestPackageTarget,
			SemanticKey:       CompileGroupSemanticGoTestNormal,
			SharedInputDigest: "sha256:" + fmt.Sprintf("%064x", index),
			ProfileDigest:     "sha256:" + strings.Repeat("f", 64),
		}
		units = append(units, compilePlanningUnit{
			workloads:   []PlannedWorkload{compilePackingTestWorkload(id, 1)},
			group:       &group,
			costMS:      1,
			affinityKey: "special:" + id,
			sortID:      id,
		})
	}
	got, err := compilePackingShardLowerBound(units, target)
	if err != nil {
		t.Fatal(err)
	}
	if got != specialCount+ordinaryCount {
		t.Fatalf("isolation-aware shard lower bound = %d, want %d", got, specialCount+ordinaryCount)
	}

	started := time.Now()
	shards, err := provenCompileUnitPacking(units, target)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 10*time.Second {
		t.Fatalf("1000-unit compile packing took %s", elapsed)
	}
	if len(shards) != specialCount+ordinaryCount {
		t.Fatalf("1000-unit compile shard count = %d, want %d", len(shards), specialCount+ordinaryCount)
	}
}

func TestBuildCompileUnitsBindsCanonicalReturnedGroups(t *testing.T) {
	context := testPlanningContext()
	firstInput := compileTestInput(AtomicGatePackageTarget, "sha256:"+strings.Repeat("1", 64))
	secondInput := compileTestInput(AtomicTaskDAGPackageTarget, "sha256:"+strings.Repeat("2", 64))
	first, firstInputs := compileTestWorkloads(t, firstInput.PackageTarget, []string{"TestFirst"}, 1_000, firstInput)
	second, secondInputs := compileTestWorkloads(t, secondInput.PackageTarget, []string{"TestSecond"}, 1_000, secondInput)
	inputs := make(map[GateID]CompileGroupInput, 2)
	maps.Copy(inputs, firstInputs)
	maps.Copy(inputs, secondInputs)
	catalog := testWorkloadCatalog(append(first, second...)...)
	index, err := BuildDurationSampleIndex(testPlanningLedger(context, nil), context)
	if err != nil {
		t.Fatal(err)
	}
	base, err := estimateShardableWorkloads(catalog, index)
	if err != nil {
		t.Fatal(err)
	}
	hints, err := BuildCompileOwnerHints(base, inputs, index.CompileTimingIndex, context)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := plannedWorkloadsFromEstimates(base, hints)
	if err != nil {
		t.Fatal(err)
	}
	units, groups, err := buildCompileUnits(planned, index, inputs, workloadCanonicalOrder(catalog), hints)
	if err != nil {
		t.Fatal(err)
	}
	assertCompileUnitsBoundToGroups(t, units, groups)
}

func assertCompileUnitsBoundToGroups(t *testing.T, units []compilePlanningUnit, groups []CompileGroup) {
	t.Helper()
	owners := make(map[GateID]*CompileGroup)
	for index := range groups {
		for _, workloadID := range groups[index].WorkloadIDs {
			owners[workloadID] = &groups[index]
		}
	}
	for _, unit := range units {
		if unit.group != nil && unit.group != owners[GateID(unit.workloads[0].Workload.ID)] {
			t.Fatalf("unit %q points outside returned canonical groups", unit.workloads[0].Workload.ID)
		}
	}
}

func TestMinimizeCompilePackingReusesKnownFeasibleInitialAtLowerBound(t *testing.T) {
	units := []compilePlanningUnit{compilePackingTestUnit("known", 5)}
	initial := []ShardPlan{{
		Index: 0, Workloads: units[0].workloads, EstimatedDurationMS: units[0].costMS,
	}}
	budget := deterministicPackingSearchBudget{}
	shards, err := minimizeCompilePackingMakespan(units, initial, 10, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 1 || shards[0].Workloads[0].Workload.ID != "known" {
		t.Fatalf("known feasible layout = %#v", shards)
	}
}

func TestCompilePackingSearchFailsClosedWhenNodeBudgetIsExhausted(t *testing.T) {
	units := []compilePlanningUnit{compilePackingTestUnit("w0", 1), compilePackingTestUnit("w1", 1)}
	budget := deterministicPackingSearchBudget{}
	if _, _, err := searchCompilePackingFixedCount(units, 1, 2, 2, &budget); !errors.Is(err, errDeterministicPackingSearchBudget) {
		t.Fatalf("exhausted compile packing proof error = %v, want fail-closed budget error", err)
	}
}

func TestCompilePackingRejectsUnsupportedNormalResourceTier(t *testing.T) {
	unit := compilePackingTestUnit("invalid-tier", 1)
	unit.tier = 0
	if _, err := distributeCompileUnitsForPlanningContext([]compilePlanningUnit{unit}, testPlanningContext()); err == nil {
		t.Fatal("compile packing accepted an unsupported normal resource tier")
	}
}

func TestCompilePackingLowerBoundsFailClosedOnOverflowAndInvalidCount(t *testing.T) {
	maxInt64 := int64(^uint64(0) >> 1)
	units := []compilePlanningUnit{
		compilePackingTestUnit("max", maxInt64),
		compilePackingTestUnit("overflow", 1),
	}
	if _, err := compilePackingShardLowerBound(units, maxInt64); !errors.Is(err, errCompilePackingDurationOverflow) {
		t.Fatalf("shard lower bound overflow error = %v, want duration overflow", err)
	}
	if _, err := compilePackingMakespanLowerBound(units, 1); !errors.Is(err, errCompilePackingDurationOverflow) {
		t.Fatalf("makespan lower bound overflow error = %v, want duration overflow", err)
	}
	if _, err := compilePackingScoreForShards([]ShardPlan{
		compilePackingTestLayoutWithDuration("max-a", maxInt64),
		compilePackingTestLayoutWithDuration("max-b", maxInt64),
	}, 1); !errors.Is(err, errCompilePackingDurationOverflow) {
		t.Fatalf("compile packing excess overflow error = %v, want duration overflow", err)
	}
	if got, err := compilePackingCapacityLowerBound(maxInt64, 1, maxInt64); err != nil || got != 2 {
		t.Fatalf("max int64 capacity lower bound = %d, %v; want 2, nil", got, err)
	}
	if _, err := compilePackingMakespanLowerBound([]compilePlanningUnit{compilePackingTestUnit("one", 1)}, -1); !errors.Is(err, errCompilePackingInvalidShardCount) {
		t.Fatalf("negative shard count error = %v, want invalid shard count", err)
	}
	if _, _, err := searchCompilePackingFixedCount(nil, -1, 1, 1, &deterministicPackingSearchBudget{}); !errors.Is(err, errCompilePackingInvalidShardCount) {
		t.Fatalf("negative fixed-count error = %v, want invalid shard count", err)
	}
}

func compilePackingTestUnit(id string, cost int64) compilePlanningUnit {
	return compilePlanningUnit{
		workloads:   []PlannedWorkload{compilePackingTestWorkload(id, cost)},
		costMS:      cost,
		affinityKey: "ordinary:" + id,
		sortID:      id,
	}
}

func compilePackingTestWorkload(id string, cost int64) PlannedWorkload {
	return PlannedWorkload{Workload: Workload{ID: id}, EstimatedDurationMS: cost}
}

func compilePackingTestLayoutWithDuration(id string, duration int64) ShardPlan {
	return ShardPlan{Workloads: []PlannedWorkload{compilePackingTestWorkload(id, duration)}, EstimatedDurationMS: duration}
}

func compilePackingTestLayout(shards []ShardPlan) string {
	parts := make([]string, len(shards))
	for index, shard := range shards {
		parts[index] = compilePackingShardKey(shard)
	}
	return strings.Join(parts, "|")
}
