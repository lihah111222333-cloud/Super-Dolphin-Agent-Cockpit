package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseRolloutGuardWorkflow struct {
	On struct {
		WorkflowDispatch struct {
			Inputs map[string]struct {
				Required bool `yaml:"required"`
			} `yaml:"inputs"`
		} `yaml:"workflow_dispatch"`
	} `yaml:"on"`
	Jobs map[string]struct {
		Steps []struct {
			Run string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func TestReleaseRolloutWorkflowRequiresOneApprovedStageAndNativeARM64Evidence(t *testing.T) {
	workflow := readRepoFile(t, "../.github/workflows/release.yml")
	actionlint := readRepoFile(t, "../.github/actionlint.yaml")
	assertScriptContains(t, actionlint, "self-hosted-runner:\n  labels:\n    - update-recovery-release")
	for _, input := range []string{
		"version:", "build_commit:", "signing_public_key_fingerprint:",
		"previous_version:", "monitoring_window_hours:",
	} {
		assertScriptContains(t, workflow, input+"\n        description:")
	}
	assertScriptContains(t, workflow, "predecessor_evidence:\n        description:")
	for _, stage := range []struct {
		value string
	}{
		{value: "internal-20"},
		{value: "10-percent"},
		{value: "30-percent"},
		{value: "100-percent"},
	} {
		assertScriptContains(t, workflow, "- "+stage.value)
		assertScriptContains(t, workflow, "if: ${{ inputs.stage == '"+stage.value+"' }}")
	}
	for _, evidence := range []string{
		"environment: update-recovery-${{ inputs.stage }}",
		"runs-on: [self-hosted, macOS, ARM64, update-recovery-release]",
		"runs-on: [self-hosted, Windows, ARM64, update-recovery-release]",
		"make release-update-gate 2>&1 | tee",
		"./scripts/package_macos.sh 2>&1 | tee",
		"./scripts/package_windows.ps1 -Artifact zip",
		"CODESIGN_IDENTITY: ${{ secrets.CODESIGN_IDENTITY }}",
		"NOTARY_PROFILE: ${{ secrets.NOTARY_PROFILE }}",
		"needs: [validate-inputs, package-macos-arm64]",
		"trimmed=\"$(printf '%s'",
		"name: update-recovery-native-evidence-macos-arm64",
		"name: update-recovery-native-evidence-windows-arm64",
	} {
		assertScriptContains(t, workflow, evidence)
	}
	assertScriptContains(t, workflow, "monitoring_window_hours must be an integer >= 8 for percentage stages")
	assertScriptContains(t, workflow, "input is blank after trimming whitespace")
	assertScriptDoesNotContain(t, workflow, "continue-on-error:")
	assertScriptDoesNotContain(t, workflow, "rollout-internal-20, rollout-10-percent")
	var parsed releaseRolloutGuardWorkflow
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("parse release rollout workflow: %v", err)
	}
	for _, input := range []string{"stage", "version", "build_commit", "signing_public_key_fingerprint", "previous_version", "monitoring_window_hours", "predecessor_evidence"} {
		if !parsed.On.WorkflowDispatch.Inputs[input].Required {
			t.Fatalf("workflow_dispatch input %s must be required", input)
		}
	}
	for jobName, job := range parsed.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "${{ inputs.") {
				t.Fatalf("job %s run block interpolates untrusted workflow input directly: %q", jobName, step.Run)
			}
		}
	}
}

func TestReleaseRolloutRunbookDefinesExactLadderMetricsAndStopActions(t *testing.T) {
	runbook := readRepoFile(t, "../docs/运维/update-recovery-schema-rollout.md")
	assertScriptContains(t, runbook, "内部 20 台 -> 10% -> 30% -> 100%")
	assertScriptContains(t, runbook, "内部阶段必须覆盖真实 macOS arm64 与 Windows arm64")
	assertScriptContains(t, runbook, "10%、30%、100% 每阶段观察窗口不得少于 8 小时")
	assertScriptContains(t, runbook, "必须在首次运行 workflow 前预先创建")
	assertScriptContains(t, runbook, "逐一配置 Required reviewers")
	assertScriptContains(t, runbook, "视为发布 blocker，禁止触发 workflow")

	metrics := []string{
		"schema_helper_reap_failed_total | > 0 | 立即停止扩量",
		"schema_helper_capacity_exhausted_rate | > 0.1% | 停止扩量",
		"schema_helper_duration_p95 | > 1.5s | 停止扩量",
		"recovery_transaction_age_max | > 60s | 停止扩量并保留 journal",
		"sqlite_mcp_first_start_failure_rate | > 1% | 停止扩量",
		"rollback_convergence_failure_total | > 0 | 回撤当前版本",
	}
	for _, metric := range metrics {
		if count := strings.Count(runbook, metric); count != 1 {
			t.Fatalf("runbook metric contract %q count = %d, want exactly one", metric, count)
		}
	}
	assertScriptContains(t, runbook, "任何停止条件命中后禁止自动进入下一阶段")
	assertScriptContains(t, runbook, "workflow_dispatch 每次只能选择并执行一个阶段")
}
