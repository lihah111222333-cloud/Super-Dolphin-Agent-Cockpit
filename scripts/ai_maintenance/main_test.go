package main

import (
	"os"
	"path/filepath"
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
		"project-map:check",
		"repo:guard",
	)
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

func TestBuildGatePlanRoutesAIMaintenanceWorkflowToSelfTest(t *testing.T) {
	plan := buildGatePlan([]string{".github/workflows/ai-maintenance-gates.yml"})

	assertStringSetContains(t, plan.RequiredGates, "ai-maintenance:self-test", "diff:whitespace")
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
	assertStringSetContains(t, plan.RequiredGates, "backend:test_with_guard", "ai-maintenance:self-test")
	assertStringSetContains(t, plan.AffectedGoPackages, "./scripts/ai_maintenance")
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
