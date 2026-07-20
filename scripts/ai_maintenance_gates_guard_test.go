package main

import (
	"fmt"
	"strings"
	"testing"
)

func hasShellInvocation(script, command string) bool {
	for line := range strings.SplitSeq(script, "\n") {
		if strings.TrimSpace(line) == command {
			return true
		}
	}
	return false
}

func validateAIMaintenanceHookRoutes(preCommit, prePush, gateScript string) error {
	if !hasShellInvocation(preCommit, "run_ai_maintenance_staged_gate") {
		return fmt.Errorf("pre-commit must invoke the staged AI maintenance gate")
	}
	if !strings.Contains(preCommit, "./scripts/ai_maintenance_gates.sh") || !strings.Contains(preCommit, "--changed-file") {
		return fmt.Errorf("pre-commit must route changed files through ai_maintenance_gates.sh")
	}
	if !hasShellInvocation(prePush, "run_ai_maintenance_push_gate") {
		return fmt.Errorf("pre-push must invoke the push AI maintenance gate")
	}
	if !strings.Contains(prePush, "./scripts/ai_maintenance_gates.sh") || !strings.Contains(prePush, "--changed-file") {
		return fmt.Errorf("pre-push must route changed files through ai_maintenance_gates.sh")
	}
	if !strings.Contains(gateScript, "go run ./scripts/ai_maintenance run \"$@\"") {
		return fmt.Errorf("ai_maintenance_gates.sh must invoke scripts/ai_maintenance run")
	}
	return nil
}

func TestAIMaintenanceGateVerifiesLocalHookArtifacts(t *testing.T) {
	script := readScript(t, "ai_maintenance_gates.sh")
	preCommit := readRepoFile(t, "../.githooks/pre-commit")
	prePush := readRepoFile(t, "../.githooks/pre-push")

	if err := validateAIMaintenanceHookRoutes(preCommit, prePush, script); err != nil {
		t.Fatal(err)
	}
	assertScriptContains(t, script, "go run ./scripts/ai_maintenance run \"$@\"")
	assertScriptContains(t, preCommit, `"$gate_bin" hook pre-commit >"$gate_output_file" 2>&1`)
	assertScriptContains(t, preCommit, `exec "$gate_bin" wait --job "$job_id"`)
	assertScriptContains(t, prePush, `exec "$gate_bin" hook pre-push "$1" "$2"`)
	for name, hook := range map[string]string{"pre-commit": preCommit, "pre-push": prePush} {
		for _, forbidden := range []string{"ai_maintenance", "test_with_guard", "go run", "npm ", "make "} {
			if strings.Contains(hook, forbidden) {
				t.Fatalf("%s contains forbidden host gate %q", name, forbidden)
			}
		}
	}
}

func TestAIMaintenanceGateRouteDeletionMutations(t *testing.T) {
	script := readScript(t, "ai_maintenance_gates.sh")
	preCommit := readRepoFile(t, "../.githooks/pre-commit")
	prePush := readRepoFile(t, "../.githooks/pre-push")

	mutations := []struct {
		name   string
		mutate func(*string, *string, *string)
	}{
		{
			name: "pre-commit invocation",
			mutate: func(preCommit, _, _ *string) {
				*preCommit = strings.Replace(*preCommit, "\nrun_ai_maintenance_staged_gate\n", "\n", 1)
			},
		},
		{
			name: "pre-push invocation",
			mutate: func(_, prePush, _ *string) {
				*prePush = strings.Replace(*prePush, "\nrun_ai_maintenance_push_gate\n", "\n", 1)
			},
		},
		{
			name: "ai-maintenance runner",
			mutate: func(_, _, gateScript *string) {
				*gateScript = strings.Replace(*gateScript, "go run ./scripts/ai_maintenance run \"$@\"", "", 1)
			},
		},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			mutatedPreCommit, mutatedPrePush, mutatedScript := preCommit, prePush, script
			mutation.mutate(&mutatedPreCommit, &mutatedPrePush, &mutatedScript)
			if err := validateAIMaintenanceHookRoutes(mutatedPreCommit, mutatedPrePush, mutatedScript); err == nil {
				t.Fatalf("deleting %s route must be rejected", mutation.name)
			}
		})
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

func TestGeneratedArtifactRefreshOrdersProducersBeforeConsumers(t *testing.T) {
	refresh := readScript(t, "refresh_generated_artifacts.sh")
	assertScriptContains(t, refresh, "refresh_codemap\n    refresh_capcontract\n    refresh_project_map")
}
