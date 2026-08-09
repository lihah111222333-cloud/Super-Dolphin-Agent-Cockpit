package gate

import (
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestSuperDolphinGateDeclaredAtomicPackageRequiresExactSelectorInventory(t *testing.T) {
	_, _, err := splitAtomicGoTestTargets(
		GateIDBackendTestWithGuard,
		[]string{AtomicSuperDolphinGatePackageTarget},
		WorkloadInventory{},
	)
	if err == nil || !strings.Contains(err.Error(), AtomicSuperDolphinGatePackageTarget) {
		t.Fatalf("splitAtomicGoTestTargets() error = %v, want super-gate exact inventory failure", err)
	}
}

func TestSuperDolphinGateUsesBoundedCompileGroups(t *testing.T) {
	selectors := testCompilePlanningSelectors(
		t,
		AtomicSuperDolphinGatePackageTarget,
		superDolphinPlanningSelectorNames(cicontract.CompileGroupMaxSelectors+1),
		make([]int64, cicontract.CompileGroupMaxSelectors+1),
	)
	partitions, err := splitCompilePlanningPartitions(compilePlanningBucket{selectors: selectors})
	if err != nil {
		t.Fatal(err)
	}
	if len(partitions) != 2 {
		t.Fatalf("super-dolphin-gate partitions = %d, want 2", len(partitions))
	}
	for _, partition := range partitions {
		if len(partition) == 0 || len(partition) > cicontract.CompileGroupMaxSelectors {
			t.Fatalf("super-dolphin-gate partition size = %d, want <=%d", len(partition), cicontract.CompileGroupMaxSelectors)
		}
	}
}

func TestSuperDolphinGateCompileGroupRejectsOverBoundManifest(t *testing.T) {
	input := compileTestInput(AtomicSuperDolphinGatePackageTarget, "sha256:"+strings.Repeat("c", 64))
	names := superDolphinPlanningSelectorNames(cicontract.CompileGroupMaxSelectors + 1)
	workloads, inputs := compileTestWorkloads(t, input.PackageTarget, names, 1_000, input)
	durationIndex, err := BuildDurationSampleIndex(testPlanningLedger(testCalibrationPlanningContext(), nil), testCalibrationPlanningContext())
	if err != nil {
		t.Fatal(err)
	}
	_, groups, err := planLPTWithCompileInputs(testWorkloadCatalog(workloads...), durationIndex, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("super-dolphin-gate planner groups = %d, want 2", len(groups))
	}
	for _, group := range groups {
		if len(group.BatchPlan) != 1 {
			t.Fatalf("super-dolphin-gate compile group batches = %d, want one", len(group.BatchPlan))
		}
	}
	groups[0].WorkloadIDs = append(groups[0].WorkloadIDs, groups[1].WorkloadIDs...)
	if len(groups[0].WorkloadIDs) <= cicontract.CompileGroupMaxSelectors {
		t.Fatal("test manifest did not exceed compile-group selector bound")
	}
	if err := groups[0].Validate(); err == nil || !strings.Contains(err.Error(), "exceeds selector bound") {
		t.Fatalf("over-bound super-dolphin-gate group validation error = %v", err)
	}
}

func TestSuperDolphinGateRejectsManualSelectorAndKeepsCopylocksOwner(t *testing.T) {
	manualTarget, err := encodeGoTestTarget(GoTestTarget{Package: "./cmd/codex_smoketest", Name: "TestManual"})
	if err != nil {
		t.Fatal(err)
	}
	manualID, err := targetWorkloadID(GateIDBackendTestWithGuard, workloadTargetGoTest, manualTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := compileGroupSelectorSafetyExpectation(GateID(manualID)); err == nil || !strings.Contains(err.Error(), "manual") {
		t.Fatalf("manual selector error = %v", err)
	}
	guards, err := splitGoGuardWorkloadSpecs(GateIDBackendTestWithGuard, workloadTargetGoPackage, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, guard := range guards {
		counts[guard.target]++
	}
	for _, target := range []string{GoGuardTargetCopylocksProvider, GoGuardTargetCopylocksPlatform, GoGuardTargetCopylocksThread} {
		if counts[target] != 1 {
			t.Fatalf("copylocks owner %q count = %d, want one", target, counts[target])
		}
	}
}

func TestSuperDolphinGateHelpersAreRemovedBeforeCatalogExpansion(t *testing.T) {
	normalized, err := normalizeWorkloadInventory(WorkloadInventory{
		GoPackages: []string{AtomicSuperDolphinGatePackageTarget},
		GoTests: []GoTestTarget{
			{Package: AtomicSuperDolphinGatePackageTarget, Name: "TestRemoteHookConcurrentProcessHelper"},
			{Package: AtomicSuperDolphinGatePackageTarget, Name: "TestRemoteHookConcurrentProcessesKeepInheritedTokenAndDeliveryIsolated"},
		},
		GoRaceTests: []GoTestTarget{
			{Package: AtomicSuperDolphinGatePackageTarget, Name: "TestRemoteHookConcurrentProcessHelper"},
			{Package: AtomicSuperDolphinGatePackageTarget, Name: "TestRemoteCIAgentTokenActualValueContinuesPastHandshake"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(normalized.GoTests, []GoTestTarget{{Package: AtomicSuperDolphinGatePackageTarget, Name: "TestRemoteHookConcurrentProcessesKeepInheritedTokenAndDeliveryIsolated"}}) {
		t.Fatalf("normalized normal selectors = %#v", normalized.GoTests)
	}
	if !slices.Equal(normalized.GoRaceTests, []GoTestTarget{{Package: AtomicSuperDolphinGatePackageTarget, Name: "TestRemoteCIAgentTokenActualValueContinuesPastHandshake"}}) {
		t.Fatalf("normalized race selectors = %#v", normalized.GoRaceTests)
	}
}

func TestCanonicalGoTestHelperRegistryHasAllCurrentOwners(t *testing.T) {
	if got := len(CanonicalGoTestHelperTargets()); got != 15 {
		t.Fatalf("canonical helper registry count = %d, want 15", got)
	}
}

func superDolphinPlanningSelectorNames(count int) []string {
	names := make([]string, count)
	for index := range names {
		names[index] = "TestSuperDolphinGate" + formatSuperDolphinPlanningIndex(index)
	}
	return names
}

func formatSuperDolphinPlanningIndex(index int) string {
	return "Selector" + string(rune('A'+index%26)) + string(rune('a'+index/26%26))
}
