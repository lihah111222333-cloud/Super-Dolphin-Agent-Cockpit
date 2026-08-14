package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

func TestRetiredLocalGateFlagsAreRejected(t *testing.T) {
	planOutput, err := os.CreateTemp(t.TempDir(), "plan-*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer planOutput.Close()
	if err := runPlan([]string{"--push-gates", "--changed-file", "README.md"}, planOutput); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("retired plan --push-gates error = %v, want undefined flag", err)
	}

	tests := []struct {
		name string
		args []string
	}{
		{name: "push gates", args: []string{"--push-gates"}},
		{name: "cache directory", args: []string{"--cache-dir", t.TempDir()}},
		{name: "cache max age", args: []string{"--cache-max-age", "1m"}},
		{name: "cache scope", args: []string{"--cache-scope", strings.Repeat("a", 40)}},
		{name: "staged diff", args: []string{"--diff-cached"}},
		{name: "push diff range", args: []string{"--diff-range", "HEAD"}},
		{name: "prevalidated gate", args: []string{"--prevalidated-gate", "codemap:check"}},
		{name: "deferred e2e override", args: []string{"--skip-deferred-e2e"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"--print-plan", "--changed-file", "README.md"}, test.args...)
			err := runGates(args)
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
				t.Fatalf("retired run flag error = %v, want undefined flag", err)
			}
		})
	}
}

func TestGenericBackendChangeDoesNotRunCapabilityContractGate(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{"internal/store/thread/store.go"})
	assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
	assertStringSetOmits(t, plan.RequiredGates, "capcontract:check")
}

func TestNonGoBackendOwnerInputsDoNotCreateAnEmptyGoPackageLane(t *testing.T) {
	tests := []struct {
		name  string
		file  string
		gates []string
	}{
		{name: "freeze baseline", file: "internal/archtest/freeze_baseline.json", gates: []string{"backend:archtest"}},
		{name: "sqlite migration", file: "internal/platform/db/sqlite/migrations/001.sql", gates: []string{"backend:archtest", "sqlc:verify"}},
		{name: "mcp orch sqlc config", file: "cmd/mcp-orch/sqlc.yaml", gates: []string{"backend:archtest"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{test.file})
			assertStringSetContains(t, plan.RequiredGates, test.gates...)
			assertStringSetOmits(t, plan.RequiredGates, "backend:test_with_guard")
			if len(plan.AffectedGoPackages) != 0 {
				t.Fatalf("non-Go owner %s produced Go packages %v", test.file, plan.AffectedGoPackages)
			}
		})
	}
}

func TestBroadBackendOwnerCoalescesFullArchtestLane(t *testing.T) {
	for _, file := range []string{"scripts/test_with_guard.sh", "go.mod"} {
		t.Run(file, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{file})
			assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
			assertStringSetOmits(t, plan.RequiredGates, "backend:archtest")
		})
	}
}

func TestWorkflowOnlyKeepsArchtestAndWorkflowGoCoalescesFullOwner(t *testing.T) {
	workflow := mustBuildGatePlan(t, []string{".github/workflows/ci.yml"})
	assertStringSetContains(t, workflow.RequiredGates, "backend:archtest")
	assertStringSetOmits(t, workflow.RequiredGates, "backend:test_with_guard")

	combined := mustBuildGatePlan(t, []string{".github/workflows/ci.yml", "internal/app/modules.go"})
	assertStringSetContains(t, combined.RequiredGates, "backend:test_with_guard")
	assertStringSetOmits(t, combined.RequiredGates, "backend:archtest")
	assertStringSetContains(t, combined.AffectedGoPackages, "./internal/app")
	args, err := backendTestWithGuardArgsForRepo(combined, aiMaintenanceRepoRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"make", "ci-l1"}) {
		t.Fatalf("workflow plus Go backend args = %v, want make ci-l1 full owner", args)
	}
}

func TestArchtestOnlyChangeRunsOneFullGuardWithoutCorePackageFallback(t *testing.T) {
	repoRoot, err := capcontract.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	files := []string{"internal/archtest/ratchet_test.go"}
	packages, err := affectedGoPackages(repoRoot, files, newGatePlanPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 0 {
		t.Fatalf("archtest-only affected packages = %v, want no duplicate package lane", packages)
	}

	plan := mustBuildGatePlan(t, files)
	args, err := backendTestWithGuardArgsForRepo(plan, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"./scripts/test_with_guard.sh", "--guard-only"}
	if !slices.Equal(args, want) {
		t.Fatalf("archtest-only backend args = %v, want %v", args, want)
	}
}

func TestInternalGuardTestsDoNotRepeatIntegrityPackage(t *testing.T) {
	for _, file := range []string{
		"internal/guards/ignored_test_guard_test.go",
		"internal/guards/rollback_skip_guard_test.go",
	} {
		t.Run(file, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{file})
			assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
			assertStringSetOmits(t, plan.RequiredGates, "backend:test-integrity")
		})
	}

	ordinary := mustBuildGatePlan(t, []string{"internal/module/thread/session_test.go"})
	assertStringSetContains(t, ordinary.RequiredGates, "backend:test-integrity")
}

func TestMcpLSPQuickRoundTripValidatesOnlyItsReceiptAfterCatalogGate(t *testing.T) {
	receipt := filepath.Join(t.TempDir(), "receipt.json")
	want := []string{
		"./scripts/check_mcp_lsp_workload_catalog.sh",
		"--receipt-only",
		"--receipt", receipt,
		"--id", "mcp-lsp-idle-quick",
	}
	if got := mcpLSPReceiptGuardArgs(receipt); !slices.Equal(got, want) {
		t.Fatalf("mcp-lsp receipt guard args = %v, want %v", got, want)
	}
}

func TestMcpLSPQuickEvidenceRequiresRunnerAndActualReceiptGuard(t *testing.T) {
	success := 0
	plan := gatePlan{RequiredGates: []string{"mcp-lsp:idle-quick"}}
	fake := evidenceDoc{CommandsRun: []evidenceCommand{{
		Cmd:  "./scripts/run_mcp_lsp_workload.sh --receipt-only mcp-lsp-idle-quick",
		Exit: &success,
	}}}
	if problems := gateCommandProblems(fake, plan); len(problems) == 0 {
		t.Fatal("impossible combined quick command was accepted as evidence")
	}

	real := evidenceDoc{CommandsRun: []evidenceCommand{
		{Cmd: "./scripts/run_mcp_lsp_workload.sh --id mcp-lsp-idle-quick --receipt /tmp/receipt.json", Exit: &success},
		{Cmd: "./scripts/check_mcp_lsp_workload_catalog.sh --receipt-only --receipt /tmp/receipt.json --id mcp-lsp-idle-quick", Exit: &success},
	}}
	if problems := gateCommandProblems(real, plan); len(problems) != 0 {
		t.Fatalf("actual quick runner plus receipt guard evidence rejected: %v", problems)
	}
}

func TestCriticalTypecheckRouteUsesRegistryAndExecutionSeeds(t *testing.T) {
	for _, file := range []string{
		"frontend-app/src/shared/ui/runUIAction.js",
		"frontend-app/jsconfig.json",
		"frontend-app/tsconfig.contracts.json",
		"frontend-app/package.json",
		"frontend-app/package-lock.json",
		"frontend-app/scripts/critical-typecheck-files.json",
		"frontend-app/scripts/critical-typecheck-guard.mjs",
		"frontend-app/scripts/contracts-typecheck-guard.test.mjs",
	} {
		t.Run(file, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{file})
			assertStringSetContains(t, plan.RequiredGates, "frontend:typecheck-contracts")
		})
	}

	plan := mustBuildGatePlan(t, []string{"frontend-app/src/App.jsx"})
	assertStringSetOmits(t, plan.RequiredGates, "frontend:typecheck-contracts")
}

func TestCriticalTypecheckRegistryRouteFailsClosedOnInvalidPaths(t *testing.T) {
	repoRoot := t.TempDir()
	registry := criticalTypecheckRegistryFixture()
	registry["productionFiles"] = []string{"../escape.js"}
	writeCriticalTypecheckRegistryFixture(t, repoRoot, registry)
	if _, err := loadCriticalTypecheckProductionFiles(repoRoot); err == nil || !strings.Contains(err.Error(), "production path is invalid") {
		t.Fatalf("invalid critical typecheck registry error = %v", err)
	}
}

func TestCriticalTypecheckRegistryRouteValidatesCompleteSchema(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "missing surface",
			mutate: func(registry map[string]any) {
				delete(registry["surfaces"].(map[string]any), "storeBridge")
			},
			want: "surfaces exact diff failed",
		},
		{
			name: "missing entrypoints",
			mutate: func(registry map[string]any) {
				delete(registry, "entrypoints")
			},
			want: "registry keys exact diff failed",
		},
		{
			name: "empty test files",
			mutate: func(registry map[string]any) {
				registry["testFiles"] = []string{}
			},
			want: "testFiles must be a non-empty array",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repoRoot := t.TempDir()
			registry := criticalTypecheckRegistryFixture()
			test.mutate(registry)
			writeCriticalTypecheckRegistryFixture(t, repoRoot, registry)
			if _, err := loadCriticalTypecheckProductionFiles(repoRoot); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("critical typecheck registry error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestBuildGatePlanForRepoBindsCriticalTypecheckRegistryToRepoRoot(t *testing.T) {
	repoRoot := t.TempDir()
	writeCriticalTypecheckRepoFixture(t, repoRoot)

	plan, err := buildGatePlanForRepo(repoRoot, []string{"frontend-app/src/critical.js"}, newGatePlanPolicy())
	if err != nil {
		t.Fatalf("buildGatePlanForRepo() error = %v", err)
	}
	assertStringSetContains(t, plan.RequiredGates, "frontend:typecheck-contracts")
}

func criticalTypecheckRegistryFixture() map[string]any {
	const source = "src/critical.js"
	return map[string]any{
		"schemaVersion": 1,
		"surfaces": map[string]any{
			"actionFeedback":      []string{source},
			"diagnostics":         []string{source},
			"promptHistory":       []string{source},
			"providerPreference":  []string{source},
			"rpcAdapter":          []string{source},
			"storeBridge":         []string{source},
			"terminalPublicError": []string{source},
			"uiAction":            []string{source},
		},
		"entrypoints":     []string{source},
		"productionFiles": []string{source},
		"testFiles":       []string{"scripts/contracts-typecheck-guard.test.mjs"},
	}
}

func writeCriticalTypecheckRegistryFixture(t *testing.T, repoRoot string, registry map[string]any) {
	t.Helper()
	writeGateFixtureFile(t, repoRoot, "frontend-app/src/critical.js", "export const critical = true;\n")
	writeGateFixtureFile(t, repoRoot, "frontend-app/scripts/contracts-typecheck-guard.test.mjs", "export {};\n")
	raw, err := json.Marshal(registry)
	if err != nil {
		t.Fatalf("marshal critical typecheck registry fixture: %v", err)
	}
	writeGateFixtureFile(t, repoRoot, criticalTypecheckRegistryPath, string(raw)+"\n")
}

func writeCriticalTypecheckRepoFixture(t *testing.T, repoRoot string) {
	t.Helper()
	for _, dir := range []string{
		"frontend-app",
		"docs/doc/codemap/capability-contract",
		"internal/devtools/capcontract",
		"scripts/capcontract",
	} {
		if err := os.MkdirAll(filepath.Join(repoRoot, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("mkdir critical typecheck fixture %q: %v", dir, err)
		}
	}
	writeGateFixtureFile(t, repoRoot, "scripts/capcontract/main.go", `package main

func defaultCapabilityRoots() []string { return []string{"frontend-app"} }
`)
	root, err := capcontract.FindRepoRoot(".")
	if err != nil {
		t.Fatalf("resolve source repository root: %v", err)
	}
	turnRegistry, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(turnContractRegistryPath)))
	if err != nil {
		t.Fatalf("read turn contract registry fixture: %v", err)
	}
	writeGateFixtureFile(t, repoRoot, turnContractRegistryPath, string(turnRegistry))
	writeCriticalTypecheckRegistryFixture(t, repoRoot, criticalTypecheckRegistryFixture())
}
