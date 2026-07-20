package main

import (
	"maps"
	"os/exec"
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
	Permissions map[string]string                 `yaml:"permissions"`
	Jobs        map[string]releaseRolloutGuardJob `yaml:"jobs"`
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
		Uses string         `yaml:"uses"`
		Run  string         `yaml:"run"`
		With map[string]any `yaml:"with"`
	} `yaml:"steps"`
}

func TestReleaseRolloutWorkflowRequiresOneApprovedStageAndNativeARM64Evidence(t *testing.T) {
	workflow := readRepoFile(t, "../.github/workflows/release.yml")
	validator := readRepoFile(t, "release_rollout_validate.sh")
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
	assertScriptContains(t, workflow, "actions: read")
	assertScriptContains(t, workflow, "./scripts/release_rollout_validate.sh")
	assertScriptContains(t, workflow, `[[ "$GITHUB_REF" == "refs/heads/$DEFAULT_BRANCH" ]]`)
	assertScriptContains(t, workflow, `git merge-base --is-ancestor "$BUILD_COMMIT" "origin/$DEFAULT_BRANCH"`)
	assertScriptDoesNotContain(t, workflow, `git merge-base --is-ancestor "origin/$DEFAULT_BRANCH" "$BUILD_COMMIT"`)
	assertScriptDoesNotContain(t, workflow, "continue-on-error:")
	assertScriptDoesNotContain(t, workflow, "rollout-internal-20, rollout-10-percent")
	assertReleaseRolloutValidatorContract(t, validator)
	var parsed releaseRolloutGuardWorkflow
	if err := yaml.Unmarshal([]byte(workflow), &parsed); err != nil {
		t.Fatalf("parse release rollout workflow: %v", err)
	}
	assertReleaseRolloutInputs(t, parsed)
	if !maps.Equal(parsed.Permissions, map[string]string{"actions": "read", "contents": "read"}) {
		t.Fatalf("workflow permissions = %v", parsed.Permissions)
	}
	assertReleaseRolloutJobKeys(t, parsed)
	assertReleaseRolloutJobDAG(t, parsed)
	assertReleaseRolloutRunBlocks(t, parsed)
	assertReleaseRolloutCheckout(t, parsed.Jobs["validate-inputs"])
}

func assertReleaseRolloutValidatorContract(t *testing.T, validator string) {
	t.Helper()
	for _, contract := range []string{
		`"$GITHUB_API_URL/repos/$GITHUB_REPOSITORY"`,
		`owner.type == "Organization"`,
		`conclusion == "success"`,
		`head_sha == $build_commit`,
		`path == $workflow_path`,
		`local artifact_name="update-recovery-upgrade-attestation-$platform"`,
		`release_rollout_verify_upgrade_attestation "$macos_run_id" "macos-arm64"`,
		`release_rollout_verify_upgrade_attestation "$windows_run_id" "windows-arm64"`,
		"update-recovery-stage-attestation-",
		"archive_download_url",
		"monitoring_window_hours",
		`[[ -f "$UPGRADE_EVIDENCE_WORKFLOW_PATH" ]]`,
		"release remains fail-closed",
	} {
		assertScriptContains(t, validator, contract)
	}
	assertScriptDoesNotContain(t, validator, "|| true")
	ownerCheck := strings.Index(validator, `release_rollout_verify_repository_owner "$output_dir"`)
	producerCheck := strings.Index(validator, `[[ -f "$UPGRADE_EVIDENCE_WORKFLOW_PATH" ]]`)
	if ownerCheck < 0 || producerCheck < 0 || ownerCheck > producerCheck {
		t.Fatal("Organization owner check must fail before the missing producer check")
	}
}

func TestReleaseRolloutEvidenceURLRejectsSuffixesAndSameRun(t *testing.T) {
	valid := releaseRolloutRunID(t, "https://github.com/acme/repo/actions/runs/123", "acme/repo")
	if valid != "123" {
		t.Fatalf("run id = %q, want 123", valid)
	}
	for _, invalid := range []string{
		"https://github.com/acme/repo/actions/runs/123/",
		"https://github.com/acme/repo/actions/runs/123?x=1",
		"https://github.com/acme/repo/actions/runs/123#x",
		"https://github.com/other/repo/actions/runs/123",
		"https://github.com/acme/repo/actions/runs/0",
	} {
		if releaseRolloutRunID(t, invalid, "acme/repo") != "" {
			t.Fatalf("invalid evidence URL accepted: %s", invalid)
		}
	}
	command := exec.Command("bash", "-c", `source ./release_rollout_validate.sh; release_rollout_distinct_run_ids "$1" "$2" "$3"`, "bash", "https://github.com/acme/repo/actions/runs/123", "https://github.com/acme/repo/actions/runs/123", "acme/repo")
	if err := command.Run(); err == nil {
		t.Fatal("same macOS and Windows run ID must be rejected")
	}
}

func TestReleaseRolloutStructuralGuardRejectsRogueJobAndMissingEnv(t *testing.T) {
	workflow := readRepoFile(t, "../.github/workflows/release.yml")
	rogue := workflow + "\n  rogue-dynamic-environment:\n    environment: update-recovery-${{ inputs.stage }}\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo rogue\n"
	if releaseRolloutStructureMatches(t, rogue) {
		t.Fatal("extra no-needs dynamic-environment job must be rejected")
	}
	missingEnv := strings.Replace(workflow, "      GITHUB_API_URL: ${{ github.api_url }}\n", "", 1)
	if releaseRolloutStructureMatches(t, missingEnv) {
		t.Fatal("missing security-critical validate env must be rejected")
	}
}

func releaseRolloutRunID(t *testing.T, url, repository string) string {
	t.Helper()
	command := exec.Command("bash", "-c", `source ./release_rollout_validate.sh; release_rollout_run_id "$1" "$2"`, "bash", url, repository)
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
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

func assertReleaseRolloutJobKeys(t *testing.T, workflow releaseRolloutGuardWorkflow) {
	t.Helper()
	want := []string{"package-macos-arm64", "package-windows-arm64", "rollout-10-percent", "rollout-100-percent", "rollout-30-percent", "rollout-internal-20", "validate-inputs"}
	got := make([]string, 0, len(workflow.Jobs))
	for key := range workflow.Jobs {
		got = append(got, key)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("workflow jobs = %v, want exact %v", got, want)
	}
}

func assertReleaseRolloutJobDAG(t *testing.T, workflow releaseRolloutGuardWorkflow) {
	t.Helper()
	assertReleaseRolloutValidationBoundary(t, workflow.Jobs["validate-inputs"])
	assertReleaseRolloutMacBoundary(t, workflow.Jobs["package-macos-arm64"])
	assertReleaseRolloutWindowsBoundary(t, workflow.Jobs["package-windows-arm64"])
	for _, stage := range []string{"internal-20", "10-percent", "30-percent", "100-percent"} {
		assertReleaseRolloutStageBoundary(t, stage, workflow.Jobs["rollout-"+stage])
	}
}

func assertReleaseRolloutMacBoundary(t *testing.T, job releaseRolloutGuardJob) {
	t.Helper()
	wantEnv := map[string]string{
		"VERSION": "${{ inputs.version }}", "BUILD_COMMIT": "${{ inputs.build_commit }}", "PREVIOUS_VERSION": "${{ inputs.previous_version }}",
		"MONITORING_WINDOW_HOURS": "${{ inputs.monitoring_window_hours }}", "MACOS_UPGRADE_MATRIX_EVIDENCE": "${{ inputs.macos_upgrade_matrix_evidence }}",
		"WINDOWS_UPGRADE_MATRIX_EVIDENCE": "${{ inputs.windows_upgrade_matrix_evidence }}", "SUPER_DOLPHIN_RELEASE_PROFILE": "gray",
		"CODESIGN_IDENTITY": "${{ secrets.CODESIGN_IDENTITY }}", "NOTARY_PROFILE": "${{ secrets.NOTARY_PROFILE }}",
		"SUPER_DOLPHIN_UPDATE_PUBLIC_KEY": "${{ secrets.SUPER_DOLPHIN_UPDATE_PUBLIC_KEY }}", "EXPECTED_PUBLIC_KEY_FINGERPRINT": "${{ inputs.signing_public_key_fingerprint }}",
	}
	if !slices.Equal(releaseRolloutNodeStrings(job.Needs), []string{"validate-inputs"}) || job.If != "" || job.Environment != "update-recovery-${{ inputs.stage }}" || !maps.Equal(job.Env, wantEnv) {
		t.Fatalf("macOS boundary = needs %v if %q environment %q env %v", releaseRolloutNodeStrings(job.Needs), job.If, job.Environment, job.Env)
	}
	assertReleaseRolloutRunner(t, "macOS", job.RunsOn, []string{"self-hosted", "macOS", "ARM64", "update-recovery-release"})
}

func assertReleaseRolloutWindowsBoundary(t *testing.T, job releaseRolloutGuardJob) {
	t.Helper()
	wantEnv := map[string]string{
		"VERSION": "${{ inputs.version }}", "BUILD_COMMIT": "${{ inputs.build_commit }}", "PREVIOUS_VERSION": "${{ inputs.previous_version }}",
		"MONITORING_WINDOW_HOURS": "${{ inputs.monitoring_window_hours }}", "MACOS_UPGRADE_MATRIX_EVIDENCE": "${{ inputs.macos_upgrade_matrix_evidence }}",
		"WINDOWS_UPGRADE_MATRIX_EVIDENCE": "${{ inputs.windows_upgrade_matrix_evidence }}", "SUPER_DOLPHIN_WINDOWS_ARCH": "arm64",
	}
	if !slices.Equal(releaseRolloutNodeStrings(job.Needs), []string{"validate-inputs", "package-macos-arm64"}) || job.If != "" || job.Environment != "" || !maps.Equal(job.Env, wantEnv) {
		t.Fatalf("Windows boundary = needs %v if %q environment %q env %v", releaseRolloutNodeStrings(job.Needs), job.If, job.Environment, job.Env)
	}
	assertReleaseRolloutRunner(t, "Windows", job.RunsOn, []string{"self-hosted", "Windows", "ARM64", "update-recovery-release"})
}

func assertReleaseRolloutStageBoundary(t *testing.T, stage string, job releaseRolloutGuardJob) {
	t.Helper()
	wantEnv := map[string]string{
		"STAGE": stage, "VERSION": "${{ inputs.version }}", "BUILD_COMMIT": "${{ inputs.build_commit }}",
		"SIGNING_PUBLIC_KEY_FINGERPRINT": "${{ inputs.signing_public_key_fingerprint }}", "PREVIOUS_VERSION": "${{ inputs.previous_version }}",
		"MONITORING_WINDOW_HOURS": "${{ inputs.monitoring_window_hours }}", "PREDECESSOR_EVIDENCE": "${{ inputs.predecessor_evidence }}",
		"MACOS_UPGRADE_MATRIX_EVIDENCE": "${{ inputs.macos_upgrade_matrix_evidence }}", "WINDOWS_UPGRADE_MATRIX_EVIDENCE": "${{ inputs.windows_upgrade_matrix_evidence }}",
		"STAGE_ATTESTATION_NAME": "update-recovery-stage-attestation-" + stage,
	}
	wantNeeds := []string{"validate-inputs", "package-macos-arm64", "package-windows-arm64"}
	if !slices.Equal(releaseRolloutNodeStrings(job.Needs), wantNeeds) || job.If != "${{ inputs.stage == '"+stage+"' }}" || job.Environment != "" || !releaseRolloutScalarEquals(job.RunsOn, "ubuntu-latest") || !maps.Equal(job.Env, wantEnv) {
		t.Fatalf("stage %s boundary = needs %v if %q environment %q runs-on %v env %v", stage, releaseRolloutNodeStrings(job.Needs), job.If, job.Environment, releaseRolloutNodeStrings(job.RunsOn), job.Env)
	}
}

func assertReleaseRolloutValidationBoundary(t *testing.T, validate releaseRolloutGuardJob) {
	t.Helper()
	wantEnv := map[string]string{
		"STAGE": "${{ inputs.stage }}", "VERSION": "${{ inputs.version }}", "BUILD_COMMIT": "${{ inputs.build_commit }}",
		"SIGNING_PUBLIC_KEY_FINGERPRINT": "${{ inputs.signing_public_key_fingerprint }}", "PREVIOUS_VERSION": "${{ inputs.previous_version }}",
		"MONITORING_WINDOW_HOURS": "${{ inputs.monitoring_window_hours }}", "PREDECESSOR_EVIDENCE": "${{ inputs.predecessor_evidence }}",
		"MACOS_UPGRADE_MATRIX_EVIDENCE": "${{ inputs.macos_upgrade_matrix_evidence }}", "WINDOWS_UPGRADE_MATRIX_EVIDENCE": "${{ inputs.windows_upgrade_matrix_evidence }}",
		"DEFAULT_BRANCH": "${{ github.event.repository.default_branch }}", "GITHUB_REPOSITORY": "${{ github.repository }}",
		"GITHUB_API_URL": "${{ github.api_url }}", "GITHUB_TOKEN": "${{ github.token }}",
		"RELEASE_WORKFLOW_PATH": ".github/workflows/release.yml", "UPGRADE_EVIDENCE_WORKFLOW_PATH": ".github/workflows/update-recovery-upgrade-evidence.yml",
	}
	if len(releaseRolloutNodeStrings(validate.Needs)) != 0 || validate.If != "" || validate.Environment != "" || !maps.Equal(validate.Env, wantEnv) || !releaseRolloutScalarEquals(validate.RunsOn, "ubuntu-latest") {
		t.Fatalf("validation boundary = needs %v if %q environment %q runs-on %v env %v", releaseRolloutNodeStrings(validate.Needs), validate.If, validate.Environment, releaseRolloutNodeStrings(validate.RunsOn), validate.Env)
	}
}

func assertReleaseRolloutCheckout(t *testing.T, validate releaseRolloutGuardJob) {
	t.Helper()
	if len(validate.Steps) != 4 || !strings.Contains(validate.Steps[0].Run, `case "$STAGE" in`) || validate.Steps[1].Uses != "actions/checkout@v4" || validate.Steps[1].With["ref"] != "${{ github.event.repository.default_branch }}" || validate.Steps[1].With["fetch-depth"] != 0 || !strings.Contains(validate.Steps[2].Run, `git merge-base --is-ancestor "$BUILD_COMMIT" "origin/$DEFAULT_BRANCH"`) || validate.Steps[3].Run != "./scripts/release_rollout_validate.sh" {
		t.Fatalf("validate step order must be input -> checkout -> ancestry -> attestation: %#v", validate.Steps)
	}
}

func releaseRolloutStructureMatches(t *testing.T, source string) bool {
	t.Helper()
	var workflow releaseRolloutGuardWorkflow
	if yaml.Unmarshal([]byte(source), &workflow) != nil {
		return false
	}
	wantJobs := []string{"package-macos-arm64", "package-windows-arm64", "rollout-10-percent", "rollout-100-percent", "rollout-30-percent", "rollout-internal-20", "validate-inputs"}
	gotJobs := make([]string, 0, len(workflow.Jobs))
	for key := range workflow.Jobs {
		gotJobs = append(gotJobs, key)
	}
	slices.Sort(gotJobs)
	return slices.Equal(gotJobs, wantJobs) && workflow.Jobs["validate-inputs"].Env["GITHUB_API_URL"] == "${{ github.api_url }}"
}

func releaseRolloutScalarEquals(node yaml.Node, want string) bool {
	return node.Kind == yaml.ScalarNode && node.Value == want
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
	assertScriptContains(t, runbook, "当前仓库 owner 类型为 `User`")
	assertScriptContains(t, runbook, "本 workflow 当前不可部署、不可用于发布")
	assertScriptContains(t, runbook, "visibility=selected")
	assertScriptContains(t, runbook, "restricted_to_workflows=true")
	assertScriptContains(t, runbook, "owner/repo/.github/workflows/release.yml@refs/heads/<default_branch>")
	assertScriptContains(t, runbook, "不得退化为仅信 URL")
	assertScriptContains(t, runbook, "当前仓库尚未部署该可信 producer")
	assertScriptContains(t, runbook, "`monitoring_window_hours` 完整五元组")

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
