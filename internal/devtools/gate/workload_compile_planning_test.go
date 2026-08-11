package gate

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestCompileGroupBatchPlanChoosesMinimumOrdinaryK(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, "./internal/example", []string{"TestAlpha", "TestBravo", "TestCharlie"}, []int64{60_000, 60_000, 60_000})
	_, batches, warning, err := compileGroupBatchPlan(selectors, 20_000, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != 3 {
		t.Fatalf("minimum ordinary batches = %d warning=%q, want K=3 without warning", len(batches), warning)
	}
	for _, batch := range batches {
		if batch.Wave != 0 || batch.Exclusive {
			t.Fatalf("ordinary batch = %#v, want wave 0 non-exclusive", batch)
		}
	}
}

func TestCompileGroupBatchPlanDoesNotCapOrdinaryBatchesByResourceVCPUs(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, "./internal/example", []string{"TestAlpha", "TestBravo", "TestCharlie"}, []int64{60_000, 60_000, 60_000})
	_, batches, warning, err := compileGroupBatchPlan(selectors, 20_000, "small")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != 3 {
		t.Fatalf("small-resource ordinary batches = %d warning=%q, want workload-driven K=3 without warning", len(batches), warning)
	}
	for _, batch := range batches {
		if batch.Wave != 0 || batch.Exclusive {
			t.Fatalf("ordinary batch = %#v, want same-wave non-exclusive", batch)
		}
	}
}

func TestCompileGroupBatchPlanLPTIsDeterministicOnTies(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, "./internal/example", []string{"TestAlpha", "TestBravo", "TestCharlie", "TestDelta"}, []int64{5, 5, 1, 1})
	_, first, warning, err := compileGroupBatchPlan(selectors, 99_990, "small")
	if err != nil {
		t.Fatal(err)
	}
	_, second, secondWarning, err := compileGroupBatchPlan(selectors, 99_990, "small")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || secondWarning != "" || len(first) != 2 || !reflect.DeepEqual(first, second) {
		t.Fatalf("LPT tie plan first=%#v second=%#v warnings=%q/%q", first, second, warning, secondWarning)
	}
	if first[0].EstimatedBodyMS != 6 || first[1].EstimatedBodyMS != 6 {
		t.Fatalf("LPT tie body estimates = %#v, want [6 6]", first)
	}
}

func TestCompileGroupBatchPlanWarnsWhenMaximumKStillExceedsTarget(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, "./internal/example", []string{"TestAlpha", "TestBravo"}, []int64{100_000, 100_000})
	_, batches, warning, err := compileGroupBatchPlan(selectors, 1, "small")
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || warning == "" || !strings.Contains(warning, "exceeds_target_ms=100000") {
		t.Fatalf("over-target plan batches=%#v warning=%q", batches, warning)
	}
}

func TestCompileGroupBatchPlanUsesOneBatchPerBoundedArchtestGroup(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, AtomicArchtestPackageTarget, []string{"TestAlpha", "TestBravo", "TestCharlie"}, []int64{10, 20, 30})
	_, batches, warning, err := compileGroupBatchPlan(selectors, 100_000, "maximum")
	if err != nil {
		t.Fatal(err)
	}
	if warning == "" || !strings.Contains(warning, "archtest_group_batch_limit=1") || len(batches) != 1 || len(batches[0].SelectorIDs) != len(selectors) {
		t.Fatalf("archtest batches = %#v warning=%q, want one batch per bounded group plus over-target warning", batches, warning)
	}
	if batches[0].Wave != 0 || batches[0].Exclusive {
		t.Fatalf("archtest batch = %#v, want non-exclusive wave 0", batches[0])
	}
}

func TestSplitArchtestCompilePlanningPartitionsUsesCapacityConstrainedLPT(t *testing.T) {
	const selectorCount = 423
	heavyCount := (selectorCount + cicontract.CompileGroupMaxSelectors - 1) / cicontract.CompileGroupMaxSelectors
	selectors := archtestLPTTestSelectors(selectorCount, heavyCount)
	partitions, err := splitCompilePlanningPartitions(compilePlanningBucket{selectors: selectors})
	if err != nil {
		t.Fatal(err)
	}
	wantPartitions := (selectorCount + cicontract.CompileGroupMaxSelectors - 1) / cicontract.CompileGroupMaxSelectors
	if len(partitions) != wantPartitions {
		t.Fatalf("partition count = %d, want %d", len(partitions), wantPartitions)
	}
	assertArchtestLPTPartitionShape(t, partitions, heavyCount)
}

func archtestLPTTestSelectors(selectorCount, heavyCount int) []compilePlanningSelector {
	selectors := make([]compilePlanningSelector, selectorCount)
	for index := range selectors {
		bodyEstimate := int64(1_000)
		if index < heavyCount {
			bodyEstimate = 100_000
		}
		selectors[index] = compilePlanningSelector{
			planned:        PlannedWorkload{Workload: Workload{ID: fmt.Sprintf("selector-%03d", index)}},
			input:          CompileGroupInput{PackageTarget: AtomicArchtestPackageTarget},
			bodyEstimateMS: bodyEstimate,
			canonicalOrder: index,
		}
	}
	return selectors
}

func assertArchtestLPTPartitionShape(t *testing.T, partitions [][]compilePlanningSelector, heavyCount int) {
	t.Helper()
	var minimum, maximum int64
	heavyPartitions := 0
	for index, partition := range partitions {
		if len(partition) == 0 || len(partition) > cicontract.CompileGroupMaxSelectors {
			t.Fatalf("partition %d size = %d, want 1..%d", index, len(partition), cicontract.CompileGroupMaxSelectors)
		}
		body, hasHeavySelector := archtestLPTPartitionBody(partition)
		if hasHeavySelector {
			heavyPartitions++
		}
		if index == 0 || body < minimum {
			minimum = body
		}
		if body > maximum {
			maximum = body
		}
	}
	if heavyPartitions != heavyCount {
		t.Fatalf("heavy selectors landed in %d partition(s), want %d", heavyPartitions, heavyCount)
	}
	if maximum-minimum > 1_000 {
		t.Fatalf("LPT partition body spread = %d (min=%d max=%d), want <=1000", maximum-minimum, minimum, maximum)
	}
}

func archtestLPTPartitionBody(partition []compilePlanningSelector) (int64, bool) {
	var body int64
	heavy := false
	for _, selector := range partition {
		body += selector.bodyEstimateMS
		if selector.bodyEstimateMS == 100_000 {
			heavy = true
		}
	}
	return body, heavy
}

func TestCompileGroupBatchPlanKeepsAgentTerminalTestMainInOneProcess(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, AtomicAgentTerminalPackageTarget, []string{"TestAgentTerminalMain", "TestAgentTerminalRecovery", "TestAgentTerminalRollback"}, []int64{10, 20, 30})
	_, batches, warning, err := compileGroupBatchPlan(selectors, 1, "maximum")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != 1 || len(batches[0].SelectorIDs) != len(selectors) {
		t.Fatalf("agent-terminal batches = %#v warning=%q, want one TestMain process", batches, warning)
	}
}

func TestCompileGroupBatchPlanSplitsInternalAppAcrossSharedCompileBatches(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, AtomicAppPackageTarget, []string{
		"TestAppModuleGraphIsClosed",
		"TestRunDesktopPreDrain",
		"TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch",
		"TestRecoveryRestoreClosesStableCrashBetweenCallsStates",
	}, []int64{60_000, 60_000, 60_000, 60_000})
	_, batches, warning, err := compileGroupBatchPlan(selectors, 20_000, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != 4 {
		t.Fatalf("internal/app batches=%#v warning=%q, want four shared-binary waves", batches, warning)
	}
	seen := make(map[GateID]bool, len(selectors))
	for _, batch := range batches {
		if batch.Wave != 0 || batch.Exclusive {
			t.Fatalf("internal/app batch=%#v, want ordinary parallel batch", batch)
		}
		for _, id := range batch.SelectorIDs {
			if seen[id] {
				t.Fatalf("internal/app selector %q appears in multiple batches", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != len(selectors) {
		t.Fatalf("internal/app selectors covered=%d, want %d", len(seen), len(selectors))
	}
}

func TestCompileGroupBatchPlanSplitsUpdaterAndTaskDAGAcrossSharedCompileBatches(t *testing.T) {
	for _, test := range []struct {
		name          string
		packageTarget string
		names         []string
	}{
		{
			name:          "updater",
			packageTarget: AtomicUpdaterPackageTarget,
			names:         []string{"TestUpdaterCandidateCleanup", "TestUpdaterRollbackEntries", "TestUpdaterTransaction", "TestUpdaterRecovery"},
		},
		{
			name:          "taskdag",
			packageTarget: AtomicTaskDAGPackageTarget,
			names:         []string{"TestTaskDAGStore", "TestTaskDAGWakeup", "TestTaskDAGRuntime", "TestTaskDAGFencing"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selectors := testCompilePlanningSelectors(t, test.packageTarget, test.names, []int64{60_000, 60_000, 60_000, 60_000})
			_, batches, warning, err := compileGroupBatchPlan(selectors, 20_000, "medium")
			if err != nil {
				t.Fatal(err)
			}
			if warning != "" || len(batches) != len(test.names) {
				t.Fatalf("%s batches=%#v warning=%q, want one ordinary batch per selector", test.packageTarget, batches, warning)
			}
			for _, batch := range batches {
				if batch.Wave != 0 || batch.Exclusive || len(batch.SelectorIDs) != 1 {
					t.Fatalf("%s batch=%#v, want ordinary parallel singleton batch", test.packageTarget, batch)
				}
			}
		})
	}
}

func TestCompileGroupBatchPlanSplitsSQLiteAcrossSharedCompileBatches(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, AtomicSQLitePackageTarget, []string{
		"TestSQLiteCommon",
		"TestSQLiteNormal",
		"TestSQLiteQueryPlanSmoke",
		"TestSQLiteMixedWritePressure",
	}, []int64{60_000, 60_000, 60_000, 60_000})
	_, batches, warning, err := compileGroupBatchPlan(selectors, 20_000, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != 4 {
		t.Fatalf("sqlite batches=%#v warning=%q, want four shared-binary waves", batches, warning)
	}
	seen := make(map[GateID]bool, len(selectors))
	for _, batch := range batches {
		if batch.Wave != 0 || batch.Exclusive {
			t.Fatalf("sqlite batch=%#v, want ordinary parallel batch", batch)
		}
		for _, id := range batch.SelectorIDs {
			if seen[id] {
				t.Fatalf("sqlite selector %q appears in multiple batches", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != len(selectors) {
		t.Fatalf("sqlite selectors covered=%d, want %d", len(seen), len(selectors))
	}
}

func TestCompileGroupBatchPlanKeepsAtomicDevtoolsPackagesOnOneBinary(t *testing.T) {
	for _, test := range []struct {
		name          string
		packageTarget string
		names         []string
	}{
		{
			name:          "gate",
			packageTarget: AtomicGatePackageTarget,
			names:         []string{"TestGateAtomicCommon", "TestGateAtomicNormal", "TestGateAtomicThird"},
		},
		{
			name:          "remoteci",
			packageTarget: AtomicRemoteCIPackageTarget,
			names:         []string{"TestRemoteCIAtomicCommon", "TestRemoteCIAtomicNormal", "TestRemoteCIAtomicThird"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			selectors := testCompilePlanningSelectors(t, test.packageTarget, test.names, []int64{60_000, 60_000, 60_000})
			_, batches, warning, err := compileGroupBatchPlan(selectors, 20_000, "medium")
			if err != nil {
				t.Fatal(err)
			}
			if warning != "" || len(batches) != len(test.names) {
				t.Fatalf("%s batches=%#v warning=%q, want one shared binary with resource-sized selector batches", test.packageTarget, batches, warning)
			}
			for _, batch := range batches {
				if batch.Wave != 0 || batch.Exclusive || len(batch.SelectorIDs) != 1 {
					t.Fatalf("%s batch=%#v, want ordinary resource batch singleton", test.packageTarget, batch)
				}
			}
		})
	}
}

func TestCompileGroupBatchPlanPlacesMcpLSPProcessCohortSelectorsInExclusiveWaves(t *testing.T) {
	selectors := append(
		testCompilePlanningSelectors(t, AtomicMcpLSPPackageTarget, []string{"TestMcpLSPCommon"}, []int64{10}),
		testCompilePlanningSelectors(t, AtomicMcpLSPPackageTarget, []string{
			"TestRuntimeDurableGoplsRootCohortSharesStateAcrossControllers",
			"TestMcpLSPBinaryConcurrentAgentsRespectGoplsRootCohortIsolation_E2E",
		}, []int64{20, 30})...,
	)
	_, batches, warning, err := compileGroupBatchPlan(selectors, 1, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != 3 || batches[0].Wave != 0 || batches[0].Exclusive {
		t.Fatalf("mcp-lsp batch plan = %#v warning=%q", batches, warning)
	}
	for index, batch := range batches[1:] {
		if batch.Wave != index+1 || !batch.Exclusive || len(batch.SelectorIDs) != 1 {
			t.Fatalf("mcp-lsp batch %d = %#v, want singleton serial wave", index+1, batch)
		}
	}
}

func TestCompileGroupBatchPlanPlacesCodexSelectorsInExclusiveSerialWaves(t *testing.T) {
	selectors := append(
		testCompilePlanningSelectors(t, "./internal/archtest", []string{"TestAlpha"}, []int64{10}),
		testCompilePlanningSelectors(t, AtomicCodexAppPackageTarget, []string{
			"TestDiscoverProcessesReturnsBothMaps",
			"TestCleanOrphanedMCPProcessesSkipsSelf",
		}, []int64{10, 20})...,
	)
	_, batches, warning, err := compileGroupBatchPlan(selectors, 1, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != 3 || batches[0].Wave != 0 || batches[0].Exclusive {
		t.Fatalf("codex batch plan = %#v warning=%q", batches, warning)
	}
	for index, batch := range batches[1:] {
		if batch.Wave != index+1 || !batch.Exclusive || len(batch.SelectorIDs) != 1 {
			t.Fatalf("codex batch %d = %#v, want singleton serial wave", index+1, batch)
		}
	}
}

func TestCompileGroupBatchPlanHandlesExclusiveOnlySelectors(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, AtomicCodexAppPackageTarget, []string{
		"TestDiscoverProcessesReturnsBothMaps",
		"TestCleanOrphanedMCPProcessesSkipsSelf",
	}, []int64{10, 20})
	_, batches, warning, err := compileGroupBatchPlan(selectors, 1, "small")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != len(selectors) {
		t.Fatalf("exclusive-only codex batch plan = %#v warning=%q, want one serial batch per selector", batches, warning)
	}
	for index, batch := range batches {
		if batch.Wave != index || !batch.Exclusive || len(batch.SelectorIDs) != 1 {
			t.Fatalf("exclusive-only codex batch %d = %#v, want singleton serial wave", index, batch)
		}
	}
}

func TestCompileGroupCriticalPathSumsWaveMaxAndSharedCompileOnce(t *testing.T) {
	batches := []CompileGroupBatch{
		{BatchID: "batch-000", Wave: 0, EstimatedBodyMS: 6},
		{BatchID: "batch-001", Wave: 0, EstimatedBodyMS: 5},
		{BatchID: "batch-002", Wave: 1, EstimatedBodyMS: 3, Exclusive: true},
		{BatchID: "batch-003", Wave: 2, EstimatedBodyMS: 2, Exclusive: true},
		{BatchID: "batch-004", Wave: 3, EstimatedBodyMS: 2, Exclusive: true},
	}
	if got, want := compileGroupCriticalPathMS(batches), int64(13); got != want {
		t.Fatalf("compile group critical path = %d, want wave max sum %d", got, want)
	}
	if got := 10 + compileGroupCriticalPathMS(batches); got != 23 {
		t.Fatalf("shared compile was not counted exactly once: got %d", got)
	}
}

func TestCompileGroupCriticalDurationUsesWaveMaxWhileWireKeepsCoverageSum(t *testing.T) {
	group := CompileGroup{
		CompileEstimateMS:   10,
		BodyEstimateMS:      18,
		EstimatedDurationMS: 28,
		BatchPlan: []CompileGroupBatch{
			{BatchID: "batch-000", Wave: 0, EstimatedBodyMS: 6},
			{BatchID: "batch-001", Wave: 0, EstimatedBodyMS: 5},
			{BatchID: "batch-002", Wave: 1, EstimatedBodyMS: 3},
			{BatchID: "batch-003", Wave: 2, EstimatedBodyMS: 2},
			{BatchID: "batch-004", Wave: 3, EstimatedBodyMS: 2},
		},
	}
	if got, want := compileGroupCriticalDurationMS(group), int64(23); got != want {
		t.Fatalf("critical planner cost = %d, want %d", got, want)
	}
	if group.EstimatedDurationMS != group.CompileEstimateMS+group.BodyEstimateMS {
		t.Fatalf("wire coverage duration changed: %#v", group)
	}
}

func TestCompileGroupCriticalPathIsPermutationDeterministic(t *testing.T) {
	left := []CompileGroupBatch{{Wave: 0, EstimatedBodyMS: 6}, {Wave: 0, EstimatedBodyMS: 5}, {Wave: 1, EstimatedBodyMS: 3}}
	right := []CompileGroupBatch{{Wave: 1, EstimatedBodyMS: 3}, {Wave: 0, EstimatedBodyMS: 5}, {Wave: 0, EstimatedBodyMS: 6}}
	if compileGroupCriticalPathMS(left) != compileGroupCriticalPathMS(right) {
		t.Fatalf("critical path changed under input permutation")
	}
}

func TestCompileGroupAffinityAllowsEligibleSerialMultiGroupShard(t *testing.T) {
	group := func(suffix string) CompileGroup {
		return CompileGroup{
			GroupID:           "group-" + suffix,
			PackageTarget:     AtomicGatePackageTarget,
			SemanticKey:       CompileGroupSemanticGoTestNormal,
			SharedInputDigest: "sha256:" + strings.Repeat(suffix, 64),
			ProfileDigest:     "sha256:" + strings.Repeat("f", 64),
			WorkloadIDs:       []GateID{GateID("gate:test-" + suffix)},
		}
	}
	groups := map[string]CompileGroup{"group-a": group("a"), "group-b": group("b")}
	if err := compileGroupAffinityFromShardIDs(groups, ShardPlan{Index: 0, CompileGroupIDs: []string{"group-a", "group-b"}}); err != nil {
		t.Fatalf("eligible serial multi-group shard rejected: %v", err)
	}
	if err := compileGroupAffinityFromShardIDs(groups, ShardPlan{Index: 0, CompileGroupIDs: []string{"group-b", "group-a"}}); err == nil {
		t.Fatal("non-canonical multi-group order was accepted")
	}
}

func TestCompileGroupAffinityRejectsSpecialMultiGroupShard(t *testing.T) {
	groups := map[string]CompileGroup{
		"group-a": {GroupID: "group-a", PackageTarget: AtomicGatePackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal, SharedInputDigest: "sha256:" + strings.Repeat("a", 64), ProfileDigest: "sha256:" + strings.Repeat("f", 64), WorkloadIDs: []GateID{GateID("gate:test-a")}},
		"group-b": {GroupID: "group-b", PackageTarget: AtomicArchtestPackageTarget, SemanticKey: CompileGroupSemanticGoTestNormal, SharedInputDigest: "sha256:" + strings.Repeat("b", 64), ProfileDigest: "sha256:" + strings.Repeat("f", 64), WorkloadIDs: []GateID{GateID("gate:test-b")}},
	}
	if err := compileGroupAffinityFromShardIDs(groups, ShardPlan{Index: 0, CompileGroupIDs: []string{"group-a", "group-b"}}); err == nil {
		t.Fatal("special compile group was accepted for serial multi-group packing")
	}
}

func TestCompileAwarePackingProvesMinimumShardCountBeyondGreedyTrap(t *testing.T) {
	durations := map[string]int64{"a": 80_000, "b": 60_000, "c": 30_000, "d": 30_000, "e": 20_000, "f": 20_000, "g": 20_000, "h": 20_000, "i": 20_000}
	units := make([]compilePlanningUnit, 0, len(durations))
	for _, workload := range testDCPAPPlannedWorkloads(durations) {
		units = append(units, compilePlanningUnit{
			workloads: []PlannedWorkload{workload}, costMS: workload.EstimatedDurationMS,
			affinityKey: "ordinary:" + workload.Workload.ID, sortID: workload.Workload.ID,
			tier: cicontract.WorkloadResourceTierFast,
		})
	}
	sortCompilePlanningUnits(units)
	context := testPlanningContext()
	context.ShardOverheadSampleCount = 1
	context.ShardOverheadProvenanceDigest = "sha256:" + strings.Repeat("a", 64)
	shards, err := distributeCompileUnitsWithinTarget(units, context)
	if err != nil {
		t.Fatalf("compile packing proof error = %v", err)
	}
	if len(shards) != 3 {
		t.Fatalf("compile-aware shard count = %d, want proven minimum 3: %#v", len(shards), shards)
	}
	for _, shard := range shards {
		if shard.EstimatedDurationMS != 100_000 {
			t.Fatalf("compile-aware makespan shard = %#v, want 100000ms", shard)
		}
	}
}

func TestCompilePackingKeepsSpecialGroupIsolatedFromOrdinaryWorkload(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, AtomicArchtestPackageTarget, []string{"TestSpecialIsolation"}, []int64{40_000})
	group := testCompileGroupFromPlanningSelectors(t, selectors, 1, "medium")
	ordinaryWorkload := testDCPAPPlannedWorkloads(map[string]int64{"ordinary": 60_000})[0]
	ordinary := compilePlanningUnit{workloads: []PlannedWorkload{ordinaryWorkload}, costMS: 60_000, affinityKey: "ordinary:ordinary", sortID: "ordinary", tier: cicontract.WorkloadResourceTierFast}
	special := compilePlanningUnit{workloads: []PlannedWorkload{selectors[0].planned}, group: &group, costMS: 40_000, affinityKey: group.SharedInputDigest, sortID: group.GroupID, tier: cicontract.WorkloadResourceTierFast}
	for _, units := range [][]compilePlanningUnit{{ordinary, special}, {special, ordinary}} {
		sortCompilePlanningUnits(units)
		shards, err := provenCompileUnitPacking(units, 100_000)
		if err != nil {
			t.Fatal(err)
		}
		if len(shards) != 2 {
			t.Fatalf("special compile group shared an ordinary shard: %#v", shards)
		}
	}
	mixed := ShardPlan{Index: 0, Workloads: []PlannedWorkload{selectors[0].planned, ordinaryWorkload}, CompileGroupIDs: []string{group.GroupID}}
	if err := compileGroupAffinityFromShardIDs(map[string]CompileGroup{group.GroupID: group}, mixed); err == nil || !strings.Contains(err.Error(), "mixes grouped and ordinary") {
		t.Fatalf("stored special+ordinary shard error = %v, want isolation failure", err)
	}
}

func TestCompilePackingKeepsDifferentResourceClassesIsolated(t *testing.T) {
	firstSelectors := testCompilePlanningSelectors(t, AtomicGatePackageTarget, []string{"TestResourceSmall"}, []int64{10_000})
	secondSelectors := testCompilePlanningSelectors(t, AtomicRemoteCIPackageTarget, []string{"TestResourceMedium"}, []int64{10_000})
	first := testCompileGroupFromPlanningSelectors(t, firstSelectors, 1_000, "small")
	second := testCompileGroupFromPlanningSelectors(t, secondSelectors, 1_000, "medium")
	units := []compilePlanningUnit{
		{workloads: []PlannedWorkload{firstSelectors[0].planned}, group: &first, costMS: 11_000, affinityKey: "resource:small", sortID: first.GroupID, tier: cicontract.WorkloadResourceTierFast},
		{workloads: []PlannedWorkload{secondSelectors[0].planned}, group: &second, costMS: 11_000, affinityKey: "resource:medium", sortID: second.GroupID, tier: cicontract.WorkloadResourceTierFast},
	}
	sortCompilePlanningUnits(units)
	if _, ok := distributeCompileUnits(units, 1, 100_000); ok {
		t.Fatal("different resource classes were packed into one shard")
	}
	shards, err := provenCompileUnitPacking(units, 100_000)
	if err != nil {
		t.Fatalf("resource-isolated packing proof error = %v", err)
	}
	if len(shards) != 2 {
		t.Fatalf("resource-isolated shard count = %d, want 2: %#v", len(shards), shards)
	}
	for index, shard := range shards {
		if len(shard.CompileGroupIDs) != 1 {
			t.Fatalf("shard %d compile groups = %#v, want one group", index, shard.CompileGroupIDs)
		}
	}
}

func TestCompileGroupBatchPlanKeepsRaceParentCodexSelectorsNonExclusive(t *testing.T) {
	input := compileTestInput(AtomicCodexAppPackageTarget, "sha256:"+strings.Repeat("a", 64))
	input.SemanticKey = CompileGroupSemanticGoTestRace
	names := []string{"TestDiscoverProcessesReturnsBothMaps", "TestCleanOrphanedMCPProcessesSkipsSelf"}
	selectors := make([]compilePlanningSelector, len(names))
	for index, name := range names {
		workload, err := NewGoTestWorkload(GateIDBackendTestGuardWithRace, AtomicCodexAppPackageTarget, name, 11)
		if err != nil {
			t.Fatal(err)
		}
		selectors[index] = compilePlanningSelector{
			planned: PlannedWorkload{Workload: workload, EstimatedDurationMS: 11},
			parent:  GateIDBackendTestGuardWithRace, targetKind: WorkloadTargetGoTest,
			target: GoTestTarget{Package: AtomicCodexAppPackageTarget, Name: name}, input: input,
			bodyEstimateMS: 10, compileEstimateMS: 1, canonicalOrder: index,
		}
	}
	_, batches, warning, err := compileGroupBatchPlan(selectors, 1, "medium")
	if err != nil {
		t.Fatal(err)
	}
	if warning != "" || len(batches) != 1 || batches[0].Wave != 0 || batches[0].Exclusive {
		t.Fatalf("race-parent codex batch plan = %#v warning=%q, want one ordinary batch", batches, warning)
	}
}

func TestCompileGroupBatchPlanDigestAndCoverageAreStrict(t *testing.T) {
	selectors := testCompilePlanningSelectors(t, "./internal/archtest", []string{"TestAlpha", "TestBravo"}, []int64{10, 20})
	group := testCompileGroupFromPlanningSelectors(t, selectors, 1, "medium")
	wrongDigest := group
	wrongDigest.BatchPlanDigest = "sha256:" + strings.Repeat("f", 64)
	if err := wrongDigest.Validate(); err == nil || !strings.Contains(err.Error(), "batch plan digest") {
		t.Fatalf("tampered batch digest validation = %v", err)
	}
	missing := group
	missing.BatchPlan = append([]CompileGroupBatch(nil), group.BatchPlan...)
	missing.BatchPlan[0].SelectorIDs = missing.BatchPlan[0].SelectorIDs[:1]
	missing.BatchPlan[0].EstimatedBodyMS = selectors[0].bodyEstimateMS
	missing.BatchPlanDigest, _ = CompileGroupBatchPlanDigest(missing)
	missing.GroupID, _ = CompileGroupID(missing)
	if err := missing.Validate(); err == nil || !strings.Contains(err.Error(), "does not cover every selector") {
		t.Fatalf("coverage-drift validation = %v", err)
	}
}

func compileTestInput(packageTarget, sharedInputDigest string) CompileGroupInput {
	return CompileGroupInput{PackageTarget: packageTarget, SemanticKey: CompileGroupSemanticGoTestNormal, SharedInputDigest: sharedInputDigest, ProfileDigest: "sha256:" + strings.Repeat("e", 64)}
}
func testCompilePlanningSelectors(t *testing.T, packageTarget string, names []string, bodies []int64) []compilePlanningSelector {
	t.Helper()
	if len(names) != len(bodies) {
		t.Fatalf("selector fixture names=%d bodies=%d", len(names), len(bodies))
	}
	input := compileTestInput(packageTarget, "sha256:"+strings.Repeat("a", 64))
	selectors := make([]compilePlanningSelector, len(names))
	for index, name := range names {
		workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, packageTarget, name, bodies[index]+1)
		if err != nil {
			t.Fatal(err)
		}
		selectors[index] = compilePlanningSelector{
			planned: PlannedWorkload{Workload: workload, EstimatedDurationMS: bodies[index] + 1},
			parent:  GateIDBackendTestWithGuard, targetKind: WorkloadTargetGoTest,
			target: GoTestTarget{Package: packageTarget, Name: name}, input: input,
			bodyEstimateMS: bodies[index], compileEstimateMS: 1, canonicalOrder: index,
		}
	}
	return selectors
}

func TestSplitMcpLSPCompilePlanningPartitionsUsesSelectorBound(t *testing.T) {
	const selectorCount = cicontract.CompileGroupMaxSelectors + 1
	selectors := make([]compilePlanningSelector, selectorCount)
	for index := range selectors {
		selectors[index] = compilePlanningSelector{
			planned:        PlannedWorkload{Workload: Workload{ID: fmt.Sprintf("mcp-lsp-selector-%03d", index)}},
			input:          CompileGroupInput{PackageTarget: AtomicMcpLSPPackageTarget},
			bodyEstimateMS: int64(index + 1), canonicalOrder: index,
		}
	}
	partitions, err := splitCompilePlanningPartitions(compilePlanningBucket{selectors: selectors})
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 2 || len(partitions[0]) > cicontract.CompileGroupMaxSelectors || len(partitions[1]) > cicontract.CompileGroupMaxSelectors {
		t.Fatalf("mcp-lsp partitions = %d sizes=%d,%d, want two bounded groups", len(partitions), len(partitions[0]), len(partitions[1]))
	}
	seen := make(map[string]bool, selectorCount)
	for _, partition := range partitions {
		for _, selector := range partition {
			id := selector.planned.Workload.ID
			if seen[id] {
				t.Fatalf("selector %q appears in multiple mcp-lsp partitions", id)
			}
			seen[id] = true
		}
	}
	if len(seen) != selectorCount {
		t.Fatalf("mcp-lsp selectors covered=%d, want %d", len(seen), selectorCount)
	}
}

func testCompileGroupFromPlanningSelectors(t *testing.T, selectors []compilePlanningSelector, compileEstimate int64, resourceClassID string) CompileGroup {
	t.Helper()
	estimates, batches, warning, err := compileGroupBatchPlan(selectors, compileEstimate, resourceClassID)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]GateID, len(selectors))
	var bodyEstimate int64
	for index, selector := range selectors {
		ids[index] = GateID(selector.planned.Workload.ID)
		bodyEstimate += selector.bodyEstimateMS
	}
	slices.Sort(ids)
	group := CompileGroup{
		PackageTarget: selectors[0].input.PackageTarget, SemanticKey: selectors[0].input.SemanticKey,
		SharedInputDigest: selectors[0].input.SharedInputDigest, ProfileDigest: selectors[0].input.ProfileDigest,
		ResourceClassID: resourceClassID, WorkloadIDs: ids, SelectorEstimates: estimates, BatchPlan: batches,
		BatchPlanWarning: warning, CompileEstimateMS: compileEstimate, BodyEstimateMS: bodyEstimate,
		EstimatedDurationMS: compileEstimate + bodyEstimate,
	}
	group.BatchPlanDigest, err = CompileGroupBatchPlanDigest(group)
	if err != nil {
		t.Fatal(err)
	}
	group.GroupID, err = CompileGroupID(group)
	if err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err != nil {
		t.Fatal(err)
	}
	return group
}
