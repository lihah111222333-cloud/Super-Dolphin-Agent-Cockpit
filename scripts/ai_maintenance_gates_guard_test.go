package main

import (
	"fmt"
	"strings"
	"testing"
)

func validateAIMaintenanceHookRoutes(preCommit, prePush, gateScript string) error {
	for _, required := range []string{
		`"$gate_bin" closure check --tree "$staged_tree"`,
		`"$gate_bin" closure refresh-dependencies --tree "$staged_tree"`,
		`git -C "$repo_root" add -u -- build/gate/runtime-deps.lock`,
		`"$gate_bin" closure refresh --tree "$staged_tree"`,
		`git -C "$repo_root" add -u -- "${closure_outputs[@]}"`,
		`remote hook pre-commit`,
		`--config "$remote_config"`,
		`--ledger "$remote_ledger"`,
		`--repository "$repo_root"`,
		`--tree "$staged_tree"`,
		`--parent "$parent_commit"`,
		`"$gate_bin" "${remote_args[@]}" 2>&1 | tee "$gate_output_file"`,
	} {
		if !strings.Contains(preCommit, required) {
			return fmt.Errorf("pre-commit must route through trusted gate CLI command %q", required)
		}
	}
	for _, required := range []string{
		`remote hook pre-push`,
		`--config "$remote_config"`,
		`--ledger "$remote_ledger"`,
		`--repository "$repo_root"`,
		`exec "$gate_bin" "${remote_args[@]}" "$1" "$2"`,
	} {
		if !strings.Contains(prePush, required) {
			return fmt.Errorf("pre-push must route through trusted gate CLI command %q", required)
		}
	}
	if !strings.Contains(gateScript, "go run ./scripts/ai_maintenance run \"$@\"") {
		return fmt.Errorf("container-owned ai_maintenance_gates.sh must invoke scripts/ai_maintenance run")
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
	assertScriptContains(t, preCommit, `if ! "$gate_bin" closure check --tree "$staged_tree"; then`)
	assertScriptContains(t, preCommit, `"$gate_bin" closure refresh-dependencies --tree "$staged_tree"`)
	assertScriptContains(t, preCommit, `git -C "$repo_root" add -u -- build/gate/runtime-deps.lock`)
	assertScriptContains(t, preCommit, `"$gate_bin" closure refresh --tree "$staged_tree"`)
	assertScriptContains(t, preCommit, `git -C "$repo_root" add -u -- "${closure_outputs[@]}"`)
	assertScriptContains(t, preCommit, `remote hook pre-commit`)
	assertScriptContains(t, preCommit, `--config "$remote_config"`)
	assertScriptContains(t, preCommit, `--ledger "$remote_ledger"`)
	assertScriptContains(t, preCommit, `--repository "$repo_root"`)
	assertScriptContains(t, preCommit, `"$gate_bin" "${remote_args[@]}" 2>&1 | tee "$gate_output_file"`)
	assertScriptContains(t, prePush, `remote hook pre-push`)
	assertScriptContains(t, prePush, `--config "$remote_config"`)
	assertScriptContains(t, prePush, `--ledger "$remote_ledger"`)
	assertScriptContains(t, prePush, `--repository "$repo_root"`)
	assertScriptContains(t, prePush, `exec "$gate_bin" "${remote_args[@]}" "$1" "$2"`)
	for name, hook := range map[string]string{"pre-commit": preCommit, "pre-push": prePush} {
		for _, line := range strings.Split(hook, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "printf ") {
				continue
			}
			for _, forbidden := range []string{"ai_maintenance", "test_with_guard", "go run", "npm ", "make "} {
				if strings.Contains(line, forbidden) {
					t.Fatalf("%s contains forbidden host gate %q", name, forbidden)
				}
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
			name: "pre-commit remote hook command",
			mutate: func(preCommit, _, _ *string) {
				*preCommit = strings.Replace(*preCommit, `remote hook pre-commit`, "", 1)
			},
		},
		{
			name: "pre-commit closure refresh",
			mutate: func(preCommit, _, _ *string) {
				*preCommit = strings.Replace(*preCommit, `"$gate_bin" closure refresh --tree "$staged_tree"`, "", 1)
			},
		},
		{
			name: "pre-push remote hook command",
			mutate: func(_, prePush, _ *string) {
				*prePush = strings.Replace(*prePush, `remote hook pre-push`, "", 1)
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
	evidenceSource := readRepoFile(t, "../scripts/ai_maintenance/evidence.go")
	testSource := readRepoFile(t, "../scripts/ai_maintenance/main_test.go") +
		readRepoFile(t, "../scripts/ai_maintenance/owner_evidence_test.go")

	assertScriptContains(t, source, "validate-evidence")
	assertScriptContains(t, source, "buildGatePlan")
	assertScriptContains(t, source, "frontend:embed-verify")
	assertScriptContains(t, source, "ai-maintenance:self-test")
	assertScriptContains(t, evidenceSource, "AGENTID must be exact platform UUID")
	assertScriptContains(t, evidenceSource, "DONE_WITH_EVIDENCE must not include BLOCKERS")
	assertScriptContains(t, evidenceSource, "generated file lacks check-failed plus refresh evidence")
	assertScriptContains(t, evidenceSource, "missing or non-pass LSP evidence")
	assertScriptContains(t, evidenceSource, "OWNED_FILES_CHANGED does not match changed files")
	assertScriptContains(t, testSource, "TestBuildGatePlanRoutesFrontendBackendAndGeneratedFiles")
	assertScriptContains(t, testSource, "TestValidateEvidenceBlocksMissingAgentIDDiagnosticsAndCommands")
}

func TestGeneratedArtifactRefreshOrdersProducersBeforeConsumers(t *testing.T) {
	refresh := readScript(t, "refresh_generated_artifacts.sh")
	assertScriptContains(t, refresh, "refresh_codemap\n    refresh_capcontract\n    refresh_project_map")
}
