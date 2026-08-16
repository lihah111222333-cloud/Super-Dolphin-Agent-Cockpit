package gate

import (
	"slices"
	"testing"
)

func TestAIMaintenanceRemoteWorkloadsRetiredWithoutLosingBackendCoverage(t *testing.T) {
	t.Run("retired remote identities", assertAIMaintenanceRemoteIdentitiesRetired)
	t.Run("workflow actionlint registry", assertWorkflowActionlintRegistry)
	t.Run("workflow actionlint executor", assertWorkflowActionlintExecutor)
	t.Run("workflow actionlint bootstrap", assertWorkflowActionlintBootstrap)
	t.Run("workflow actionlint check scope", assertWorkflowActionlintCheckScope)
	t.Run("mandatory backend coverage", assertAIMaintenanceMandatoryBackendCoverage)
}

func assertAIMaintenanceRemoteIdentitiesRetired(t *testing.T) {
	t.Helper()
	retiredGateID := GateID("ai-maintenance:self-test")
	for _, spec := range GateRegistry() {
		if spec.ID == retiredGateID {
			t.Errorf("retired remote gate remains registered: %q", spec.ID)
		}
	}
	if _, exists := DefaultWorkloadBootstrapPolicy()[retiredGateID]; exists {
		t.Errorf("retired remote gate remains in bootstrap policy: %q", retiredGateID)
	}
	if _, _, err := ParseExecutorCommand([]string{"run", "--gate", string(retiredGateID)}); err == nil {
		t.Errorf("retired remote gate remains executable: %q", retiredGateID)
	}
	for _, target := range []string{"ai-maintenance-unit", "ai-maintenance-gate"} {
		if _, err := targetWorkloadID(retiredGateID, workloadTargetGoGuard, target); err == nil {
			t.Errorf("retired remote target remains valid: %q", target)
		}
	}
}

func assertWorkflowActionlintRegistry(t *testing.T) {
	t.Helper()
	gateID := GateIDWorkflowActionlint
	spec := findGateSpec(t, gateID)
	all := allProfiles()
	if !slices.Equal(spec.Profiles, all) || !slices.Equal(spec.RequiredProfiles, all) {
		t.Fatalf("workflow actionlint profiles = %v required=%v, want all profiles", spec.Profiles, spec.RequiredProfiles)
	}
	for _, profile := range []Profile{ProfileLocalFast, ProfileRelease} {
		plan, err := BuildGatePlan(profile, registryTestSource())
		if err != nil {
			t.Fatal(err)
		}
		if !gatePlanContains(plan, gateID) {
			t.Errorf("profile %q omitted mandatory workflow actionlint", profile)
		}
	}
}

func gatePlanContains(plan GatePlan, gateID GateID) bool {
	for _, spec := range plan.Gates {
		if spec.ID == gateID {
			return true
		}
	}
	return false
}

func assertWorkflowActionlintExecutor(t *testing.T) {
	t.Helper()
	gateID := GateIDWorkflowActionlint
	gotID, program, err := ParseExecutorCommand([]string{"run", "--gate", string(gateID)})
	if err != nil {
		t.Fatalf("workflow actionlint executor is unavailable: %v", err)
	}
	if gotID != gateID || len(program.Steps) != 1 || !slices.Equal(program.Steps[0].Argv, []string{"actionlint"}) {
		t.Fatalf("workflow actionlint executor = id=%q program=%#v", gotID, program)
	}
	wantPaths := []string{".github/workflows", ".github/actionlint.yaml"}
	if !slices.Equal(program.RequiredPaths, wantPaths) {
		t.Fatalf("workflow actionlint required paths = %v, want %v", program.RequiredPaths, wantPaths)
	}
	wantExecutables := []string{ExecutorActionlintBinaryPath}
	if !slices.Equal(program.RequiredExecutables, wantExecutables) {
		t.Fatalf("workflow actionlint required executables = %v, want %v", program.RequiredExecutables, wantExecutables)
	}
	if program.NeedsGoSeed || program.NeedsFrontendSeed || program.NeedsFrontendEmbedSeed {
		t.Fatalf("workflow actionlint unexpectedly requests unrelated seeds: %#v", program)
	}
}

func assertWorkflowActionlintBootstrap(t *testing.T) {
	t.Helper()
	gateID := GateIDWorkflowActionlint
	got, found := DefaultWorkloadBootstrapPolicy()[gateID]
	want := WorkloadBootstrap{Kind: WorkloadKindGuard, EstimateMS: 60_000, Shardable: true}
	if !found || got != want {
		t.Fatalf("workflow actionlint bootstrap = %#v found=%t, want %#v", got, found, want)
	}
}

func assertWorkflowActionlintCheckScope(t *testing.T) {
	t.Helper()
	check, err := RequiredCheckForWorkloadID(string(GateIDWorkflowActionlint))
	if err != nil {
		t.Fatal(err)
	}
	if check != "gate" {
		t.Fatalf("workflow actionlint required check = %q, want gate", check)
	}
}

func assertAIMaintenanceMandatoryBackendCoverage(t *testing.T) {
	t.Helper()
	plan, err := BuildGatePlan(ProfileLocalFast, registryTestSource())
	if err != nil {
		t.Fatal(err)
	}
	inventory, wantTests := aiMaintenanceBackendInventory()
	catalog, err := BuildExpandedWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy(), inventory)
	if err != nil {
		t.Fatal(err)
	}
	unitPackageFound, gotTests := collectAIMaintenanceBackendCoverage(t, catalog)
	if !unitPackageFound {
		t.Error("backend:test_with_guard no longer covers ./scripts/ai_maintenance")
	}
	if !slices.Equal(gotTests, wantTests) {
		t.Fatalf("backend:test_with_guard AI maintenance selectors = %v, want %v", gotTests, wantTests)
	}
}

func aiMaintenanceBackendInventory() (WorkloadInventory, []string) {
	wantTests := []string{
		"TestAIMaintenanceGateImplementationContracts",
		"TestAIMaintenanceGateRejectsRemotePreCommitAndOutputSpool",
		"TestAIMaintenanceGateRouteDeletionMutations",
		"TestAIMaintenanceGateSelectsMcpLSPResourceCohortE2E",
		"TestAIMaintenanceGateVerifiesLocalHookArtifacts",
	}
	tests := make([]GoTestTarget, 0, len(wantTests))
	for _, name := range wantTests {
		tests = append(tests, GoTestTarget{Package: AtomicScriptsPackageTarget, Name: name})
	}
	return WorkloadInventory{
		GoPackages: []string{"./scripts/ai_maintenance", AtomicScriptsPackageTarget},
		GoTests:    tests,
	}, wantTests
}

func collectAIMaintenanceBackendCoverage(t *testing.T, catalog WorkloadCatalog) (bool, []string) {
	t.Helper()
	unitPackageFound := false
	var gotTests []string
	for _, workload := range catalog.Workloads {
		parent, kind, target, targeted, err := ParseWorkloadID(workload.ID)
		if err != nil {
			t.Fatal(err)
		}
		if parent != GateIDBackendTestWithGuard || !targeted {
			continue
		}
		if kind == WorkloadTargetGoPackage {
			unitPackageFound = assertAIMaintenanceBackendPackage(t, target, unitPackageFound)
		}
		if kind == WorkloadTargetGoTest {
			gotTests = appendAIMaintenanceBackendTest(t, target, gotTests)
		}
	}
	return unitPackageFound, gotTests
}

func assertAIMaintenanceBackendPackage(t *testing.T, target string, found bool) bool {
	t.Helper()
	if target == AtomicScriptsPackageTarget {
		t.Error("./scripts remained a whole-package backend workload")
	}
	return found || target == "./scripts/ai_maintenance"
}

func appendAIMaintenanceBackendTest(t *testing.T, target string, tests []string) []string {
	t.Helper()
	parsed, err := ParseGoTestTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Package != AtomicScriptsPackageTarget {
		return tests
	}
	return append(tests, parsed.Name)
}
