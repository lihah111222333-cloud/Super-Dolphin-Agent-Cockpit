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
	heavyCount := (selectorCount + cicontract.ArchtestMaxSelectorsPerCompileGroup - 1) / cicontract.ArchtestMaxSelectorsPerCompileGroup
	selectors := archtestLPTTestSelectors(selectorCount, heavyCount)
	partitions, err := splitCompilePlanningPartitions(compilePlanningBucket{selectors: selectors})
	if err != nil {
		t.Fatal(err)
	}
	wantPartitions := (selectorCount + cicontract.ArchtestMaxSelectorsPerCompileGroup - 1) / cicontract.ArchtestMaxSelectorsPerCompileGroup
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
		if len(partition) == 0 || len(partition) > cicontract.ArchtestMaxSelectorsPerCompileGroup {
			t.Fatalf("partition %d size = %d, want 1..%d", index, len(partition), cicontract.ArchtestMaxSelectorsPerCompileGroup)
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
