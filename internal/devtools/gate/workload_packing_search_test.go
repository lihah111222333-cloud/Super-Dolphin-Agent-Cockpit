package gate

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestDCPAPCounterexamplesArePermutationDeterministic(t *testing.T) {
	cases := []struct {
		name      string
		durations []int64
		target    int64
	}{
		{name: "target-seven", durations: []int64{5, 2, 1, 2, 5, 1, 2, 5, 1, 2, 4, 3, 3}, target: 7},
		{name: "target-five", durations: []int64{2, 5, 5, 5, 1, 3, 4, 3, 2, 4, 1, 1, 1}, target: 5},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			ids := make([]string, len(testCase.durations))
			durations := make(map[string]int64, len(testCase.durations))
			for index, duration := range testCase.durations {
				ids[index] = fmt.Sprintf("w%02d", index)
				durations[ids[index]] = duration
			}
			left, err := distributeDCPAP(testDCPAPWorkloadsInOrder(ids, durations), testCase.target)
			if err != nil {
				t.Fatal(err)
			}
			reversed := append([]string(nil), ids...)
			slices.Reverse(reversed)
			right, err := distributeDCPAP(testDCPAPWorkloadsInOrder(reversed, durations), testCase.target)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("DCPAP changed under input permutation: left=%#v right=%#v", left, right)
			}
		})
	}
}

func TestDCPAPPlannerFindsMinimumShardPackingForSixFiveThreeTwoTwoTwo(t *testing.T) {
	planned := make([]PlannedWorkload, 0, 6)
	for index, duration := range []int64{6, 5, 3, 2, 2, 2} {
		planned = append(planned, testDCPAPWorkload(fmt.Sprintf("w%d", index), duration))
	}
	shards, err := distributeDCPAP(planned, 10)
	if err != nil {
		t.Fatalf("distributeDCPAP() error = %v", err)
	}
	if len(shards) != 2 {
		t.Fatalf("DCPAP shard count = %d, want minimum 2", len(shards))
	}
	for _, shard := range shards {
		if shard.EstimatedDurationMS > 10 {
			t.Fatalf("DCPAP shard exceeds target: %#v", shard)
		}
	}
}

func TestDCPAPHeuristicRepairsBFDTwoMoveCounterexample(t *testing.T) {
	items := []dcpapItem{{id: "a", durationMS: 6}, {id: "b", durationMS: 5}, {id: "c", durationMS: 3}, {id: "d", durationMS: 2}, {id: "e", durationMS: 2}, {id: "f", durationMS: 2}}
	bfd := dcpapBestFitPack(items, 10)
	if len(bfd) != 3 {
		t.Fatalf("BFD shard count = %d, want counterexample 3: %#v", len(bfd), bfd)
	}
	result, err := dcpapHeuristicPack(items, 10)
	if err != nil {
		t.Fatalf("heuristic repair error = %v", err)
	}
	if len(result.bins) != 2 || result.shardGap != 0 {
		t.Fatalf("heuristic repair = shards=%d gap=%d bins=%#v, want 2/0", len(result.bins), result.shardGap, result.bins)
	}
	if result.plannedShards-result.lowerBoundShards != result.shardGap || result.objectiveMode != cicontract.WorkloadPlanningHeuristicSolverModeID {
		t.Fatalf("heuristic evidence = %#v, want truthful mode and shard gap", result)
	}
}

func TestDCPAPLargeProductionPathReportsTruthfulGap(t *testing.T) {
	planned := make([]PlannedWorkload, 0, 13)
	for index, duration := range []int64{6, 5, 3, 2, 2, 2, 10, 10, 10, 10, 10, 10, 10} {
		planned = append(planned, testDCPAPWorkload(fmt.Sprintf("large-%02d", index), duration))
	}
	shards, err := distributeDCPAP(planned, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 9 {
		t.Fatalf("large heuristic shard count = %d, want lower-bound-tight 9", len(shards))
	}
	items := make([]dcpapItem, len(planned))
	for index, workload := range planned {
		items[index] = dcpapItem{id: workload.Workload.ID, durationMS: workload.EstimatedDurationMS}
	}
	result, err := dcpapHeuristicPack(items, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.lowerBoundShards != 9 || result.plannedShards != 9 || result.shardGap != 0 {
		t.Fatalf("large heuristic evidence = %#v, want lower/planned/gap 9/9/0", result)
	}
}

func TestDCPAPLargeHeuristicReportsPositiveGapHonestly(t *testing.T) {
	items := make([]dcpapItem, 0, 13)
	for index, duration := range []int64{8, 7, 6, 5, 4} {
		items = append(items, dcpapItem{id: fmt.Sprintf("regular-%d", index), durationMS: duration})
	}
	for index := range 8 {
		items = append(items, dcpapItem{id: fmt.Sprintf("atomic-%d", index), durationMS: 10})
	}
	result, err := dcpapHeuristicPack(items, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.plannedShards <= result.lowerBoundShards || result.shardGap != result.plannedShards-result.lowerBoundShards {
		t.Fatalf("heuristic positive gap evidence = %#v, want planned > lower and truthful gap", result)
	}
}

func TestDCPAPPlannerProvesMinimumShardPackingWithinExactThreshold(t *testing.T) {
	planned := testDCPAPPlannedWorkloads(map[string]int64{
		"a": 8, "b": 6, "c": 3, "d": 3, "e": 2, "f": 2, "g": 2, "h": 2, "i": 2,
	})
	shards, err := distributeDCPAP(planned, 10)
	if err != nil {
		t.Fatalf("distributeDCPAP() error = %v", err)
	}
	if len(shards) != 3 {
		t.Fatalf("DCPAP shard count = %d, want proven minimum 3: %#v", len(shards), shards)
	}
	for _, shard := range shards {
		if shard.EstimatedDurationMS != 10 {
			t.Fatalf("DCPAP optimal makespan shard = %#v, want 10ms", shard)
		}
	}
}

func TestDCPAPPlannerChoosesCanonicalLayoutWithinExactThreshold(t *testing.T) {
	planned := testDCPAPPlannedWorkloads(map[string]int64{
		"a": 1, "b": 3, "c": 3, "d": 4, "e": 5, "f": 1, "g": 3, "h": 5, "i": 6,
	})
	shards, err := distributeDCPAP(planned, 11)
	if err != nil {
		t.Fatal(err)
	}
	if got := dcpapShardLayout(shards); got != "a,b,c,d|e,f,g|h,i" {
		t.Fatalf("canonical DCPAP layout = %q, want a,b,c,d|e,f,g|h,i", got)
	}
}

func TestDCPAPPlannerChoosesCanonicalLayoutForEqualLoadBins(t *testing.T) {
	shards, err := distributeDCPAP(testDCPAPPlannedWorkloads(map[string]int64{"a": 1, "b": 2, "c": 2, "d": 2}), 6)
	if err != nil {
		t.Fatal(err)
	}
	if got := dcpapShardLayout(shards); got != "a,b|c,d" {
		t.Fatalf("canonical DCPAP layout = %q, want a,b|c,d", got)
	}
}

func TestDCPAPExactPackingFailsClosedWhenNodeBudgetIsExhausted(t *testing.T) {
	items := []dcpapItem{{id: "a", durationMS: 8}, {id: "b", durationMS: 6}, {id: "c", durationMS: 3}}
	budget := deterministicPackingSearchBudget{}
	if _, err := dcpapExactPack(items, 10, &budget); !errors.Is(err, errDeterministicPackingSearchBudget) {
		t.Fatalf("exhausted exact packing error = %v, want fail-closed budget error", err)
	}
}

func TestDCPAPExactPackingStopsAtMandatoryOversizeExcess(t *testing.T) {
	items := []dcpapItem{{id: "oversize", durationMS: 101_000}}
	for index := 1; index <= 11; index++ {
		items = append(items, dcpapItem{id: fmt.Sprintf("unit-%02d", index), durationMS: 1_000})
	}
	bins, err := dcpapProvenPack(items, 100_000)
	if err != nil {
		t.Fatalf("oversize exact packing error = %v, want nil", err)
	}
	if len(bins) != 2 {
		t.Fatalf("oversize exact packing shard count = %d, want 2: %#v", len(bins), bins)
	}
	if bins[0].bodyMS != 101_000 || len(bins[0].items) != 1 || bins[0].items[0].id != "oversize" {
		t.Fatalf("oversize exact packing canonical oversize bin = %#v", bins[0])
	}
	if bins[1].bodyMS != 11_000 || len(bins[1].items) != 11 {
		t.Fatalf("oversize exact packing regular bin = %#v", bins[1])
	}
}

func TestDCPAPPlannerRejectsDurationArithmeticOverflow(t *testing.T) {
	maximum := int64(^uint64(0) >> 1)
	planned := []PlannedWorkload{
		testDCPAPWorkload("max-a", maximum),
		testDCPAPWorkload("max-b", maximum),
	}
	if _, err := distributeDCPAP(planned, 1); !errors.Is(err, errDCPAPDurationOverflow) {
		t.Fatalf("overflowing DCPAP duration error = %v, want fail-fast arithmetic overflow", err)
	}
}

func TestDCPAPPlannerRejectsDuplicateAndEmptyWorkloadIDs(t *testing.T) {
	for name, planned := range map[string][]PlannedWorkload{
		"duplicate": {
			testDCPAPWorkload("same", 1),
			testDCPAPWorkload("same", 2),
		},
		"empty": {
			testDCPAPWorkload("", 1),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := distributeDCPAP(planned, 10); err == nil {
				t.Fatal("DCPAP accepted invalid workload identity")
			}
		})
	}
}

func TestDCPAPPlannerIsPermutationDeterministic(t *testing.T) {
	durations := map[string]int64{"a": 2, "b": 3, "c": 4, "d": 5}
	left, err := distributeDCPAP(testDCPAPWorkloadsInOrder([]string{"a", "b", "c", "d"}, durations), 6)
	if err != nil {
		t.Fatal(err)
	}
	right, err := distributeDCPAP(testDCPAPWorkloadsInOrder([]string{"d", "b", "a", "c"}, durations), 6)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("DCPAP changed under input permutation: left=%#v right=%#v", left, right)
	}
}

func TestDCPAPPlannerWithinExactThresholdIsDeterministic(t *testing.T) {
	durations := map[string]int64{}
	for index := range 9 {
		durations[fmt.Sprintf("w%d", index)] = int64((index % 4) + 2)
	}
	left, err := distributeDCPAP(testDCPAPWorkloadsInOrder([]string{"w0", "w1", "w2", "w3", "w4", "w5", "w6", "w7", "w8"}, durations), 10)
	if err != nil {
		t.Fatal(err)
	}
	right, err := distributeDCPAP(testDCPAPWorkloadsInOrder([]string{"w8", "w3", "w6", "w1", "w7", "w0", "w5", "w2", "w4"}, durations), 10)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(left, right) {
		t.Fatalf("exact DCPAP changed under input permutation: left=%#v right=%#v", left, right)
	}
}

func TestDCPAPPlannerLargeInputPerformanceGuard(t *testing.T) {
	const target int64 = 100
	planned, items, expected := testDCPAPLargeInputFixtures()
	reversed := slices.Clone(planned)
	slices.Reverse(reversed)
	started := time.Now()
	shards, err := distributeDCPAP(planned, target)
	if err != nil {
		t.Fatalf("large-input DCPAP error = %v, want nil", err)
	}
	reversedShards, err := distributeDCPAP(reversed, target)
	if err != nil {
		t.Fatalf("reversed large-input DCPAP error = %v, want nil", err)
	}
	if !reflect.DeepEqual(shards, reversedShards) {
		t.Fatalf("large-input DCPAP changed under input reversal")
	}
	assertDCPAPLargeHeuristicEvidence(t, items, target, len(shards))
	assertDCPAPLargeShardCoverage(t, shards, expected, target)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("large-input DCPAP performance guard failed: shards=%d elapsed=%s", len(shards), elapsed)
	}
}

func testDCPAPLargeInputFixtures() ([]PlannedWorkload, []dcpapItem, map[string]struct{}) {
	planned := make([]PlannedWorkload, 1_000)
	items := make([]dcpapItem, len(planned))
	expected := make(map[string]struct{}, len(planned))
	for index := range planned {
		id := fmt.Sprintf("large-%04d", index)
		duration := int64((index % 20) + 1)
		planned[index] = testDCPAPWorkload(id, duration)
		items[index] = dcpapItem{id: id, durationMS: duration}
		expected[id] = struct{}{}
	}
	return planned, items, expected
}

func assertDCPAPLargeHeuristicEvidence(t *testing.T, items []dcpapItem, target int64, shardCount int) {
	t.Helper()
	result, err := dcpapHeuristicPack(items, target)
	if err != nil {
		t.Fatalf("large-input heuristic packing error = %v", err)
	}
	if result.objectiveMode != cicontract.WorkloadPlanningHeuristicSolverModeID {
		t.Fatalf("large-input solver mode = %q, want heuristic", result.objectiveMode)
	}
	lower, err := dcpapShardCountLowerBound(items, target)
	if err != nil {
		t.Fatalf("large-input lower bound error = %v", err)
	}
	if result.lowerBoundShards != lower || result.plannedShards != shardCount || result.shardGap != result.plannedShards-result.lowerBoundShards {
		t.Fatalf("large-input heuristic evidence = %#v, want truthful lower/planned/gap", result)
	}
}

func assertDCPAPLargeShardCoverage(t *testing.T, shards []ShardPlan, expected map[string]struct{}, target int64) {
	t.Helper()
	covered := make(map[string]struct{}, len(expected))
	for _, shard := range shards {
		if shard.EstimatedDurationMS <= 0 || shard.EstimatedDurationMS > target {
			t.Fatalf("large-input shard violates target: %#v", shard)
		}
		for _, workload := range shard.Workloads {
			id := workload.Workload.ID
			if _, duplicate := covered[id]; duplicate {
				t.Fatalf("large-input shard coverage duplicates workload %q", id)
			}
			if _, known := expected[id]; !known {
				t.Fatalf("large-input shard coverage contains unknown workload %q", id)
			}
			covered[id] = struct{}{}
		}
	}
	if len(covered) != len(expected) {
		t.Fatalf("large-input shard coverage = %d workloads, want %d", len(covered), len(expected))
	}
}

func TestDCPAPPlannerLargeEasyInputProvesWithinBudget(t *testing.T) {
	planned := make([]PlannedWorkload, 1_000)
	for index := range planned {
		planned[index] = testDCPAPWorkload(fmt.Sprintf("singleton-%04d", index), 100)
	}
	started := time.Now()
	shards, err := distributeDCPAP(planned, 100)
	if err != nil {
		t.Fatalf("easy large-input DCPAP error = %v", err)
	}
	if len(shards) != len(planned) {
		t.Fatalf("easy large-input DCPAP shard count = %d, want %d", len(shards), len(planned))
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("easy large-input DCPAP performance guard failed: elapsed=%s", elapsed)
	}
}

func testDCPAPPlannedWorkloads(durations map[string]int64) []PlannedWorkload {
	ids := make([]string, 0, len(durations))
	for id := range durations {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return testDCPAPWorkloadsInOrder(ids, durations)
}

func testDCPAPWorkloadsInOrder(ids []string, durations map[string]int64) []PlannedWorkload {
	planned := make([]PlannedWorkload, 0, len(ids))
	for _, id := range ids {
		planned = append(planned, testDCPAPWorkload(id, durations[id]))
	}
	return planned
}

func testDCPAPWorkload(id string, duration int64) PlannedWorkload {
	return PlannedWorkload{Workload: Workload{ID: id, Kind: WorkloadKindGoTest, CommandDigest: testWorkloadDigest, BootstrapEstimateMS: duration, Shardable: true}, EstimatedDurationMS: duration, ResourceCPU: 2, ResourceMemoryGiB: 4}
}

func dcpapShardLayout(shards []ShardPlan) string {
	parts := make([]string, len(shards))
	for index, shard := range shards {
		ids := make([]string, len(shard.Workloads))
		for itemIndex, workload := range shard.Workloads {
			ids[itemIndex] = workload.Workload.ID
		}
		slices.Sort(ids)
		parts[index] = strings.Join(ids, ",")
	}
	return strings.Join(parts, "|")
}
