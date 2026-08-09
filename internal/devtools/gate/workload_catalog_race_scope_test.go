package gate

import (
	"slices"
	"strings"
	"testing"
)

func TestRaceCatalogExcludesNormalOnlyStaticCodeSizeGuard(t *testing.T) {
	plan := mustRaceScopePlan(t)
	normal, calibration := mustRaceScopeCatalogs(t, plan, raceScopeInventory())
	assertExactCodeSizeGuardWorkload(t, normal, ProfileRelease)
	for name, catalog := range map[string]WorkloadCatalog{"normal": normal, "calibration": calibration} {
		t.Run(name, func(t *testing.T) {
			assertRaceArchtestCatalog(t, catalog)
		})
	}
	assertRaceScopeIdentitySeparation(t, normal)
}

func TestRaceCatalogRejectsStaticOnlyInventoryInsteadOfPackageFallback(t *testing.T) {
	plan := mustRaceScopePlan(t)
	_, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
		GoPackages: []string{AtomicArchtestPackageTarget},
		GoRaceTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestCodeSizeGuard"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "missing exact selectors") || !strings.Contains(err.Error(), AtomicArchtestPackageTarget) {
		t.Fatalf("static-only race inventory error = %v, want fail-fast exclusion error", err)
	}
}

func TestRaceCatalogRejectsAnyAtomicPackageWithIncompleteInventory(t *testing.T) {
	plan := mustRaceScopePlan(t)
	_, err := BuildCalibrationWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
		GoPackages: []string{AtomicArchtestPackageTarget, AtomicCodexAppPackageTarget},
		GoRaceTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestCodeSizeGuard"},
			{Package: AtomicCodexAppPackageTarget, Name: "TestCodexRace"},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "missing exact selectors") || !strings.Contains(err.Error(), AtomicArchtestPackageTarget) || !strings.Contains(err.Error(), AtomicCodexAppPackageTarget) {
		t.Fatalf("incomplete atomic inventory error = %v, want fail-fast package coverage error", err)
	}
}

func mustRaceScopePlan(t *testing.T) GatePlan {
	t.Helper()
	plan, err := BuildGatePlan(ProfileRelease, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func raceScopeInventory() WorkloadInventory {
	return WorkloadInventory{
		GoPackages: []string{AtomicArchtestPackageTarget},
		GoTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestCodeSizeGuard"},
		},
		GoRaceTests: []GoTestTarget{
			{Package: AtomicArchtestPackageTarget, Name: "TestCodeSizeGuard"},
			{Package: AtomicArchtestPackageTarget, Name: "TestRace"},
		},
	}
}

func mustRaceScopeCatalogs(t *testing.T, plan GatePlan, inventory WorkloadInventory) (WorkloadCatalog, WorkloadCatalog) {
	t.Helper()
	normal, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
	if err != nil {
		t.Fatalf("BuildExpandedWorkloadCatalog() error = %v", err)
	}
	calibration, err := BuildCalibrationWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
	if err != nil {
		t.Fatalf("BuildCalibrationWorkloadCatalog() error = %v", err)
	}
	return normal, calibration
}

func assertRaceScopeIdentitySeparation(t *testing.T, normal WorkloadCatalog) {
	t.Helper()
	normalCodeSize, ok := catalogGoTestWorkload(t, normal, GateIDBackendTestWithGuard, AtomicArchtestPackageTarget, "TestCodeSizeGuard")
	if !ok {
		t.Fatal("normal catalog lost TestCodeSizeGuard")
	}
	raceCodeSizeTarget, err := encodeGoTestTarget(GoTestTarget{Package: AtomicArchtestPackageTarget, Name: "TestCodeSizeGuard"})
	if err != nil {
		t.Fatal(err)
	}
	raceCodeSizeID, err := targetWorkloadID(GateIDBackendTestGuardWithRace, workloadTargetGoTest, raceCodeSizeTarget)
	if err != nil {
		t.Fatal(err)
	}
	normalDigest, err := WorkloadExecutionDigest(normalCodeSize.ID)
	if err != nil {
		t.Fatal(err)
	}
	raceDigest, err := WorkloadExecutionDigest(raceCodeSizeID)
	if err != nil {
		t.Fatal(err)
	}
	if normalDigest == raceDigest {
		t.Fatal("normal and race code-size identities unexpectedly share execution digest")
	}
	assertRequiredCheck(t, normalCodeSize.ID, "normal")
	assertRequiredCheck(t, raceCodeSizeID, "race")
}

func assertRequiredCheck(t *testing.T, workloadID, want string) {
	t.Helper()
	got, err := RequiredCheckForWorkloadID(workloadID)
	if err != nil || string(got) != want {
		t.Fatalf("workload %q required check = %q, error = %v; want %q", workloadID, got, err, want)
	}
}

func assertRaceArchtestCatalog(t *testing.T, catalog WorkloadCatalog) {
	t.Helper()
	selectors := raceArchtestSelectors(t, catalog)
	if !slices.Equal(selectors, []string{"TestRace"}) {
		t.Fatalf("race archtest selectors = %v, want [TestRace]", selectors)
	}
	raceWorkload, ok := catalogGoTestWorkload(t, catalog, GateIDBackendTestGuardWithRace, AtomicArchtestPackageTarget, "TestRace")
	if !ok {
		t.Fatal("race catalog lost TestRace")
	}
	digest, err := WorkloadExecutionDigest(raceWorkload.ID)
	if err != nil || raceWorkload.CommandDigest != digest {
		t.Fatalf("race TestRace digest = %q, expected %q, error = %v", raceWorkload.CommandDigest, digest, err)
	}
}

func raceCatalogPackageTargets(t *testing.T, catalog WorkloadCatalog) []string {
	t.Helper()
	seen := make(map[string]struct{})
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if parent != GateIDBackendTestGuardWithRace || !targeted {
			continue
		}
		packageTarget := raceCatalogPackageTarget(t, kind, target)
		if packageTarget != "" {
			seen[packageTarget] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for packageTarget := range seen {
		result = append(result, packageTarget)
	}
	slices.Sort(result)
	return result
}

func raceCatalogPackageTarget(t *testing.T, kind WorkloadTargetKind, target string) string {
	t.Helper()
	switch kind {
	case WorkloadTargetGoPackage:
		return target
	case WorkloadTargetGoTest:
		testTarget, err := ParseGoTestTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		return testTarget.Package
	default:
		return ""
	}
}

func raceArchtestSelectors(t *testing.T, catalog WorkloadCatalog) []string {
	t.Helper()
	var selectors []string
	for _, workload := range catalog.Workloads {
		collectRaceArchtestSelector(t, workload, &selectors)
	}
	return selectors
}

func collectRaceArchtestSelector(t *testing.T, workload Workload, selectors *[]string) {
	t.Helper()
	parent, kind, target, targeted, err := ParseWorkloadID(workload.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parent != GateIDBackendTestGuardWithRace || !targeted {
		return
	}
	if kind == WorkloadTargetGoPackage && target == AtomicArchtestPackageTarget {
		t.Fatal("race catalog fell back to the whole archtest package")
	}
	if kind != WorkloadTargetGoTest {
		return
	}
	testTarget, err := ParseGoTestTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if testTarget.Package != AtomicArchtestPackageTarget {
		return
	}
	if testTarget.Name == "TestCodeSizeGuard" {
		t.Fatal("race catalog retained normal-only TestCodeSizeGuard")
	}
	*selectors = append(*selectors, testTarget.Name)
}

func catalogGoTestWorkload(t *testing.T, catalog WorkloadCatalog, parent GateID, packageTarget, name string) (Workload, bool) {
	t.Helper()
	for _, workload := range catalog.Workloads {
		gotParent, kind, target, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if gotParent != parent || kind != WorkloadTargetGoTest || !targeted {
			continue
		}
		testTarget, err := ParseGoTestTarget(target)
		if err != nil {
			t.Fatal(err)
		}
		if testTarget.Package == packageTarget && testTarget.Name == name {
			return workload, true
		}
	}
	return Workload{}, false
}
