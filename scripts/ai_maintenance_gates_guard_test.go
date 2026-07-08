package main

import "testing"

func TestAIMaintenanceGateVerifiesFrontendEmbedArtifacts(t *testing.T) {
	script := readScript(t, "ai_maintenance_gates.sh")
	workflow := readRepoFile(t, "../.github/workflows/ai-maintenance-gates.yml")
	makefile := readRepoFile(t, "../Makefile")

	assertScriptContains(t, script, "go run ./scripts/ai_maintenance run \"$@\"")
	assertScriptContains(t, makefile, "ai-maintenance-gates:")
	assertScriptContains(t, makefile, "./scripts/ai_maintenance_gates.sh $(AI_MAINTENANCE_ARGS)")
	assertScriptContains(t, workflow, "Run AI maintenance gates")
	assertScriptContains(t, workflow, "./scripts/ai_maintenance_gates.sh")
	assertScriptContains(t, workflow, "github.event.pull_request.base.sha")
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
