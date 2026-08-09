package gate

import (
	"slices"
	"strings"
	"testing"
)

func TestExpandedFrontendPreflightCatalogUsesAllowlistWithoutAggregate(t *testing.T) {
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
	var targets []string
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if parent != GateIDFrontendPreflight {
			continue
		}
		if !targeted {
			t.Fatalf("frontend preflight aggregate workload leaked into expanded catalog: %#v", workload)
		}
		if kind != WorkloadTargetFrontendGuard || !workload.Shardable {
			t.Fatalf("frontend preflight workload = %#v, want shardable targeted guard", workload)
		}
		targets = append(targets, target)
	}
	if !slices.Equal(targets, FrontendPreflightTargets()) {
		t.Fatalf("frontend preflight targets = %v, want %v", targets, FrontendPreflightTargets())
	}
}

func TestFrontendPreflightEstimateFailsClosedForAllowlistDrift(t *testing.T) {
	if _, err := expandedTargetWorkloadWithEstimate(GateIDFrontendPreflight, workloadTargetFrontendGuard, "unregistered-target", 0); err == nil {
		t.Fatal("unknown frontend preflight target received a fallback bootstrap estimate")
	}
}

func TestBuildWorkloadCatalogUsesBodyOnlyFrontendSuiteForNonLocalProfiles(t *testing.T) {
	for _, profile := range []Profile{ProfileRemoteRequired, ProfilePromotion, ProfileRelease} {
		plan, err := BuildGatePlan(profile, registryTestSource())
		if err != nil {
			t.Fatal(err)
		}
		catalog, err := BuildWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy())
		if err != nil {
			t.Fatalf("BuildWorkloadCatalog(%s): %v", profile, err)
		}
		assertProfileFrontendSuiteWorkloads(t, profile, catalog)
	}
}

func assertProfileFrontendSuiteWorkloads(t *testing.T, profile Profile, catalog WorkloadCatalog) {
	t.Helper()
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if parent != GateIDFrontendTest && parent != GateIDFrontendFullTest {
			continue
		}
		assertFrontendSuiteWorkload(t, profile, workload, parent, kind, target, targeted)
	}
}

func assertFrontendSuiteWorkload(t *testing.T, profile Profile, workload Workload, parent GateID, kind WorkloadTargetKind, target string, targeted bool) {
	t.Helper()
	wantTarget := FrontendChangedSuiteCarrierTarget
	if parent == GateIDFrontendFullTest {
		wantTarget = FrontendFullSuiteCarrierTarget
	}
	if !targeted || kind != WorkloadTargetVitest || target != wantTarget {
		t.Fatalf("%s %s fallback = %#v", profile, parent, workload)
	}
}

func TestBuildWorkloadCatalogKeepsLocalFrontendTestParent(t *testing.T) {
	plan, err := BuildGatePlan(ProfileLocalFast, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range catalog.Workloads {
		if workload.ID != string(GateIDFrontendTest) {
			continue
		}
		if !workload.Shardable {
			t.Fatalf("local FrontendTest parent is not executable: %#v", workload)
		}
		return
	}
	t.Fatal("local catalog omitted raw FrontendTest parent")
}

func TestBuildExpandedLocalSplitsFrontendTestBodyAndPreflight(t *testing.T) {
	plan, err := BuildGatePlan(ProfileLocalFast, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{
		GoPackages: []string{"./internal/alpha"}, FrontendChangedTests: []string{"src/changed.test.js"},
	})
	if err != nil {
		t.Fatal(err)
	}
	changedTests, preflightTargets := localFrontendTestTargets(t, catalog)
	if !slices.Equal(changedTests, []string{"src/changed.test.js"}) {
		t.Fatalf("remote local-fast changed tests = %v", changedTests)
	}
	if !slices.Equal(preflightTargets, FrontendPreflightTargets()) {
		t.Fatalf("remote local-fast preflight targets = %v, want %v", preflightTargets, FrontendPreflightTargets())
	}
}

func localFrontendTestTargets(t *testing.T, catalog WorkloadCatalog) ([]string, []string) {
	t.Helper()
	var changedTests []string
	var preflightTargets []string
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if parent != GateIDFrontendTest {
			continue
		}
		if !targeted {
			t.Fatalf("remote local-fast catalog leaked raw FrontendTest parent: %#v", workload)
		}
		switch kind {
		case WorkloadTargetVitest:
			if preflightTarget, ok := ParseFrontendPreflightCarrierTarget(target); ok {
				preflightTargets = append(preflightTargets, preflightTarget)
			} else {
				changedTests = append(changedTests, target)
			}
		default:
			t.Fatalf("remote local-fast frontend workload = %#v", workload)
		}
	}
	return changedTests, preflightTargets
}

func TestBuildExpandedRemoteZeroFrontendInventoryUsesSuiteTargets(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRemoteRequired, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), WorkloadInventory{GoPackages: []string{"./internal/alpha"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if parent != GateIDFrontendTest {
			continue
		}
		if !targeted || kind != WorkloadTargetVitest || target != FrontendChangedSuiteCarrierTarget {
			t.Fatalf("remote zero changed inventory workload = %#v", workload)
		}
		return
	}
	t.Fatal("remote expanded catalog omitted FrontendTest suite target")
}

func TestNonLocalCatalogRejectsRawFrontendParents(t *testing.T) {
	for _, profile := range []Profile{ProfileRemoteRequired, ProfilePromotion, ProfileRelease} {
		assertNonLocalCatalogRejectsRawParents(t, profile)
	}
}

func assertNonLocalCatalogRejectsRawParents(t *testing.T, profile Profile) {
	t.Helper()
	plan, err := BuildGatePlan(profile, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatal(err)
	}
	parents := []GateID{GateIDFrontendPreflight}
	if profile == ProfileRelease {
		parents = append(parents, GateIDFrontendFullTest)
	} else {
		parents = append(parents, GateIDFrontendTest)
	}
	for _, wantParent := range parents {
		assertNonLocalCatalogRejectsRawParent(t, profile, plan, catalog, wantParent)
	}
}

func assertNonLocalCatalogRejectsRawParent(t *testing.T, profile Profile, plan GatePlan, catalog WorkloadCatalog, wantParent GateID) {
	t.Helper()
	for index, workload := range catalog.Workloads {
		parent, _, _, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if parent != wantParent || !targeted {
			continue
		}
		forged := catalog
		forged.Workloads = slices.Clone(catalog.Workloads)
		forgedWorkload, err := forgedRawFrontendWorkload(wantParent)
		if err != nil {
			t.Fatal(err)
		}
		forged.Workloads[index] = forgedWorkload
		validationErr := validateWorkloadCatalogForGatePlan(plan, forged)
		assertRawFrontendParentRejected(t, profile, wantParent, targeted, validationErr)
		return
	}
	t.Fatalf("%s catalog omitted targeted workload for %s", profile, wantParent)
}

func forgedRawFrontendWorkload(parent GateID) (Workload, error) {
	workload := Workload{ID: string(parent), Kind: WorkloadKindNodeTest, Shardable: true, BootstrapEstimateMS: 90000}
	if parent == GateIDFrontendPreflight {
		workload.Kind = WorkloadKindGuard
		workload.Shardable = false
		workload.BootstrapEstimateMS = 60000
		workload.CommandDigest = expansionOnlyWorkloadDigest(parent)
		return workload, nil
	}
	digest, err := WorkloadExecutionDigest(string(parent))
	workload.CommandDigest = digest
	return workload, err
}

func assertRawFrontendParentRejected(t *testing.T, profile Profile, parent GateID, targeted bool, validationErr error) {
	t.Helper()
	if validationErr == nil {
		t.Fatalf("%s accepted raw %s workload (original targeted=%t)", profile, parent, targeted)
	}
	if parent == GateIDFrontendPreflight {
		if !strings.Contains(validationErr.Error(), "mixes raw and targeted") {
			t.Fatalf("%s mixed raw preflight rejection = %v", profile, validationErr)
		}
		return
	}
	if !strings.Contains(validationErr.Error(), "cannot execute raw frontend test") {
		t.Fatalf("%s raw %s rejection = %v", profile, parent, validationErr)
	}
}

func TestNonLocalCatalogRejectsPureRawPreflightDescriptor(t *testing.T) {
	plan, err := BuildGatePlan(ProfileRemoteRequired, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := BuildWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatal(err)
	}
	pure, inserted, err := catalogWithoutPreflightChildren(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("remote catalog omitted preflight children")
	}
	if err := validateWorkloadCatalogForGatePlan(plan, pure); err == nil || !strings.Contains(err.Error(), "expansion descriptor") {
		t.Fatalf("pure raw preflight descriptor validation error = %v", err)
	}
}

func catalogWithoutPreflightChildren(catalog WorkloadCatalog) (WorkloadCatalog, bool, error) {
	pure := WorkloadCatalog{Version: catalog.Version, Authoritative: true}
	inserted := false
	for _, workload := range catalog.Workloads {
		parent, _, _, targeted, parseErr := ParseWorkloadID(workload.ID)
		if parseErr != nil {
			return WorkloadCatalog{}, false, parseErr
		}
		if parent != GateIDFrontendPreflight {
			pure.Workloads = append(pure.Workloads, workload)
			continue
		}
		if inserted || !targeted {
			continue
		}
		pure.Workloads = append(pure.Workloads, Workload{
			ID: string(GateIDFrontendPreflight), Kind: WorkloadKindGuard,
			CommandDigest: expansionOnlyWorkloadDigest(GateIDFrontendPreflight), BootstrapEstimateMS: 60000,
		})
		inserted = true
	}
	return pure, inserted, nil
}
