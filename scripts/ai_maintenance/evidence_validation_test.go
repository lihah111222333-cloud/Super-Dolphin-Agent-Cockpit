package main

import "testing"

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
  - cmd: cd frontend-app && npm run guard:architecture
    exit: 0
  - cmd: cd frontend-app && npm run lint
    exit: 0
  - cmd: cd frontend-app && npm run typecheck:contracts
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
