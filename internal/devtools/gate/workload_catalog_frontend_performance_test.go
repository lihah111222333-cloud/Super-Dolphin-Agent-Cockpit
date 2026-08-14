package gate

import (
	"slices"
	"testing"
)

func TestFrontendPerformanceVerifyHasCanonicalRemoteOwner(t *testing.T) {
	const performanceGateID GateID = "ai:frontend:performance-verify"
	assertFrontendPerformanceProfiles(t, findGateSpec(t, performanceGateID))
	plan, err := BuildGatePlan(ProfileRemoteRequired, registryTestSource())
	if err != nil {
		t.Fatalf("BuildGatePlan() error = %v", err)
	}
	assertFrontendPerformanceWorkload(t, plan, performanceGateID)
	assertFrontendPerformanceExecutor(t, performanceGateID)
	assertFrontendPerformanceCheck(t, performanceGateID)
}

func assertFrontendPerformanceProfiles(t *testing.T, spec GateSpec) {
	t.Helper()
	want := []Profile{ProfileRemoteRequired, ProfilePromotion, ProfileRelease}
	if !slices.Equal(spec.Profiles, want) || !slices.Equal(spec.RequiredProfiles, want) {
		t.Fatalf("performance gate profiles = %v/%v, want %v", spec.Profiles, spec.RequiredProfiles, want)
	}
}

func assertFrontendPerformanceWorkload(t *testing.T, plan GatePlan, id GateID) {
	t.Helper()
	catalog, err := BuildWorkloadCatalog(plan, DefaultWorkloadBootstrapPolicy())
	if err != nil {
		t.Fatalf("BuildWorkloadCatalog() error = %v", err)
	}
	for index := range catalog.Workloads {
		workload := catalog.Workloads[index]
		if workload.ID != string(id) {
			continue
		}
		if workload.Kind != WorkloadKindGuard || !workload.Shardable || workload.BootstrapEstimateMS <= 0 {
			t.Fatalf("performance workload = %#v, want shardable guard with positive bootstrap estimate", workload)
		}
		return
	}
	t.Fatalf("remote workload catalog omitted %q", id)
}

func assertFrontendPerformanceExecutor(t *testing.T, id GateID) {
	t.Helper()
	parent, program, err := executorProgramForWorkload(id)
	if err != nil {
		t.Fatalf("executorProgramForWorkload() error = %v", err)
	}
	assertFrontendPerformanceExecutorIdentity(t, parent, id, program)
	step := program.Steps[0]
	assertFrontendPerformanceExecutorStep(t, step)
	assertFrontendPerformanceExecutorSeeds(t, program)
	assertFrontendPerformanceExecutorPaths(t, program)
}

func assertFrontendPerformanceExecutorIdentity(t *testing.T, parent, id GateID, program ExecutorProgram) {
	t.Helper()
	if parent != id {
		t.Fatalf("performance executor parent = %q, want %q", parent, id)
	}
	if len(program.Steps) != 1 {
		t.Fatalf("performance executor steps = %d, want one", len(program.Steps))
	}
}

func assertFrontendPerformanceExecutorStep(t *testing.T, step ExecutorStep) {
	t.Helper()
	if step.Directory != "frontend-app" {
		t.Fatalf("performance executor directory = %q, want frontend-app", step.Directory)
	}
	if !slices.Equal(step.Argv, []string{"npm", "run", "performance:verify"}) {
		t.Fatalf("performance executor argv = %v, want npm run performance:verify", step.Argv)
	}
}

func assertFrontendPerformanceExecutorSeeds(t *testing.T, program ExecutorProgram) {
	t.Helper()
	if !program.NeedsFrontendSeed {
		t.Fatal("performance executor omitted frontend seed")
	}
	if program.NeedsGoSeed || program.NeedsFrontendEmbedSeed {
		t.Fatalf("performance executor seed contract = go:%t frontend:%t embed:%t", program.NeedsGoSeed, program.NeedsFrontendSeed, program.NeedsFrontendEmbedSeed)
	}
}

func assertFrontendPerformanceExecutorPaths(t *testing.T, program ExecutorProgram) {
	t.Helper()
	for _, path := range []string{".git", "frontend-app/package.json", "frontend-app/scripts/performance-budget-runner.mjs"} {
		if !slices.Contains(program.RequiredPaths, path) {
			t.Fatalf("performance executor required paths = %v, missing %q", program.RequiredPaths, path)
		}
	}
}

func assertFrontendPerformanceCheck(t *testing.T, id GateID) {
	t.Helper()
	check, err := RequiredCheckForWorkloadID(string(id))
	if err != nil {
		t.Fatalf("RequiredCheckForWorkloadID() error = %v", err)
	}
	if check != "frontend" {
		t.Fatalf("performance required check = %q, want frontend", check)
	}
}
