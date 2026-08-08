package gate

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestDefaultWorkloadBootstrapPolicyCoversRegistryExactly(t *testing.T) {
	policy := DefaultWorkloadBootstrapPolicy()
	if err := ValidateWorkloadBootstrapPolicy(policy); err != nil {
		t.Fatalf("ValidateWorkloadBootstrapPolicy() error = %v", err)
	}
	if len(policy) != len(GateRegistry()) {
		t.Fatalf("policy entries = %d, registry entries = %d", len(policy), len(GateRegistry()))
	}
}

func TestWorkloadBootstrapPolicyRejectsMissingAndStaleGate(t *testing.T) {
	missing := DefaultWorkloadBootstrapPolicy()
	delete(missing, GateIDFrontendTest)
	if err := ValidateWorkloadBootstrapPolicy(missing); err == nil || !strings.Contains(err.Error(), "missing gate") {
		t.Fatalf("missing policy error = %v", err)
	}

	stale := DefaultWorkloadBootstrapPolicy()
	stale[GateID("removed:gate")] = WorkloadBootstrap{Kind: WorkloadKindGuard, EstimateMS: 1, Shardable: true}
	if err := ValidateWorkloadBootstrapPolicy(stale); err == nil || !strings.Contains(err.Error(), "stale gate") {
		t.Fatalf("stale policy error = %v", err)
	}
}

func TestFrontendPreflightBootstrapRemainsShardableGuard(t *testing.T) {
	bootstrap := DefaultWorkloadBootstrapPolicy()[GateIDFrontendPreflight]
	if bootstrap != (WorkloadBootstrap{Kind: WorkloadKindGuard, EstimateMS: 60000, Shardable: true}) {
		t.Fatalf("frontend preflight bootstrap = %#v", bootstrap)
	}
}

func TestBuildWorkloadCatalogUsesCanonicalPlanAndBareCommandDigest(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatalf("BuildGatePlan() error = %v", err)
	}
	catalog, err := BuildWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	if len(catalog.Workloads) != len(plan.Gates) {
		t.Fatalf("catalog workloads = %d, plan gates = %d", len(catalog.Workloads), len(plan.Gates))
	}
	for _, workload := range catalog.Workloads {
		assertCanonicalWorkload(t, workload)
	}
}

func assertCanonicalWorkload(t *testing.T, workload Workload) {
	t.Helper()
	if !isSHA256Digest(workload.CommandDigest) || strings.HasPrefix(workload.CommandDigest, "sha256:") {
		t.Fatalf("workload %q digest = %q, want bare lowercase SHA-256", workload.ID, workload.CommandDigest)
	}
	switch workload.ID {
	case string(GateIDReleaseLayeredCheck):
		if workload.Shardable {
			t.Fatal("release layered attestation is shardable")
		}
	case string(GateIDBackendNilness):
		if workload.Shardable {
			t.Fatal("nilness expansion descriptor is shardable")
		}
	default:
		if !workload.Shardable {
			t.Fatalf("workload %q unexpectedly non-shardable", workload.ID)
		}
	}
}

func TestBuildWorkloadCatalogRejectsTamperedPlan(t *testing.T) {
	plan, err := BuildGatePlan(ProfileLocalFast, registryTestSource())
	if err != nil {
		t.Fatalf("BuildGatePlan() error = %v", err)
	}
	plan.Gates[0].Argv[0] = "/tmp/untrusted"
	if _, err := BuildWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy()); err == nil {
		t.Fatal("BuildWorkloadCatalog() error = nil for tampered plan")
	}
}

func TestBuildExpandedWorkloadCatalogUsesAtomicTargets(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
		GoPackages:      []string{"./internal/alpha", "./internal/beta"},
		NestedGoModules: []string{"build/gate/runtime-proxy", "build/gate/runtime-tools", "tools/custom-check"},
		FrontendFullTests: []string{
			"src/alpha.test.ts",
			"src/beta.test.tsx",
			"scripts/runtime.test.mjs",
		},
	})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	if !catalog.Authoritative || len(catalog.Workloads) <= len(plan.Gates) {
		t.Fatalf("expanded catalog = %#v", catalog)
	}
	parents := workloadParentCounts(t, catalog)
	if parents[GateIDAIMaintenanceSelfTest] != 2 || parents[GateIDBackendTestWithGuard] != 9 || parents[GateIDFrontendFullTest] != 3 {
		t.Fatalf("expanded parent counts = %v", parents)
	}
	assertAtomicExpandedWorkloads(t, catalog)
}

func TestBuildExpandedWorkloadCatalogSplitsNilnessByGoPackage(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	inventory := WorkloadInventory{GoPackages: []string{"./internal/alpha", "./internal/beta"}}
	catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	nilness := collectNilnessPackageWorkloads(t, catalog)
	if len(nilness) != len(inventory.GoPackages) {
		t.Fatalf("nilness workloads = %d, want %d", len(nilness), len(inventory.GoPackages))
	}
}

func collectNilnessPackageWorkloads(t *testing.T, catalog WorkloadCatalog) []Workload {
	t.Helper()
	var nilness []Workload
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if parent != GateIDBackendNilness {
			continue
		}
		assertNilnessPackageWorkload(t, workload, kind, target, targeted)
		nilness = append(nilness, workload)
	}
	return nilness
}

func assertNilnessPackageWorkload(t *testing.T, workload Workload, kind WorkloadTargetKind, target string, targeted bool) {
	t.Helper()
	if !targeted || kind != WorkloadTargetGoPackage {
		t.Fatalf("nilness workload = %#v, want targeted Go package", workload)
	}
	if target != "./internal/alpha" && target != "./internal/beta" {
		t.Fatalf("nilness target = %q, want inventory package", target)
	}
}

func TestBuildExpandedWorkloadCatalogRejectsEmptyNilnessInventory(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{}); err == nil || !strings.Contains(err.Error(), "nilness") {
		t.Fatalf("empty nilness inventory error = %v, want fail-fast nilness error", err)
	}
}

func TestNilnessBootstrapBudgetIsDistributedAcrossPackages(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	packageCounts := []int{3, 6}
	var totals []int64
	for _, count := range packageCounts {
		total, seen := nilnessBudgetForPackageCount(t, plan, count)
		if seen != count {
			t.Fatalf("nilness workloads = %d, want %d", seen, count)
		}
		totals = append(totals, total)
	}
	if totals[0] != expandedNilnessTotalBootstrapEstimateMS || totals[1] != expandedNilnessTotalBootstrapEstimateMS {
		t.Fatalf("nilness bootstrap totals = %v, want fixed total %d", totals, expandedNilnessTotalBootstrapEstimateMS)
	}
}

func nilnessBudgetForPackageCount(t *testing.T, plan GatePlan, count int) (int64, int) {
	t.Helper()
	packages := make([]string, count)
	for index := range packages {
		packages[index] = fmt.Sprintf("./internal/nilness%02d", index)
	}
	catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{GoPackages: packages})
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog(%d packages) error = %v", count, err)
	}
	return sumNilnessBootstrapWorkloads(t, catalog)
}

func sumNilnessBootstrapWorkloads(t *testing.T, catalog WorkloadCatalog) (int64, int) {
	t.Helper()
	var total int64
	seen := 0
	for _, workload := range catalog.Workloads {
		parent, kind, _, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if parent != GateIDBackendNilness {
			continue
		}
		if !targeted || kind != WorkloadTargetGoPackage || workload.BootstrapEstimateMS < 1 {
			t.Fatalf("nilness workload = %#v, want positive package estimate", workload)
		}
		total += workload.BootstrapEstimateMS
		seen++
	}
	return total, seen
}

func TestExpandedCatalogsIncludeExactCodeSizeGuardOnceAcrossProfiles(t *testing.T) {
	inventory := WorkloadInventory{
		GoPackages: []string{AtomicArchtestPackageTarget, "./internal/alpha"},
		GoTests:    []GoTestTarget{{Package: AtomicArchtestPackageTarget, Name: "TestCodeSizeGuard"}},
		GoRaceTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestCodeSizeGuard"},
			{Package: AtomicArchtestPackageTarget, Name: "TestRace"},
		},
	}
	for _, profile := range []Profile{ProfileLocalFast, ProfilePush, ProfileRelease} {
		t.Run(string(profile), func(t *testing.T) {
			plan, err := BuildGatePlan(profile, registryTestSource())
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
			if err != nil {
				t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
			}
			assertExactCodeSizeGuardWorkload(t, catalog, profile)
		})
	}
}

func assertExactCodeSizeGuardWorkload(t *testing.T, catalog WorkloadCatalog, profile Profile) {
	t.Helper()
	matches := exactCodeSizeGuardWorkloads(t, catalog)
	if len(matches) != 1 {
		t.Fatalf("profile %q code-size guard workloads = %d, want exactly one: %#v", profile, len(matches), matches)
	}
	if got, err := RequiredCheckForWorkloadID(matches[0].ID); err != nil || got != "normal" {
		t.Fatalf("profile %q code-size guard required-check = %q, error = %v", profile, got, err)
	}
	digest, err := workloadProgramDigest(matches[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if matches[0].CommandDigest != digest {
		t.Fatalf("profile %q code-size guard command digest drifted", profile)
	}
}

func exactCodeSizeGuardWorkloads(t *testing.T, catalog WorkloadCatalog) []Workload {
	t.Helper()
	var matches []Workload
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if parent != GateIDBackendTestWithGuard || !targeted || kind != WorkloadTargetGoTest {
			continue
		}
		testTarget, err := ParseGoTestTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		if testTarget.Package == AtomicArchtestPackageTarget && testTarget.Name == "TestCodeSizeGuard" {
			matches = append(matches, workload)
		}
	}
	return matches
}

func TestBuildExpandedWorkloadCatalogSplitsPlaywrightDescribes(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
		GoPackages: []string{"./internal/alpha"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		playwrightBusinessReadSurfacesTarget,
		playwrightBusinessChatBridgeTarget,
		playwrightDesktopShellTarget,
		playwrightDesktopBusinessPagesTarget,
		playwrightDesktopReadSettingsTarget,
	}
	var got []string
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if parent != GateIDFrontendE2E {
			continue
		}
		if !targeted || kind != WorkloadTargetPlaywright || workload.BootstrapEstimateMS != expandedPlaywrightBootstrapEstimateMS {
			t.Fatalf("Playwright workload = %#v, want targeted medium-tier describe workload", workload)
		}
		got = append(got, target)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Playwright describe targets = %v, want %v", got, want)
	}
}

func workloadParentCounts(t *testing.T, catalog WorkloadCatalog) map[GateID]int {
	t.Helper()
	parents := make(map[GateID]int)
	for _, workload := range catalog.Workloads {
		parent, err := WorkloadParentGateID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		parents[parent]++
	}
	return parents
}

func assertAtomicExpandedWorkloads(t *testing.T, catalog WorkloadCatalog) {
	t.Helper()
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch parent {
		case GateIDBackendTestWithGuard:
			assertAtomicBackendGuard(t, workload, kind, target, targeted)
		case GateIDAIMaintenanceSelfTest:
			assertAtomicAIMaintenanceGuard(t, workload, kind, target, targeted)
		}
	}
}

func assertAtomicBackendGuard(t *testing.T, workload Workload, kind WorkloadTargetKind, target string, targeted bool) {
	t.Helper()
	if !targeted || kind != WorkloadTargetGoGuard {
		return
	}
	if target == GoGuardTargetCanonical || workload.BootstrapEstimateMS > FullCITargetDurationMS {
		t.Fatalf("backend guard was not atomically bounded: target=%q estimate=%d", target, workload.BootstrapEstimateMS)
	}
	if strings.HasPrefix(target, goGuardTargetNestedModulePrefix) {
		if _, err := ParseNestedGoModuleGuardTarget(target); err != nil {
			t.Fatalf("dynamic nested module target %q: %v", target, err)
		}
	}
}

func assertAtomicAIMaintenanceGuard(t *testing.T, workload Workload, kind WorkloadTargetKind, target string, targeted bool) {
	t.Helper()
	if !targeted || kind != WorkloadTargetGoGuard ||
		(target != GoGuardTargetAIMaintenanceUnit && target != GoGuardTargetAIMaintenanceGate) ||
		workload.BootstrapEstimateMS > FullCITargetDurationMS {
		t.Fatalf("AI maintenance gate was not atomically bounded: id=%q estimate=%d", workload.ID, workload.BootstrapEstimateMS)
	}
}

func TestBuildExpandedWorkloadCatalogSplitsAtomicGoTopLevelTests(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	inventory := atomicCatalogInventory()
	normal, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	calibration, err := BuildCalibrationWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	assertAtomicArchtestCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestAlpha", "TestBeta"})
	assertAtomicArchtestCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestAlpha", "TestRace"})
	assertAtomicCodexAppCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestTransportClose", "TestTransportStart"})
	assertAtomicCodexAppCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestTransportRace", "TestTransportStart"})
	assertAtomicAgentRuntimeCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestAgentRuntimeEnv", "TestAgentRuntimeMain"})
	assertAtomicAgentRuntimeCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestAgentRuntimeMain", "TestAgentRuntimeRace"})
	assertAtomicAgentTerminalCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestAgentTerminalMain", "TestAgentTerminalRecovery"})
	assertAtomicAgentTerminalCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestAgentTerminalMain", "TestAgentTerminalRecovery"})
	assertAtomicAppCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestAppModuleGraphIsClosed", "TestRunDesktopPreDrain"})
	assertAtomicAppCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch", "TestRunDesktopPreDrain"})
	assertAtomicUpdaterCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestUpdaterCandidateCleanup", "TestUpdaterRollbackEntries"})
	assertAtomicUpdaterCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestUpdaterCandidateCleanup", "TestUpdaterRollbackEntries"})
	assertAtomicTaskDAGCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestTaskDAGStore", "TestTaskDAGWakeup"})
	assertAtomicTaskDAGCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestTaskDAGStore", "TestTaskDAGWakeup"})
	assertAtomicSQLiteCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestSQLiteCommon", "TestSQLiteNormal"})
	assertAtomicSQLiteCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestSQLiteCommon", "TestSQLiteRace"})
	assertAtomicGateCatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestGateAtomicCommon", "TestGateAtomicNormal"})
	assertAtomicGateCatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestGateAtomicCommon", "TestGateAtomicRace"})
	assertAtomicRemoteCICatalog(t, normal, GateIDBackendTestWithGuard, []string{"TestRemoteCIAtomicCommon", "TestRemoteCIAtomicNormal"})
	assertAtomicRemoteCICatalog(t, calibration, GateIDBackendTestGuardWithRace, []string{"TestRemoteCIAtomicCommon", "TestRemoteCIAtomicRace"})
}

func atomicCatalogInventory() WorkloadInventory {
	return WorkloadInventory{
		GoPackages: append([]string{"./internal/alpha"}, AtomicGoPackageTargets()...),
		GoTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestAlpha"},
			{Package: AtomicArchtestPackageTarget, Name: "TestBeta"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestTransportStart"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestTransportClose"},
			{Package: AtomicAgentRuntimePackageTarget, Name: "TestAgentRuntimeMain"},
			{Package: AtomicAgentRuntimePackageTarget, Name: "TestAgentRuntimeEnv"},
			{Package: AtomicAgentTerminalPackageTarget, Name: "TestAgentTerminalMain"},
			{Package: AtomicAgentTerminalPackageTarget, Name: "TestAgentTerminalRecovery"},
			{Package: AtomicAppPackageTarget, Name: "TestRunDesktopPreDrain"},
			{Package: AtomicAppPackageTarget, Name: "TestAppModuleGraphIsClosed"},
			{Package: AtomicUpdaterPackageTarget, Name: "TestUpdaterRollbackEntries"},
			{Package: AtomicUpdaterPackageTarget, Name: "TestUpdaterCandidateCleanup"},
			{Package: AtomicTaskDAGPackageTarget, Name: "TestTaskDAGStore"},
			{Package: AtomicTaskDAGPackageTarget, Name: "TestTaskDAGWakeup"},
			{Package: AtomicSQLitePackageTarget, Name: "TestSQLiteCommon"},
			{Package: AtomicSQLitePackageTarget, Name: "TestSQLiteNormal"},
			{Package: AtomicGatePackageTarget, Name: "TestGateAtomicCommon"},
			{Package: AtomicGatePackageTarget, Name: "TestGateAtomicNormal"},
			{Package: AtomicRemoteCIPackageTarget, Name: "TestRemoteCIAtomicCommon"},
			{Package: AtomicRemoteCIPackageTarget, Name: "TestRemoteCIAtomicNormal"},
		},
		GoRaceTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestAlpha"},
			{Package: AtomicArchtestPackageTarget, Name: "TestRace"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestTransportStart"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestTransportRace"},
			{Package: AtomicAgentRuntimePackageTarget, Name: "TestAgentRuntimeMain"},
			{Package: AtomicAgentRuntimePackageTarget, Name: "TestAgentRuntimeRace"},
			{Package: AtomicAgentTerminalPackageTarget, Name: "TestAgentTerminalMain"},
			{Package: AtomicAgentTerminalPackageTarget, Name: "TestAgentTerminalRecovery"},
			{Package: AtomicAppPackageTarget, Name: "TestRunDesktopPreDrain"},
			{Package: AtomicAppPackageTarget, Name: "TestRecoveryRestoreAndGuardConvergeOneTokenBoundLaunch"},
			{Package: AtomicUpdaterPackageTarget, Name: "TestUpdaterRollbackEntries"},
			{Package: AtomicUpdaterPackageTarget, Name: "TestUpdaterCandidateCleanup"},
			{Package: AtomicTaskDAGPackageTarget, Name: "TestTaskDAGStore"},
			{Package: AtomicTaskDAGPackageTarget, Name: "TestTaskDAGWakeup"},
			{Package: AtomicSQLitePackageTarget, Name: "TestSQLiteCommon"},
			{Package: AtomicSQLitePackageTarget, Name: "TestSQLiteRace"},
			{Package: AtomicGatePackageTarget, Name: "TestGateAtomicCommon"},
			{Package: AtomicGatePackageTarget, Name: "TestGateAtomicRace"},
			{Package: AtomicRemoteCIPackageTarget, Name: "TestRemoteCIAtomicCommon"},
			{Package: AtomicRemoteCIPackageTarget, Name: "TestRemoteCIAtomicRace"},
		},
	}
}

func TestAtomicGoPackageTargetsReturnsStableClone(t *testing.T) {
	want := []string{
		AtomicArchtestPackageTarget,
		AtomicCodexAppPackageTarget,
		AtomicAgentRuntimePackageTarget,
		AtomicAgentTerminalPackageTarget,
		AtomicAppPackageTarget,
		AtomicUpdaterPackageTarget,
		AtomicTaskDAGPackageTarget,
		AtomicSQLitePackageTarget,
		AtomicGatePackageTarget,
		AtomicRemoteCIPackageTarget,
	}
	first := AtomicGoPackageTargets()
	if !slices.Equal(first, want) {
		t.Fatalf("atomic Go package targets = %v, want %v", first, want)
	}
	first[0] = "./mutated"
	second := AtomicGoPackageTargets()
	if !slices.Equal(second, want) {
		t.Fatalf("atomic Go package targets leaked caller mutation: %v", second)
	}
}

func assertAtomicAgentRuntimeCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicAgentRuntimePackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q agent-runtime=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicAgentTerminalCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicAgentTerminalPackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q agent-terminal=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicAppCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicAppPackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q internal/app=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicUpdaterCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicUpdaterPackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q updater=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicTaskDAGCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicTaskDAGPackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q taskdag=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicSQLiteCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicSQLitePackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q sqlite=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicGateCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicGatePackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q gate=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicRemoteCICatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicRemoteCIPackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q remoteci=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicCodexAppCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	packageFound, got := atomicPackageCatalogTargets(t, catalog, parent, AtomicCodexAppPackageTarget)
	if !packageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q codexapp=%v tests=%v want=%v", parent, packageFound, got, want)
	}
}

func assertAtomicArchtestCatalog(t *testing.T, catalog WorkloadCatalog, parent GateID, want []string) {
	t.Helper()
	alphaPackageFound, got := atomicArchtestCatalogTargets(t, catalog, parent)
	if !alphaPackageFound || !slices.Equal(got, want) {
		t.Fatalf("parent %q alpha=%v archtest tests=%v want=%v", parent, alphaPackageFound, got, want)
	}
}

func atomicArchtestCatalogTargets(t *testing.T, catalog WorkloadCatalog, parent GateID) (bool, []string) {
	return atomicPackageCatalogTargets(t, catalog, parent, AtomicArchtestPackageTarget)
}

func atomicPackageCatalogTargets(t *testing.T, catalog WorkloadCatalog, parent GateID, atomicPackage string) (bool, []string) {
	t.Helper()
	var tests []string
	packageFound := false
	for _, workload := range catalog.Workloads {
		workloadParent, kind, target, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if workloadParent != parent || !targeted {
			continue
		}
		found, testName := classifyAtomicPackageWorkload(t, parent, kind, target, atomicPackage)
		packageFound = packageFound || found
		if testName != "" {
			tests = append(tests, testName)
		}
	}
	return packageFound, tests
}

func classifyAtomicPackageWorkload(t *testing.T, parent GateID, kind WorkloadTargetKind, target, atomicPackage string) (bool, string) {
	t.Helper()
	if kind == WorkloadTargetGoPackage {
		if target == atomicPackage {
			t.Fatalf("%s remained a package workload for %q", atomicPackage, parent)
		}
		return target == "./internal/alpha", ""
	}
	if kind != WorkloadTargetGoTest {
		return false, ""
	}
	parsed, err := ParseGoTestTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Package == atomicPackage {
		return false, parsed.Name
	}
	return false, ""
}

func TestBuildExpandedWorkloadCatalogKeepsCanonicalGateWithoutGoInventory(t *testing.T) {
	plan, err := BuildGatePlan(ProfileLocalFast, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, workload := range catalog.Workloads {
		parent, _, _, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if parent == GateIDBackendTestWithGuard {
			count++
			if targeted {
				t.Fatalf("empty Go inventory produced targeted backend workload %q", workload.ID)
			}
		}
	}
	if count != 1 {
		t.Fatalf("empty Go inventory backend workloads = %d, want 1 canonical gate", count)
	}
}

func TestBuildCalibrationWorkloadCatalogRacesEveryGoPackage(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	inventory := WorkloadInventory{
		GoPackages: []string{"./internal/alpha", "./pkg/beta", "./scripts"},
	}
	normal, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	calibration, err := BuildCalibrationWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	countParent := func(catalog WorkloadCatalog, parent GateID) int {
		count := 0
		for _, workload := range catalog.Workloads {
			got, parentErr := WorkloadParentGateID(workload.ID)
			if parentErr != nil {
				t.Fatal(parentErr)
			}
			if got == parent {
				count++
			}
		}
		return count
	}
	if got := countParent(calibration, GateIDBackendTestGuardWithRace); got != len(inventory.GoPackages) {
		t.Fatalf("calibration race workloads = %d, want %d", got, len(inventory.GoPackages))
	}
	if got := countParent(normal, GateIDBackendTestGuardWithRace); got >= len(inventory.GoPackages) {
		t.Fatalf("normal race workloads unexpectedly cover every package: %d", got)
	}
}

func TestCalibrationBootstrapPlansRepositoryScaleWithinTarget(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	packages := make([]string, 273)
	for index := range packages {
		packages[index] = fmt.Sprintf("./internal/calibration/package%03d", index)
	}
	catalog, err := BuildCalibrationWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
		GoPackages: packages, FrontendFullTests: []string{"src/calibration.test.ts"},
	})
	if err != nil {
		t.Fatal(err)
	}
	context := testLinuxPlanningContext()
	context.Calibration = true
	context.CalibrationResourceClassID = "calibration"
	context.CalibrationResourceCPU = 4
	context.CalibrationResourceMemoryGiB = 8
	_, err = BuildWorkloadExecutionPlan(
		plan,
		catalog,
		DurationLedgerSnapshot{Generation: 1, Ledger: NewDurationLedger()},
		context,
	)
	if err != nil {
		t.Fatalf("repository-scale calibration plan: %v", err)
	}
}

func TestBuildSelectedTestWorkloadCatalogIsNonAuthoritative(t *testing.T) {
	plan, err := BuildGatePlan(ProfileLocalFast, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildSelectedTestWorkloadCatalog(plan, WorkloadInventory{
		GoPackages:        []string{"./internal/alpha"},
		FrontendFullTests: []string{"src/alpha.test.ts"},
	})
	if err != nil {
		t.Fatalf("BuildSelectedTestWorkloadCatalog() error = %v", err)
	}
	if catalog.Authoritative || len(catalog.Workloads) != 2 {
		t.Fatalf("selected catalog = %#v", catalog)
	}
	context := PlanningContext{Platform: "linux/arm64", Runner: "runner", Toolchain: "toolchain", TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: "snapshot-selected"}
	snapshot := DurationLedgerSnapshot{Generation: 1, Ledger: testPlanningLedger(context, nil)}
	if _, err := BuildWorkloadExecutionPlan(plan, catalog, snapshot, context); err != nil {
		t.Fatalf("BuildWorkloadExecutionPlan(selected) error = %v", err)
	}
}
