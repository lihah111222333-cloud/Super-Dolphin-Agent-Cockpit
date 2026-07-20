package main

import (
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type releaseRolloutGuardWorkflow struct {
	On struct {
		WorkflowDispatch struct {
			Inputs map[string]releaseRolloutGuardInput `yaml:"inputs"`
		} `yaml:"workflow_dispatch"`
	} `yaml:"on"`
	Jobs map[string]releaseRolloutGuardJob `yaml:"jobs"`
}

type releaseRolloutGuardInput struct {
	Required bool     `yaml:"required"`
	Type     string   `yaml:"type"`
	Options  []string `yaml:"options"`
}

type releaseRolloutGuardJob struct {
	Needs       yaml.Node         `yaml:"needs"`
	If          string            `yaml:"if"`
	Environment string            `yaml:"environment"`
	RunsOn      yaml.Node         `yaml:"runs-on"`
	Env         map[string]string `yaml:"env"`
	Steps       []struct {
		Run string `yaml:"run"`
	} `yaml:"steps"`
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
	assertScriptContains(t, workflow, "macos_upgrade_matrix_evidence:\n        description:")
	assertScriptContains(t, workflow, "windows_upgrade_matrix_evidence:\n        description:")
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
		"group: update-recovery-release",
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
	assertScriptContains(t, workflow, `case "$STAGE" in`)
	assertScriptContains(t, workflow, "unsupported rollout stage")
	assertScriptContains(t, workflow, "macOS and Windows upgrade matrix evidence must differ")
	assertScriptContains(t, workflow, `GITHUB_REPOSITORY: ${{ github.repository }}`)
	assertScriptContains(t, workflow, `^https://github.com/$GITHUB_REPOSITORY/actions/runs/[0-9]+([/?#].*)?$`)
	assertScriptContains(t, workflow, `[[ "$GITHUB_REF" == "refs/heads/$DEFAULT_BRANCH" ]]`)
	assertScriptDoesNotContain(t, workflow, "continue-on-error:")
	assertScriptDoesNotContain(t, workflow, "rollout-internal-20, rollout-10-percent")
	var parsed releaseRolloutGuardWorkflow
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("parse release rollout workflow: %v", err)
	}
	assertReleaseRolloutInputs(t, parsed)
	assertReleaseRolloutJobDAG(t, parsed)
	assertReleaseRolloutRunBlocks(t, parsed)
}

func assertReleaseRolloutInputs(t *testing.T, workflow releaseRolloutGuardWorkflow) {
	t.Helper()
	for _, input := range []string{"stage", "version", "build_commit", "signing_public_key_fingerprint", "previous_version", "monitoring_window_hours", "predecessor_evidence", "macos_upgrade_matrix_evidence", "windows_upgrade_matrix_evidence"} {
		if !workflow.On.WorkflowDispatch.Inputs[input].Required {
			t.Fatalf("workflow_dispatch input %s must be required", input)
		}
	}
	wantStages := []string{"internal-20", "10-percent", "30-percent", "100-percent"}
	if stage := workflow.On.WorkflowDispatch.Inputs["stage"]; stage.Type != "choice" || !slices.Equal(stage.Options, wantStages) {
		t.Fatalf("stage input = type %q options %v, want exact choice %v", stage.Type, stage.Options, wantStages)
	}
}

func assertReleaseRolloutRunBlocks(t *testing.T, workflow releaseRolloutGuardWorkflow) {
	t.Helper()
	for jobName, job := range workflow.Jobs {
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "${{ inputs.") {
				t.Fatalf("job %s run block interpolates untrusted workflow input directly: %q", jobName, step.Run)
			}
		}
	}
}

func assertReleaseRolloutJobDAG(t *testing.T, workflow releaseRolloutGuardWorkflow) {
	t.Helper()
	assertReleaseRolloutValidationBoundary(t, workflow.Jobs["validate-inputs"])
	macOS := workflow.Jobs["package-macos-arm64"]
	if !slices.Equal(releaseRolloutNodeStrings(macOS.Needs), []string{"validate-inputs"}) || macOS.Environment != "update-recovery-${{ inputs.stage }}" {
		t.Fatalf("macOS approval boundary = needs %v environment %q", releaseRolloutNodeStrings(macOS.Needs), macOS.Environment)
	}
	assertReleaseRolloutRunner(t, "macOS", macOS.RunsOn, []string{"self-hosted", "macOS", "ARM64", "update-recovery-release"})
	windows := workflow.Jobs["package-windows-arm64"]
	if !slices.Equal(releaseRolloutNodeStrings(windows.Needs), []string{"validate-inputs", "package-macos-arm64"}) {
		t.Fatalf("Windows evidence needs = %v", releaseRolloutNodeStrings(windows.Needs))
	}
	assertReleaseRolloutRunner(t, "Windows", windows.RunsOn, []string{"self-hosted", "Windows", "ARM64", "update-recovery-release"})
	for _, stage := range []string{"internal-20", "10-percent", "30-percent", "100-percent"} {
		job := workflow.Jobs["rollout-"+stage]
		wantNeeds := []string{"validate-inputs", "package-macos-arm64", "package-windows-arm64"}
		if !slices.Equal(releaseRolloutNodeStrings(job.Needs), wantNeeds) || job.If != "${{ inputs.stage == '"+stage+"' }}" || job.Environment != "" {
			t.Fatalf("stage %s DAG = needs %v if %q environment %q", stage, releaseRolloutNodeStrings(job.Needs), job.If, job.Environment)
		}
	}
}

func assertReleaseRolloutValidationBoundary(t *testing.T, validate releaseRolloutGuardJob) {
	t.Helper()
	if len(releaseRolloutNodeStrings(validate.Needs)) != 0 || validate.Env["DEFAULT_BRANCH"] != "${{ github.event.repository.default_branch }}" || validate.Env["GITHUB_REPOSITORY"] != "${{ github.repository }}" {
		t.Fatalf("validation boundary = needs %v default branch %q repository %q", releaseRolloutNodeStrings(validate.Needs), validate.Env["DEFAULT_BRANCH"], validate.Env["GITHUB_REPOSITORY"])
	}
}

func assertReleaseRolloutRunner(t *testing.T, platform string, runsOn yaml.Node, wantLabels []string) {
	t.Helper()
	group := releaseRolloutMappingValue(runsOn, "group")
	labels := releaseRolloutMappingValue(runsOn, "labels")
	if group.Value != "update-recovery-release" || !slices.Equal(releaseRolloutNodeStrings(labels), wantLabels) {
		t.Fatalf("%s runner boundary = group %q labels %v", platform, group.Value, releaseRolloutNodeStrings(labels))
	}
}

func releaseRolloutMappingValue(node yaml.Node, key string) yaml.Node {
	for index := 0; index+1 < len(node.Content); index += 2 {
		if node.Content[index].Value == key {
			return *node.Content[index+1]
		}
	}
	return yaml.Node{}
}

func releaseRolloutNodeStrings(node yaml.Node) []string {
	if node.Kind == yaml.ScalarNode && node.Value != "" {
		return []string{node.Value}
	}
	values := make([]string, 0, len(node.Content))
	for _, child := range node.Content {
		values = append(values, child.Value)
	}
	return values
}

func TestReleaseRolloutRunbookDefinesExactLadderMetricsAndStopActions(t *testing.T) {
	runbook := readRepoFile(t, "../docs/运维/update-recovery-schema-rollout.md")
	assertScriptContains(t, runbook, "内部 20 台 -> 10% -> 30% -> 100%")
	assertScriptContains(t, runbook, "内部阶段必须覆盖真实 macOS arm64 与 Windows arm64")
	assertScriptContains(t, runbook, "10%、30%、100% 每阶段观察窗口不得少于 8 小时")
	assertScriptContains(t, runbook, "必须在首次运行 workflow 前预先创建")
	assertScriptContains(t, runbook, "逐一配置 Required reviewers")
	assertScriptContains(t, runbook, "视为发布 blocker，禁止触发 workflow")
	assertScriptContains(t, runbook, "当前 commit 的版本无关恢复状态矩阵")
	assertScriptContains(t, runbook, "macos_upgrade_matrix_evidence")
	assertScriptContains(t, runbook, "windows_upgrade_matrix_evidence")
	assertScriptContains(t, runbook, "runner group `update-recovery-release`")
	assertScriptContains(t, runbook, "仅授权本仓库和默认分支的 `.github/workflows/release.yml`")

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
