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
		if !isSHA256Digest(workload.CommandDigest) || strings.HasPrefix(workload.CommandDigest, "sha256:") {
			t.Fatalf("workload %q digest = %q, want bare lowercase SHA-256", workload.ID, workload.CommandDigest)
		}
		if workload.ID == string(GateIDReleaseLayeredCheck) {
			if workload.Shardable {
				t.Fatal("release layered attestation is shardable")
			}
			continue
		}
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
	if parents[GateIDAIMaintenanceSelfTest] != 2 || parents[GateIDBackendTestWithGuard] != 10 || parents[GateIDFrontendFullTest] != 3 {
		t.Fatalf("expanded parent counts = %v", parents)
	}
	assertAtomicExpandedWorkloads(t, catalog)
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

func TestBuildExpandedWorkloadCatalogSplitsArchtestTopLevelTests(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	inventory := WorkloadInventory{
		GoPackages: []string{
			"./internal/alpha",
			AtomicArchtestPackageTarget,
			AtomicCodexAppPackageTarget,
		},
		GoTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestAlpha"},
			{Package: AtomicArchtestPackageTarget, Name: "TestBeta"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestTransportStart"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestTransportClose"},
		},
		GoRaceTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestAlpha"},
			{Package: AtomicArchtestPackageTarget, Name: "TestRace"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestTransportStart"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestTransportRace"},
		},
	}
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
	_, err = BuildWorkloadExecutionPlan(
		plan,
		catalog,
		DurationLedgerSnapshot{Generation: 1, Ledger: NewDurationLedger()},
		testLinuxPlanningContext(),
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
	snapshot := DurationLedgerSnapshot{Generation: 1, Ledger: DurationLedger{Version: durationLedgerVersion}}
	if _, err := BuildWorkloadExecutionPlan(plan, catalog, snapshot, PlanningContext{
		Platform: "linux/arm64", Runner: "runner", Toolchain: "toolchain",
		TargetDurationMS: FullCITargetDurationMS,
	}); err != nil {
		t.Fatalf("BuildWorkloadExecutionPlan(selected) error = %v", err)
	}
}
