package main

import "testing"

import "strings"

func TestAIMaintenanceGateVerifiesLocalHookArtifacts(t *testing.T) {
	script := readScript(t, "ai_maintenance_gates.sh")
	preCommit := readRepoFile(t, "../.githooks/pre-commit")
	prePush := readRepoFile(t, "../.githooks/pre-push")

	assertScriptContains(t, script, "go run ./scripts/ai_maintenance run \"$@\"")
	assertScriptContains(t, preCommit, "run_ai_maintenance_staged_gate")
	assertScriptContains(t, preCommit, "./scripts/ai_maintenance_gates.sh")
	assertScriptContains(t, preCommit, "--changed-file")
	assertScriptContains(t, preCommit, "go run ./scripts/ai_maintenance")
	assertScriptContains(t, preCommit, "scripts/refresh_generated_artifacts.sh capcontract")
	assertScriptContains(t, preCommit, "git add -- \"$CAPCONTRACT_MANIFEST\"")
	assertScriptContains(t, prePush, "run_ai_maintenance_push_gate")
	assertScriptContains(t, prePush, "./scripts/ai_maintenance_gates.sh")
	assertScriptContains(t, prePush, "--changed-file")
	if strings.Contains(prePush, "add_capcontract_path") || strings.Contains(prePush, "internal/provider/*") {
		t.Fatal("pre-push must delegate capcontract routing to the unified AI plan")
	}
}

func TestAIMaintenanceGateImplementationContracts(t *testing.T) {
	source := readRepoFile(t, "../scripts/ai_maintenance/main.go")
	testSource := readRepoFile(t, "../scripts/ai_maintenance/main_test.go")

	assertScriptContains(t, source, "validate-evidence")
	assertScriptContains(t, source, "buildGatePlan")
	assertScriptContains(t, source, "frontend:embed-verify")
	assertScriptContains(t, source, "ai-maintenance:self-test")
	assertScriptContains(t, source, "AGENTID must be exact platform UUID")
	assertScriptContains(t, source, "DONE_WITH_EVIDENCE must not include BLOCKERS")
	assertScriptContains(t, source, "generated file lacks check-failed plus refresh evidence")
	assertScriptContains(t, source, "missing or non-pass LSP evidence")
	assertScriptContains(t, source, "OWNED_FILES_CHANGED does not match changed files")
	assertScriptContains(t, testSource, "TestBuildGatePlanRoutesFrontendBackendAndGeneratedFiles")
	assertScriptContains(t, testSource, "TestValidateEvidenceBlocksMissingAgentIDDiagnosticsAndCommands")
}
