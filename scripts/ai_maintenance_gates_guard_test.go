package main

import (
	"fmt"
	"strings"
	"testing"
)

func validateAIMaintenanceHookRoutes(preCommit, prePush, gateScript string) error {
	for _, required := range []string{
		`run_staged_project_map_refresh "$staged_tree"`,
		`run_staged_light_code_guard "$effective_staged_tree"`,
		`"$project_map_gate_bin" project-map check --tree "$source_tree"`,
		`"$project_map_gate_bin" project-map refresh --tree "$source_tree"`,
		`git -C "$repo_root" add -A -- "$project_map_path"`,
		`config --local --get superdolphin.gateLauncher`,
		`config --local superdolphin.gateLauncher "$built_gate"`,
		`"$install_root/v1/$source_tree"/*/super-dolphin-gate`,
		`./scripts/test_with_guard.sh --light-guard-only`,
		`SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT=1`,
		`worktree_root="$repo_root/.worktrees"`,
	} {
		if !strings.Contains(preCommit, required) {
			return fmt.Errorf("pre-commit must retain lightweight code-guard contract %q", required)
		}
	}
	for _, forbidden := range []string{"gate_output_file", "tee", `remote hook pre-commit`, `remote_config=`, `remote_ledger=`, `SUPER_DOLPHIN_CI_AGENT_TOKEN`, `closure check`, `codemap check`, `capability-contract check`} {
		if strings.Contains(preCommit, forbidden) {
			return fmt.Errorf("pre-commit must not contain complete gate operation %q", forbidden)
		}
	}
	for _, required := range []string{
		`remote hook pre-push`,
		`--config "$remote_config"`,
		`--ledger "$remote_ledger"`,
		`--repository "$repo_root"`,
		`"$gate_bin" "${remote_args[@]}" "$1" "$2" <"$push_input_file"`,
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
	testWithGuard := readScript(t, "test_with_guard.sh")
	preCommit := readRepoFile(t, "../.githooks/pre-commit")
	prePush := readRepoFile(t, "../.githooks/pre-push")

	if err := validateAIMaintenanceHookRoutes(preCommit, prePush, script); err != nil {
		t.Fatal(err)
	}
	assertScriptContains(t, script, "go run ./scripts/ai_maintenance run \"$@\"")
	assertScriptContains(t, preCommit, `run_staged_project_map_refresh "$staged_tree"`)
	assertScriptContains(t, preCommit, `run_staged_light_code_guard "$effective_staged_tree"`)
	assertScriptContains(t, preCommit, `"$project_map_gate_bin" project-map check --tree "$source_tree"`)
	assertScriptContains(t, preCommit, `"$project_map_gate_bin" project-map refresh --tree "$source_tree"`)
	assertScriptContains(t, preCommit, `git -C "$repo_root" add -A -- "$project_map_path"`)
	assertScriptContains(t, preCommit, `./scripts/test_with_guard.sh --light-guard-only`)
	assertScriptContains(t, preCommit, `SUPER_DOLPHIN_GUARD_FAIL_ON_DRIFT=1`)
	lightGuardStart := strings.Index(testWithGuard, "run_light_guard() {")
	archtestStart := strings.Index(testWithGuard, "run_archtest_only() {")
	if lightGuardStart < 0 || archtestStart <= lightGuardStart {
		t.Fatal("test_with_guard must expose a bounded light code-guard function")
	}
	lightGuard := testWithGuard[lightGuardStart:archtestStart]
	assertScriptContains(t, lightGuard, `"$real_go" run ./scripts/code_size_guard.go`)
	assertScriptDoesNotContain(t, lightGuard, `"$real_go" test`)
	assertScriptContains(t, testWithGuard, `[ "$1" != "--light-guard-only" ]`)
	assertScriptDoesNotContain(t, preCommit, `remote hook pre-commit`)
	assertScriptDoesNotContain(t, preCommit, `SUPER_DOLPHIN_CI_AGENT_TOKEN`)
	assertScriptContains(t, prePush, `remote hook pre-push`)
	assertScriptContains(t, prePush, `--config "$remote_config"`)
	assertScriptContains(t, prePush, `--ledger "$remote_ledger"`)
	assertScriptContains(t, prePush, `--repository "$repo_root"`)
	assertScriptContains(t, prePush, `"$gate_bin" "${remote_args[@]}" "$1" "$2" <"$push_input_file"`)
	for name, hook := range map[string]string{"pre-push": prePush} {
		for line := range strings.SplitSeq(hook, "\n") {
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

func TestAIMaintenanceGateRejectsRemotePreCommitAndOutputSpool(t *testing.T) {
	script := readScript(t, "ai_maintenance_gates.sh")
	preCommit := readRepoFile(t, "../.githooks/pre-commit")
	prePush := readRepoFile(t, "../.githooks/pre-push")

	mutatedPreCommit := strings.Replace(
		preCommit,
		`./scripts/test_with_guard.sh --light-guard-only`,
		`"$gate_bin" remote hook pre-commit 2>&1 | tee "$gate_output_file"`,
		1,
	)
	if err := validateAIMaintenanceHookRoutes(mutatedPreCommit, prePush, script); err == nil {
		t.Fatal("reintroducing the pre-commit output spool must be rejected")
	}

	mutatedPreCommit = strings.Replace(preCommit, `./scripts/test_with_guard.sh --light-guard-only`, "", 1)
	if err := validateAIMaintenanceHookRoutes(mutatedPreCommit, prePush, script); err == nil {
		t.Fatal("dropping the staged code guard must be rejected")
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
			name: "pre-commit trusted project-map refresh",
			mutate: func(preCommit, _, _ *string) {
				*preCommit = strings.ReplaceAll(*preCommit, `run_staged_project_map_refresh "$staged_tree"`, "")
			},
		},
		{
			name: "pre-commit staged code guard",
			mutate: func(preCommit, _, _ *string) {
				*preCommit = strings.ReplaceAll(*preCommit, `./scripts/test_with_guard.sh --light-guard-only`, "")
			},
		},
		{
			name: "pre-push remote hook command",
			mutate: func(_, prePush, _ *string) {
				*prePush = strings.ReplaceAll(*prePush, `remote hook pre-push`, "")
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
