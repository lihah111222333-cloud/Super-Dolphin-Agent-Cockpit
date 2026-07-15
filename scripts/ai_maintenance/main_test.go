package main

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuildGatePlanRoutesFrontendBackendAndGeneratedFiles(t *testing.T) {
	plan := buildGatePlan([]string{
		"frontend-app/src/App.jsx",
		"internal/app/modules.go",
		"docs/doc/codemap/project-map/AI_PROJECT_MAP.md",
	})

	assertStringSetContains(t, plan.RequiredGates,
		"backend:test_with_guard",
		"codemap:check",
		"diff:whitespace",
		"frontend:build",
		"frontend:embed-verify",
		"frontend:lint",
		"frontend:test",
		"lsp:changed-diagnostics",
		"project-map:check",
	)
	assertStringSetContains(t, plan.DiagnosticFiles, "frontend-app/src/App.jsx", "internal/app/modules.go")
	assertStringSetContains(t, plan.RequiredEvidence,
		"generated:source",
		"lsp:diagnostics",
		"lsp:inspect",
		"lsp:locate",
		"lsp:read_file",
		"lsp:xref",
	)
	assertStringSetContains(t, plan.GeneratedFiles, "docs/doc/codemap/project-map/AI_PROJECT_MAP.md")
	if !plan.RequiresEvidenceDoc {
		t.Fatal("source and generated changes must require evidence doc")
	}
}

func TestBuildGatePlanRoutesAIMaintenanceHooksToSelfTest(t *testing.T) {
	plan := buildGatePlan([]string{".githooks/pre-commit", ".githooks/pre-push"})

	assertStringSetContains(t, plan.RequiredGates, "ai-maintenance:self-test", "diff:whitespace")
}

func TestBuildGatePlanRoutesProjectMapOverridesToCodemapChecks(t *testing.T) {
	plan := buildGatePlan([]string{".ai-project-map.overrides.json"})

	assertStringSetContains(t, plan.RequiredGates, "codemap:check", "project-map:check", "diff:whitespace")
}

func TestExecuteGatePlanKeepsGeneratedDriftFailFast(t *testing.T) {
	binDir := t.TempDir()
	makePath := filepath.Join(binDir, "make")
	if err := os.WriteFile(makePath, []byte("#!/usr/bin/env bash\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("write fake make: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	plan := gatePlan{RequiredGates: []string{"codemap:check", "project-map:check"}}

	if err := executeGatePlan(plan); err == nil {
		t.Fatal("generated drift gate unexpectedly succeeded")
	}

	t.Setenv("SUPER_DOLPHIN_PRE_PUSH_SOFT_GENERATED_DRIFT", "1")
	if err := executeGatePlan(plan); err == nil {
		t.Fatal("legacy soft-generated environment variable bypassed a failing gate")
	}
}

func TestExecuteGatePlanRejectsRequiredGateWithoutRunner(t *testing.T) {
	plan := gatePlan{RequiredGates: []string{"missing:runner"}}

	err := executeGatePlan(plan)
	if err == nil {
		t.Fatal("executeGatePlan should reject a required gate without a runner")
	}
	if !strings.Contains(err.Error(), "missing:runner") {
		t.Fatalf("error should name the missing runner, got %v", err)
	}
}

func TestLSPDiagnosticsRunnerAuditsAllDeleted(t *testing.T) {
	root := t.TempDir()
	plan := gatePlan{DiagnosticFiles: []string{
		filepath.Join(root, "deleted-a.go"),
		filepath.Join(root, "deleted-b.go"),
	}}
	runner := gateRunners(plan, gateExecutionScope{})["lsp:changed-diagnostics"]
	stderr := captureStderr(t, runner.run)
	want := "[ai-maintenance] lsp diagnostics skip: planned=2 existing=0 reason=all-deleted\n"
	if stderr != want {
		t.Fatalf("stderr = %q, want %q", stderr, want)
	}
}

func TestExistingDiagnosticFilesRejectsNonRegularTarget(t *testing.T) {
	_, _, err := existingDiagnosticFiles([]string{t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("non-regular target error = %v", err)
	}
}

func captureStderr(t *testing.T, run func() error) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = original }()
	runErr := run()
	if closeErr := writer.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	data, readErr := io.ReadAll(reader)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if runErr != nil {
		t.Fatalf("run error = %v", runErr)
	}
	return string(data)
}

func TestBuildGatePlanRequiresFullLSPEvidenceForGoScripts(t *testing.T) {
	plan := buildGatePlan([]string{"scripts/ai_maintenance/main.go"})

	assertStringSetContains(t, plan.RequiredEvidence,
		"lsp:diagnostics",
		"lsp:inspect",
		"lsp:locate",
		"lsp:read_file",
		"lsp:xref",
	)
	assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
	assertStringSetOmits(t, plan.RequiredGates, "ai-maintenance:self-test")
	assertStringSetContains(t, plan.AffectedGoPackages, "./scripts/ai_maintenance")
}

func TestBuildGatePlanIncludesChangedBackendPackages(t *testing.T) {
	plan := buildGatePlan([]string{
		"internal/store/thread/store.go",
		"internal/module/memory/service.go",
		"internal/contract/provider.go",
	})

	assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
	assertStringSetOmits(t, plan.RequiredGates, "repo:guard")
	assertStringSetContains(t, plan.AffectedGoPackages,
		"./internal/store/thread",
		"./internal/module/memory",
		"./internal/contract",
	)
	assertStringSetOmits(t, plan.AffectedGoPackages, "./internal/archtest")
}

func TestBuildGatePlanRoutesCapabilityContractProducerInputs(t *testing.T) {
	for _, path := range []string{
		"internal/contract/provider.go",
		"internal/provider/codexapp/support.go",
		"cmd/mcp-orch/orchestration/registry.go",
		"cmd/mcp-orch/tools/position.go",
	} {
		t.Run(path, func(t *testing.T) {
			plan := buildGatePlan([]string{path})
			assertStringSetContains(t, plan.RequiredGates, "capcontract:check")
			assertStringSetContains(t, plan.GeneratedFiles, capabilityContractManifest)
		})
	}

	for _, path := range []string{"frontend-app/src/App.jsx", "docs/README.md"} {
		t.Run("unrelated/"+path, func(t *testing.T) {
			plan := buildGatePlan([]string{path})
			assertStringSetOmits(t, plan.RequiredGates, "capcontract:check")
			assertStringSetOmits(t, plan.GeneratedFiles, capabilityContractManifest)
		})
	}
}

func TestBuildGatePlanRoutesGateInfrastructureToOwnedChecks(t *testing.T) {
	tests := []struct {
		path  string
		gates []string
	}{
		{"Makefile", []string{"backend:test_with_guard", "codemap:check", "frontend:embed-verify", "project-map:check", "sqlc:verify"}},
		{"scripts/test_with_guard.sh", []string{"backend:test_with_guard"}},
		{"scripts/sqlc_verify_worktree.sh", []string{"ai-maintenance:self-test", "sqlc:verify"}},
		{"scripts/frontend_embed_verify.sh", []string{"ai-maintenance:self-test", "frontend:embed-verify"}},
		{"scripts/refresh_generated_artifacts.sh", []string{"ai-maintenance:self-test", "codemap:check", "project-map:check"}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			plan := buildGatePlan([]string{test.path})
			assertStringSetContains(t, plan.RequiredGates, test.gates...)
			assertStringSetContains(t, plan.RequiredGates, "diff:whitespace")
		})
	}
}

func TestBuildGatePlanRoutesSQLCAndGoModuleInputs(t *testing.T) {
	tests := []struct {
		path        string
		wantBackend bool
	}{
		{"go.mod", true},
		{"go.sum", true},
		{"go.work", true},
		{"go.work.sum", true},
		{"sqlc.yaml", false},
		{"cmd/mcp-orch/sqlc.yaml", true},
		{"sql/queries.sql", false},
		{"migrations/001.sql", false},
		{"cmd/mcp-orch/sql/queries.sql", true},
		{"internal/platform/db/sqlite/migrations/001.sql", true},
		{"internal/store/thread/store.go", true},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			plan := buildGatePlan([]string{test.path})
			assertStringSetContains(t, plan.RequiredGates, "sqlc:verify", "diff:whitespace")
			if test.wantBackend {
				assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
			} else {
				assertStringSetOmits(t, plan.RequiredGates, "backend:test_with_guard")
			}
		})
	}
}

func TestBuildGatePlanUsesBackendAsAIMaintenanceSelfTestSuperset(t *testing.T) {
	plan := buildGatePlan([]string{"scripts/ai_maintenance/main.go"})
	assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard")
	assertStringSetOmits(t, plan.RequiredGates, "ai-maintenance:self-test")

	hookPlan := buildGatePlan([]string{".githooks/pre-commit"})
	assertStringSetContains(t, hookPlan.RequiredGates, "ai-maintenance:self-test")
}

func TestGatePlanProducerMatchesRunnerAndEvidenceRegistries(t *testing.T) {
	producerGates := map[string]bool{}
	for _, files := range [][]string{
		{"scripts/ai_maintenance/main.go"},
		{".githooks/pre-commit"},
		{"frontend-app/src/App.jsx"},
		{"internal/store/thread/store.go"},
		{"docs/doc/codemap/ai-index.json"},
	} {
		for _, gate := range buildGatePlan(files).RequiredGates {
			producerGates[gate] = true
		}
	}

	runners := gateRunners(gatePlan{}, gateExecutionScope{})
	assertRegistryMatchesProducer(t, producerGates, runners, "runner", "diff:whitespace")
	assertRegistryMatchesProducer(t, producerGates, gateEvidenceCommandFragments(), "evidence", "diff:whitespace")
}

func TestGateRunnersCacheOnlyStaticGeneratedChecks(t *testing.T) {
	cacheable := map[string]bool{
		"project-map:check": true,
	}
	for gate, runner := range gateRunners(gatePlan{}, gateExecutionScope{}) {
		if runner.cacheable != cacheable[gate] {
			t.Errorf("gate %q cacheable=%v, want %v", gate, runner.cacheable, cacheable[gate])
		}
	}
}

func TestNewGateExecutionScopeRejectsAmbiguousWhitespaceTruth(t *testing.T) {
	if _, err := newGateExecutionScope(true, []string{"base..head"}); err == nil {
		t.Fatal("staged and push-range whitespace scopes were both accepted")
	}
	if _, err := newGateExecutionScope(false, []string{" "}); err == nil {
		t.Fatal("empty push range was accepted")
	}
}

func TestRunWhitespaceCheckUsesExplicitGitTruthScope(t *testing.T) {
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "git-args.log")
	gitPath := filepath.Join(binDir, "git")
	if err := os.WriteFile(gitPath, []byte("#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >>\"$GIT_ARGS_LOG\"\nif [ \"$*\" = \"hash-object -t tree /dev/null\" ]; then\n  printf '%040d\\n' 0\nfi\n"), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_ARGS_LOG", logPath)

	if err := runWhitespaceCheck(gateExecutionScope{diffCached: true}); err != nil {
		t.Fatalf("staged whitespace check: %v", err)
	}
	if err := runWhitespaceCheck(gateExecutionScope{diffRanges: []string{"a..b", "c..d", "head"}}); err != nil {
		t.Fatalf("range whitespace check: %v", err)
	}
	if err := runWhitespaceCheck(gateExecutionScope{}); err != nil {
		t.Fatalf("worktree whitespace check: %v", err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read git args: %v", err)
	}
	got := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{"diff --cached --check", "diff --check a..b", "diff --check c..d", "hash-object -t tree /dev/null", "diff --check 0000000000000000000000000000000000000000 head", "diff --check"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("git whitespace invocations = %q, want %q", got, want)
	}
}

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

func TestValidateEvidenceBlocksMissingAgentIDDiagnosticsAndCommands(t *testing.T) {
	plan := buildGatePlan([]string{"frontend-app/src/App.jsx"})
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
	plan := buildGatePlan([]string{"internal/app/modules.go"})
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
	plan := buildGatePlan([]string{"internal/app/modules.go"})
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
	plan := buildGatePlan([]string{
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
  - cmd: cd frontend-app && npm run lint
    exit: 0
  - cmd: cd frontend-app && npm test
    exit: 0
  - cmd: cd frontend-app && npm run build
    exit: 0
  - cmd: make frontend-embed-verify
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
	plan := buildGatePlan([]string{"internal/app/modules.go"})
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

func TestGateCommandEnvironmentIsolatesCacheIndexFromNonGitGates(t *testing.T) {
	t.Setenv("GIT_INDEX_FILE", filepath.Join(t.TempDir(), "staged-index"))

	for _, entry := range gateCommandEnvironment("go") {
		if strings.HasPrefix(entry, "GIT_INDEX_FILE=") {
			t.Fatalf("non-git gate inherited staged index: %q", entry)
		}
	}

	want := "GIT_INDEX_FILE=" + os.Getenv("GIT_INDEX_FILE")
	if !slices.Contains(gateCommandEnvironment("git"), want) {
		t.Fatalf("git whitespace gate lost staged index %q", want)
	}
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
