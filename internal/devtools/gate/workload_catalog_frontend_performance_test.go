package gate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
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
	assertFrontendPerformanceCorrectnessScope(t)
}

func assertFrontendPerformanceCorrectnessScope(t *testing.T) {
	t.Helper()
	assertFrontendPerformanceCorrectnessInputs(t)
	root := frontendPerformanceRepositoryRoot(t)
	assertFrontendPerformanceActiveOwners(t, root)
	assertFrontendPerformanceActiveSchema(t, root)
	assertFrontendPerformanceRetirementExceptions(t, root)
}

func assertFrontendPerformanceCorrectnessInputs(t *testing.T) {
	t.Helper()
	inputs := FrontendPerformanceInputPaths()
	for _, required := range []string{
		"frontend-app/scripts/performance-budget-runner.mjs",
		"frontend-app/scripts/render-isolation-probe.test.jsx",
		"frontend-app/scripts/resource-budget.mjs",
	} {
		if !slices.Contains(inputs, required) {
			t.Errorf("frontend performance correctness inputs omitted %q: %v", required, inputs)
		}
	}
	for _, retired := range []string{
		"frontend-app/scripts/chat-history-benchmark.mjs",
		"frontend-app/scripts/stop-feedback-benchmark.mjs",
		"frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.js",
		"frontend-app/src/entities/client/model/threadLifecycleRuntime.js",
		"frontend-app/src/pages/chat/components/ChatActionFeedback.js",
		"frontend-app/src/pages/chat/model/timelineMaterializationModel.js",
	} {
		if slices.Contains(inputs, retired) {
			t.Errorf("retired wall-clock input %q remains in correctness fingerprint: %v", retired, inputs)
		}
	}
}

func assertFrontendPerformanceActiveOwners(t *testing.T, root string) {
	t.Helper()
	for _, relative := range []string{
		"frontend-app/scripts/evidence-provenance.mjs",
		"frontend-app/scripts/performance-baseline-provenance.mjs",
		"frontend-app/scripts/performance-budget-runner.mjs",
		"internal/devtools/gate/workload_catalog_frontend.go",
	} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read active frontend performance owner %s: %v", relative, err)
		}
		assertFrontendPerformanceOwnerOmitsTiming(t, relative, string(body))
	}
}

func assertFrontendPerformanceOwnerOmitsTiming(t *testing.T, relative, body string) {
	t.Helper()
	for _, retired := range []string{
		"chat-history-benchmark",
		"stop-feedback-benchmark",
		"collectDetachedP01P02Evidence",
		"collectDetachedStopFeedbackBudget",
		"validateP03SubjectRuntime",
	} {
		if strings.Contains(body, retired) {
			t.Errorf("active frontend performance owner %s still binds retired wall-clock surface %q", relative, retired)
		}
	}
}

func assertFrontendPerformanceActiveSchema(t *testing.T, root string) {
	t.Helper()
	retiredMetrics := []string{"P" + "02-history-budget", "P" + "03-feedback-budget"}
	for _, relative := range []string{
		"frontend-app/scripts/frontend-performance-cases.json",
		"frontend-app/scripts/performance-budget-runner.mjs",
	} {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatalf("read active frontend performance schema %s: %v", relative, err)
		}
		assertFrontendPerformanceSchemaMetrics(t, relative, string(body), retiredMetrics)
	}
}

func assertFrontendPerformanceSchemaMetrics(t *testing.T, relative, body string, retiredMetrics []string) {
	t.Helper()
	for _, retired := range retiredMetrics {
		if strings.Contains(body, retired) {
			t.Errorf("active frontend performance schema %s still contains retired metric %q", relative, retired)
		}
	}
	for _, required := range []string{"P01-render-isolation", "P04-resource-budget"} {
		if !strings.Contains(body, required) {
			t.Errorf("active frontend performance schema %s omitted %q", relative, required)
		}
	}
}

func assertFrontendPerformanceRetirementExceptions(t *testing.T, root string) {
	t.Helper()
	retiredMetrics := []string{"P" + "02-history-budget", "P" + "03-feedback-budget"}
	legacyBaselineRaw, err := os.ReadFile(filepath.Join(root, "frontend-app", "scripts", "frontend-maintainability-baseline.json"))
	if err != nil {
		t.Fatalf("read legacy frontend performance baseline: %v", err)
	}
	var legacyBaseline struct {
		Metrics map[string]json.RawMessage `json:"metrics"`
	}
	if err := json.Unmarshal(legacyBaselineRaw, &legacyBaseline); err != nil {
		t.Fatalf("parse legacy frontend performance baseline: %v", err)
	}
	allowedLegacyMetrics := map[string]bool{
		"P01-render-isolation": true,
		"P04-resource-budget":  true,
		retiredMetrics[0]:      true,
		retiredMetrics[1]:      true,
	}
	for metric := range legacyBaseline.Metrics {
		if !allowedLegacyMetrics[metric] {
			t.Errorf("legacy frontend performance baseline has an unknown metric %q", metric)
		}
	}
	for _, relative := range []string{
		"frontend-app/scripts/chat-history-benchmark.mjs",
		"frontend-app/scripts/chat-history-benchmark.test.mjs",
		"frontend-app/scripts/stop-feedback-benchmark.mjs",
		"frontend-app/scripts/stop-feedback-benchmark.test.mjs",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Errorf("non-blocking timing diagnostic %s must remain available: %v", relative, err)
		}
	}
}

func frontendPerformanceRepositoryRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve frontend performance test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
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
