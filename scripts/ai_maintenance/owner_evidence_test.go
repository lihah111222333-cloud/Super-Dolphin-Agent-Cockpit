package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/capcontract"
)

func TestExcludeDeferredE2EGoPackagesKeepsFastPackages(t *testing.T) {
	packages, err := excludeDeferredE2EGoPackages([]string{
		"./internal/app",
		"./internal/provider/claudecli",
		"./internal/provider/codexapp",
		"./scripts/ai_maintenance",
	}, "deferred_e2e_packages.txt")
	if err != nil {
		t.Fatalf("exclude deferred E2E packages: %v", err)
	}

	assertStringSetContains(t, packages, "./internal/app", "./scripts/ai_maintenance")
	for _, packageName := range packages {
		if packageName == "./internal/provider/claudecli" || packageName == "./internal/provider/codexapp" {
			t.Fatalf("deferred E2E package remained in pre-push scope: %q", packageName)
		}
	}
}

func TestBuildGatePlanRoutesReleaseWorkflowAndArchtestInputsToOwners(t *testing.T) {
	tests := []struct {
		file  string
		gates []string
	}{
		{".github/workflows/release.yml", []string{"workflow:actionlint", "release:semantic-guards", "backend:archtest"}},
		{"scripts/package_linux.sh", []string{"release:semantic-guards"}},
		{"scripts/package_windows.ps1", []string{"release:semantic-guards"}},
		{"scripts/prepare_lsp_bundle_linux.sh", []string{"release:semantic-guards"}},
		{"scripts/prepare_lsp_bundle_macos.sh", []string{"release:semantic-guards"}},
		{"scripts/prepare_lsp_bundle_windows.ps1", []string{"release:semantic-guards"}},
		{"docs/scripts/macos_release_smoke.sh", []string{"release:semantic-guards"}},
		{"migrations/001.sql", []string{"backend:archtest"}},
		{"internal/platform/db/sqlite/migrations/001.sql", []string{"backend:archtest"}},
		{"docs/契约/modularity-convention.md", []string{"backend:archtest"}},
		{"docs/guards/code-size-freeze-v3-fail-first.txt", []string{"backend:archtest"}},
		{"go.mod", []string{"backend:test_with_guard"}},
		{"internal/archtest/freeze_baseline.json", []string{"backend:archtest"}},
		{"internal/provider/_template/module.go.txt", []string{"backend:archtest"}},
		{"scripts/ai_maintenance_gates.sh", []string{"backend:archtest"}},
		{"scripts/ci_cross_platform_smoke.ps1", []string{"backend:archtest"}},
		{"scripts/codemap_policy.txt", []string{"backend:archtest", "codemap:check", "project-map:check"}},
		{"cmd/mcp-orch/sqlc.yaml", []string{"backend:archtest"}},
		{"cmd/mcp-orch/sql/queries/task_dag_node_runtime.sql", []string{"backend:archtest"}},
		{"sql/queries/agent_thread.sql", []string{"backend:archtest"}},
		{"frontend-app/src/App.jsx", []string{"backend:archtest"}},
		{"frontend-app/src/app/appShellModel.js", []string{"backend:archtest"}},
		{"frontend-app/src/features/slash-commands/adapters/skillInfoFieldRegistry.json", []string{"backend:archtest"}},
		{"internal/platform/shared/builtinprompts/assets/sections/main-dag-designer-en/00-runtime-tools.md", []string{"backend:archtest"}},
		{"internal/platform/shared/builtinprompts/assets/templates/main-dag-designer-en.json", []string{"backend:archtest"}},
		{"internal/guards/refactor_baseline.json", []string{"backend:archtest"}},
		{"internal/guards/guard_manifest.json", []string{"backend:archtest"}},
		{".githooks/pre-commit", []string{"backend:archtest", "ai-maintenance:self-test"}},
		{"internal/module/thread/session_test.go", []string{"backend:test-integrity"}},
		{"docs/automation/全仓夜间门禁健康巡检协议.md", []string{"nightly-protocol:check"}},
	}
	for _, test := range tests {
		t.Run(test.file, func(t *testing.T) {
			plan := mustBuildGatePlan(t, []string{test.file})
			assertStringSetContains(t, plan.RequiredGates, test.gates...)
		})
	}
}

func TestAffectedGoPackagesUsesCurrentOwnerForTestdataAndSkipsDeletedPackage(t *testing.T) {
	repoRoot := t.TempDir()
	ownerDir := filepath.Join(repoRoot, "internal", "devtools", "typednil")
	if err := os.MkdirAll(ownerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ownerDir, "analyzer.go"), []byte("package typednil\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	packages, err := affectedGoPackages(repoRoot, []string{
		"internal/devtools/typednil/testdata/src/fixture/fixture.go",
		"internal/deleted/package_test.go",
	}, newGatePlanPolicy())
	if err != nil {
		t.Fatal(err)
	}
	assertStringSetContains(t, packages, "./internal/devtools/typednil")
	assertStringSetOmits(t, packages,
		"./internal/devtools/typednil/testdata/src/fixture",
		"./internal/deleted",
	)
}

func TestAffectedGoPackagesRetainsCoreMetadataForDeletionOnly(t *testing.T) {
	packages, err := affectedGoPackages(t.TempDir(), []string{"internal/deleted/package.go"}, newGatePlanPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) == 0 {
		t.Fatal("deletion-only backend change produced an empty package plan")
	}
	assertStringSetOmits(t, packages, "./internal/deleted")
	assertStringSetContains(t, packages, "./internal/app", "./scripts/ai_maintenance")
}

func TestBackendTestWithGuardArgsUsesCIL1WhenAnyRenameOwnerWasDeleted(t *testing.T) {
	repoRoot := t.TempDir()
	writeGateFixtureFile(t, repoRoot, "go.mod", "module example.com/fixture\n\ngo 1.25\n")
	writeGateFixtureFile(t, repoRoot, "internal/newowner/new.go", "package newowner\n")
	writeGateFixtureFile(t, repoRoot, "internal/consumer/consumer.go",
		"package consumer\nimport _ \"example.com/fixture/internal/deletedowner\"\n",
	)
	plan := gatePlan{
		ChangedFiles:       []string{"internal/deletedowner/old.go", "internal/newowner/new.go"},
		AffectedGoPackages: []string{"./internal/newowner"},
	}

	args, err := backendTestWithGuardArgsForRepo(plan, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args, []string{"make", "ci-l1"}) {
		t.Fatalf("rename with deleted owner args = %v, want fail-closed full workspace lane", args)
	}
}

func writeGateFixtureFile(t *testing.T, root, relative, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func aiMaintenanceRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := capcontract.FindRepoRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestGeneratedCodemapClassificationMatchesRefreshOwnerOutputs(t *testing.T) {
	repoRoot := aiMaintenanceRepoRoot(t)
	command := exec.Command("bash", filepath.Join(repoRoot, "scripts", "refresh_generated_artifacts.sh"), "all", "--list-outputs")
	command.Dir = repoRoot
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list generated outputs: %v", err)
	}
	for _, line := range lines(string(output)) {
		kind, path, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("malformed generated output owner row %q", line)
		}
		switch kind {
		case "file":
			if !generatedCodemapFile(path) {
				t.Errorf("generated file %q is missing planner classification", path)
			}
			plan := mustBuildGatePlan(t, []string{path})
			assertStringSetContains(t, plan.RequiredGates, "codemap:check")
		case "tree":
			probe := strings.TrimSuffix(path, "/") + "/OWNED"
			if !generatedCodemapFile(probe) {
				t.Errorf("generated tree %q is missing planner classification", path)
			}
		default:
			t.Fatalf("unknown generated output kind %q", kind)
		}
	}
}

func TestValidateEvidenceBlocksMissingAgentIDDiagnosticsAndCommands(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{"frontend-app/src/App.jsx"})
	path := writeEvidence(t, `
STATUS: DONE_WITH_EVIDENCE
OWNED_FILES_CHANGED:
  - frontend-app/src/App.jsx
LSP_EVIDENCE:
  locate: PASS
  inspect: PASS
  xref: PASS
  read_file: PASS
  diagnostics:
COMMANDS_RUN:
`)

	err := validateEvidenceFile(path, plan)
	if err == nil {
		t.Fatal("missing evidence was accepted")
	}
	out := err.Error()
	for _, want := range []string{
		"missing AGENTID",
		"missing or non-pass LSP evidence diagnostics",
		"DONE_WITH_EVIDENCE requires COMMANDS_RUN",
		"missing command evidence for frontend:lint",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("error missing %q\n%s", want, out)
		}
	}
}

func TestValidateEvidenceAcceptsBlockedReportWithoutGreenEvidence(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{"internal/app/modules.go"})
	path := writeEvidence(t, `
STATUS: BLOCKED
AGENTID: 019f0000-0000-7000-8000-000000000000
BLOCKERS:
  - LSP diagnostics unavailable after narrowed retry
`)

	if err := validateEvidenceFile(path, plan); err != nil {
		t.Fatalf("blocked evidence rejected: %v", err)
	}
}

func TestValidateEvidenceBlocksDoneWithBlockersAndLooseAgentID(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{"internal/app/modules.go"})
	path := writeEvidence(t, `
STATUS: DONE_WITH_EVIDENCE
AGENTID: worker-1
OWNED_FILES_CHANGED:
  - internal/app/modules.go
LSP_EVIDENCE:
  locate: PASS
  inspect: PASS
  xref: PASS
  read_file: PASS
  diagnostics: PASS
COMMANDS_RUN:
  - cmd: ./scripts/test_with_guard.sh ./internal/app -count=1
    exit: 0
  - cmd: make guard
    exit: 0
BLOCKERS:
  - still blocked
`)

	err := validateEvidenceFile(path, plan)
	if err == nil {
		t.Fatal("bad DONE_WITH_EVIDENCE accepted")
	}
	assertErrorContainsAll(t, err,
		"AGENTID must be exact platform UUID",
		"DONE_WITH_EVIDENCE must not include BLOCKERS",
	)
}

func TestValidateEvidenceAcceptsCompleteFrontendAndGeneratedEvidence(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{
		"frontend-app/src/App.jsx",
		"docs/doc/codemap/ai-index.json",
	})
	path := writeEvidence(t, `
PACKAGE: B1-frontend-surface
STATUS: DONE_WITH_EVIDENCE
AGENTID: 019f0000-0000-7000-8000-000000000000
BASE_HEAD: abc123
OWNED_FILES_CHANGED:
  - frontend-app/src/App.jsx
  - docs/doc/codemap/ai-index.json
UNRELATED_DIRTY_FILES_PRESERVED: []
LSP_EVIDENCE:
  locate: PASS
  inspect: PASS
  xref: PASS
  read_file: PASS
  diagnostics: PASS
COMMANDS_RUN:
  - cmd: go run ./scripts/lsp_diagnostics_gate --file frontend-app/src/App.jsx
    exit: 0
  - cmd: ./scripts/test_with_guard.sh --archtest-only
    exit: 0
  - cmd: cd frontend-app && npm run guard:critical-skip
    exit: 0
  - cmd: cd frontend-app && npm run lint
    exit: 0
  - cmd: cd frontend-app && npm run typecheck:contracts
    exit: 0
  - cmd: cd frontend-app && npx vitest run src/App.test.jsx
    exit: 0
  - cmd: cd frontend-app && npm run build
    exit: 0
  - cmd: cd frontend-app && npm run verify:embed:isolated
    exit: 0
  - cmd: make codemap-check
    exit: 0
  - cmd: make project-map-check
    exit: 0
GENERATED_FILES:
  - path: docs/doc/codemap/ai-index.json
    precheck_failed: make codemap-check
    source_command: make codemap-refresh
BLOCKERS: []
`)

	if err := validateEvidenceFile(path, plan); err != nil {
		t.Fatalf("complete evidence rejected: %v", err)
	}
}

func assertErrorContainsAll(t *testing.T, err error, wants ...string) {
	t.Helper()
	out := err.Error()
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("error missing %q\n%s", want, out)
		}
	}
}

func TestValidateEvidenceBlocksDiffScopeMismatch(t *testing.T) {
	plan := mustBuildGatePlan(t, []string{"internal/app/modules.go"})
	path := writeEvidence(t, `
STATUS: DONE_WITH_EVIDENCE
AGENTID: 019f0000-0000-7000-8000-000000000000
OWNED_FILES_CHANGED:
  - internal/app/other.go
LSP_EVIDENCE:
  locate: PASS
  inspect: PASS
  xref: PASS
  read_file: PASS
  diagnostics: PASS
COMMANDS_RUN:
  - cmd: ./scripts/test_with_guard.sh ./internal/app -count=1
    exit: 0
  - cmd: make guard
    exit: 0
`)

	err := validateEvidenceFile(path, plan)
	if err == nil || !strings.Contains(err.Error(), "OWNED_FILES_CHANGED does not match changed files") {
		t.Fatalf("diff scope mismatch not blocked: %v", err)
	}
}

func writeEvidence(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "evidence.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write evidence: %v", err)
	}
	return path
}

func assertStringSetContains(t *testing.T, values []string, wants ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, value := range values {
		set[value] = true
	}
	for _, want := range wants {
		if !set[want] {
			t.Fatalf("missing %q in %#v", want, values)
		}
	}
}

func assertStringSetOmits(t *testing.T, values []string, unwanted ...string) {
	t.Helper()
	for _, value := range values {
		for _, blocked := range unwanted {
			if value == blocked {
				t.Fatalf("unexpected value %q in %v", blocked, values)
			}
		}
	}
}

func assertRegistryMatchesProducer[T any](t *testing.T, producer map[string]bool, registry map[string]T, name string, exempt ...string) {
	t.Helper()
	exemptions := map[string]bool{}
	for _, gate := range exempt {
		exemptions[gate] = true
	}
	for gate := range producer {
		if exemptions[gate] {
			continue
		}
		if _, ok := registry[gate]; !ok {
			t.Errorf("%s registry missing produced gate %q", name, gate)
		}
	}
	for gate := range registry {
		if !producer[gate] {
			t.Errorf("%s registry contains stale gate %q", name, gate)
		}
	}
}

func mustBuildGatePlan(t *testing.T, files []string) gatePlan {
	t.Helper()
	plan, err := buildGatePlan(files)
	if err != nil {
		t.Fatalf("build gate plan: %v", err)
	}
	return plan
}
