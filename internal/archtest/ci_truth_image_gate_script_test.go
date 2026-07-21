package archtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const testBootstrapImage = "registry.example/super-dolphin/bootstrap@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestProtectedWorkflowUsesOnePullAndOneBootstrapRun(t *testing.T) {
	root := repoRootForCICrossPlatformSmokeGuard(t)
	runnerTemp := t.TempDir()
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "candidate"), 0o700); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "docker.log")
	fakeDocker := writeFakeDocker(t)
	command := exec.Command("bash", "-c", protectedWorkflowRunScript(t, root))
	command.Dir = root
	command.Env = append(os.Environ(),
		"PATH="+filepath.Dir(fakeDocker)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"FAKE_DOCKER_LOG="+logPath,
		"RUNNER_TEMP="+runnerTemp,
		"GITHUB_WORKSPACE="+workspace,
		"GATE_EVENT_REPOSITORY=example/repository",
		"GATE_EVENT_REF=refs/heads/candidate",
		"GATE_EVENT_SHA="+strings.Repeat("a", 40),
		"SUPER_DOLPHIN_GATE_BOOTSTRAP_IMAGE="+testBootstrapImage,
		"SUPER_DOLPHIN_GATE_AUTHORITY_BUNDLE_B64=authority-is-not-shell-input",
		"SUPER_DOLPHIN_GATE_WORKFLOW_OIDC_AUDIENCE=super-dolphin-gate-workflow",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("protected workflow bootstrap orchestration error = %v\n%s", err, output)
	}
	lines := dockerLogLines(t, logPath)
	if len(lines) != 2 || lines[0] != "pull --platform=linux/amd64 "+testBootstrapImage {
		t.Fatalf("docker invocations = %#v", lines)
	}
	assertProtectedWorkflowRunArguments(t, strings.Fields(lines[1]), workspace, runnerTemp)
}

func TestCITruthImageScriptDelegatesToTrustedCoordinator(t *testing.T) {
	root := repoRootForCICrossPlatformSmokeGuard(t)
	logPath, fakeCoordinator := writeFakeCoordinator(t)
	path := filepath.Dir(fakeCoordinator) + string(os.PathListSeparator) + os.Getenv("PATH")
	command := exec.Command("bash", filepath.Join(root, "scripts", "ci_truth_image_gate.sh"))
	command.Dir = root
	command.Env = ciTruthImageEnv(path, logPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("CI script workflow-host delegation error=%v output=%s", err, output)
	}
	if got := coordinatorLogLines(t, logPath); !slices.Equal(got, []string{"workflow-host"}) {
		t.Fatalf("CI script default coordinator argv = %#v", got)
	}
}

func TestCITruthImageScriptBindsNonReleaseProfilesToActiveStagedTree(t *testing.T) {
	root := newCITruthImageScriptRepository(t, true)
	scriptPath := filepath.Join(repoRootForCICrossPlatformSmokeGuard(t), "scripts", "ci_truth_image_gate.sh")
	logPath, fakeCoordinator := writeFakeCoordinator(t)
	path := filepath.Dir(fakeCoordinator) + string(os.PathListSeparator) + os.Getenv("PATH")
	objectFormat := gitOutput(t, root, "rev-parse", "--show-object-format")
	commitSHA := gitOutput(t, root, "rev-parse", "HEAD")
	stagedTree := gitOutput(t, root, "write-tree")
	commitTree := gitOutput(t, root, "rev-parse", commitSHA+"^{tree}")
	if stagedTree == commitTree {
		t.Fatal("fixture must contain a staged tree that differs from HEAD")
	}
	for _, profile := range []string{"local-fast", "push", "remote-required"} {
		t.Run(profile, func(t *testing.T) {
			if err := os.WriteFile(logPath, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("bash", scriptPath, profile)
			command.Dir = root
			command.Env = ciTruthImageEnv(path, logPath)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("CI script profile %s delegation error=%v output=%s", profile, err, output)
			}
			want := "submit --wait --profile " + profile + " --object-format " + objectFormat +
				" --tree " + stagedTree + " --parent " + commitSHA + " --source-tree " + stagedTree
			if got := coordinatorLogLines(t, logPath); !slices.Equal(got, []string{want}) {
				t.Fatalf("CI script profile %s coordinator argv = %#v, want %q", profile, got, want)
			}
		})
	}
}

func TestCITruthImageScriptBindsReleaseToCurrentCommit(t *testing.T) {
	root := newCITruthImageScriptRepository(t, false)
	appRoot := repoRootForCICrossPlatformSmokeGuard(t)
	logPath, fakeCoordinator := writeFakeCoordinator(t)
	path := filepath.Dir(fakeCoordinator) + string(os.PathListSeparator) + os.Getenv("PATH")
	objectFormat := gitOutput(t, root, "rev-parse", "--show-object-format")
	commitSHA := gitOutput(t, root, "rev-parse", "HEAD")
	commitTree := gitOutput(t, root, "rev-parse", commitSHA+"^{tree}")
	command := exec.Command("bash", filepath.Join(appRoot, "scripts", "ci_truth_image_gate.sh"), "release")
	command.Dir = root
	command.Env = ciTruthImageEnv(path, logPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("CI script release delegation error=%v output=%s", err, output)
	}
	want := "_production-launcher submit --wait --profile release --object-format " + objectFormat +
		" --commit " + commitSHA + " --source-tree " + commitTree
	if got := coordinatorLogLines(t, logPath); !slices.Equal(got, []string{want}) {
		t.Fatalf("CI script release coordinator argv = %#v, want %q", got, want)
	}
	coordinator := readGuardFile(t, filepath.Join(appRoot, "cmd", "super-dolphin-gate", "coordinator_cli.go"))
	const releaseSubmitAdapter = "runProductionReleaseSubmitPlanWithWaitConnector"
	if !strings.Contains(coordinator, "func "+releaseSubmitAdapter+"(") {
		t.Fatalf("release authority adapter %q is missing", releaseSubmitAdapter)
	}
	if !strings.Contains(coordinator, "return "+releaseSubmitAdapter+"(plan, stdout, config, repositoryRoot, connector, waitForTerminal)") {
		t.Fatalf("release authority adapter %q is not wired to the production launcher", releaseSubmitAdapter)
	}
}

func TestCITruthImageScriptRejectsReleaseWithStagedChanges(t *testing.T) {
	root := newCITruthImageScriptRepository(t, true)
	scriptPath := filepath.Join(repoRootForCICrossPlatformSmokeGuard(t), "scripts", "ci_truth_image_gate.sh")
	logPath, fakeCoordinator := writeFakeCoordinator(t)
	command := exec.Command("bash", scriptPath, "release")
	command.Dir = root
	command.Env = ciTruthImageEnv(filepath.Dir(fakeCoordinator)+string(os.PathListSeparator)+os.Getenv("PATH"), logPath)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "release requires the staged index to match HEAD") {
		t.Fatalf("staged release result error=%v output=%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("release must fail before coordinator delegation, log stat error=%v", err)
	}
}

func newCITruthImageScriptRepository(t *testing.T, stageChange bool) string {
	t.Helper()
	root := t.TempDir()
	gitOutput(t, root, "init")
	gitOutput(t, root, "config", "user.email", "ci@example.invalid")
	gitOutput(t, root, "config", "user.name", "CI Truth Image Test")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, root, "add", "tracked.txt")
	gitOutput(t, root, "commit", "-m", "initial")
	if stageChange {
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("staged candidate\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitOutput(t, root, "add", "tracked.txt")
	}
	return root
}

func TestCITruthImageScriptFailsClosedWithoutTrustedCoordinator(t *testing.T) {
	root := repoRootForCICrossPlatformSmokeGuard(t)
	command := exec.Command("bash", filepath.Join(root, "scripts", "ci_truth_image_gate.sh"))
	command.Dir = root
	command.Env = ciTruthImageEnv(t.TempDir(), filepath.Join(t.TempDir(), "unused.log"))
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "trusted super-dolphin-gate CLI is not installed") {
		t.Fatalf("missing trusted coordinator result error=%v output=%s", err, output)
	}
}

func TestGitHooksREADMEDeclaresThinHookEntrypoints(t *testing.T) {
	root := repoRootForCICrossPlatformSmokeGuard(t)
	readme := readGuardFile(t, filepath.Join(root, ".githooks", "README.md"))
	for _, contract := range []struct {
		name           string
		hookPath       string
		hookEntrypoint string
		documentation  string
	}{
		{
			name:           "pre-commit closure",
			hookPath:       ".githooks/pre-commit",
			hookEntrypoint: `closure check --tree "$staged_tree"`,
			documentation:  "`super-dolphin-gate closure check --tree <tree>`",
		},
		{
			name:           "pre-commit coordinator",
			hookPath:       ".githooks/pre-commit",
			hookEntrypoint: `hook pre-commit --tree "$staged_tree"`,
			documentation:  "`super-dolphin-gate hook pre-commit --tree <tree>`",
		},
		{
			name:           "pre-commit coordinator wait",
			hookPath:       ".githooks/pre-commit",
			hookEntrypoint: `wait --job "$job_id" --tree "$staged_tree"`,
			documentation:  "`super-dolphin-gate wait --job <job-id> --tree <tree>`",
		},
		{
			name:           "pre-push coordinator",
			hookPath:       ".githooks/pre-push",
			hookEntrypoint: "hook pre-push",
			documentation:  "`super-dolphin-gate hook pre-push <remote-name> <remote-url>`",
		},
		{
			name:           "commit title guard",
			hookPath:       ".githooks/commit-msg",
			hookEntrypoint: "guard_commit_titles.sh --message",
			documentation:  "`scripts/guard_commit_titles.sh --message <message-file>`",
		},
		{
			name:           "commit fix-test guard",
			hookPath:       ".githooks/commit-msg",
			hookEntrypoint: "guard_fix_commits_have_tests.sh --cached",
			documentation:  "`scripts/guard_fix_commits_have_tests.sh --cached <message-file>`",
		},
	} {
		t.Run(contract.name, func(t *testing.T) {
			if !strings.Contains(readme, contract.documentation) {
				t.Fatalf("README is missing documented entrypoint %q", contract.documentation)
			}
			hook := readGuardFile(t, filepath.Join(root, contract.hookPath))
			if !strings.Contains(hook, contract.hookEntrypoint) {
				t.Fatalf("%s is missing entrypoint %q", contract.hookPath, contract.hookEntrypoint)
			}
		})
	}
	if !strings.Contains(readme, "thin hook 不直接运行 gofmt、go vet、包测试、前端检查、codemap/project-map 刷新或 AI-maintenance plan") {
		t.Fatal("README must state that thin hooks do not run the retired direct gates")
	}
	if !strings.Contains(readme, "这些门禁不支持绕过") {
		t.Fatal("README must state that thin-hook gates do not support bypasses")
	}
	if strings.Contains(readme, "--no-verify") {
		t.Fatal("README must not document hook bypasses")
	}
	preCommit := readGuardFile(t, filepath.Join(root, ".githooks", "pre-commit"))
	for _, required := range []string{"gate_output_file=$(mktemp", "hook_rc\" -ne 13", "^job-[0-9a-f]{32}$", `wait --job "$job_id" --tree "$staged_tree"`} {
		if !strings.Contains(preCommit, required) {
			t.Fatalf("pre-commit does not synchronously wait for queued jobs: missing %q", required)
		}
	}
}

func assertProtectedWorkflowRunArguments(t *testing.T, arguments []string, workspace, runnerTemp string) {
	t.Helper()
	runtimeRoot := mountSourceForTarget(t, arguments, "")
	if !strings.HasPrefix(runtimeRoot, filepath.Join(runnerTemp, "super-dolphin-gate-runtime.")) {
		t.Fatalf("shared runtime mount source = %q", runtimeRoot)
	}
	expected := []string{
		"run", "--rm",
		"--cpus=4",
		"--memory=8g",
		"--mount", "type=bind,source=/var/run/docker.sock,target=/var/run/docker.sock",
		"--mount", "type=bind,source=" + filepath.Join(workspace, "candidate") + ",target=/workspace/super-dolphin-checkout,readonly",
		"--mount", "type=bind,source=" + runtimeRoot + ",target=" + runtimeRoot,
		"--workdir", "/workspace/super-dolphin-checkout",
		"--env", "SUPER_DOLPHIN_GATE_AUTHORITY_BUNDLE_B64",
		"--env", "SUPER_DOLPHIN_GATE_WORKFLOW_RUNTIME_ROOT=" + runtimeRoot,
		"--env", "SUPER_DOLPHIN_GATE_WORKFLOW_OIDC_AUDIENCE",
		"--env", "ACTIONS_ID_TOKEN_REQUEST_URL",
		"--env", "ACTIONS_ID_TOKEN_REQUEST_TOKEN",
		testBootstrapImage, "workflow-host",
		"--repository-root", "/workspace/super-dolphin-checkout",
		"--event-repository", "example/repository",
		"--event-ref", "refs/heads/candidate",
		"--event-sha", strings.Repeat("a", 40),
	}
	if !slices.Equal(arguments, expected) {
		t.Fatalf("docker run argv = %#v\nwant %#v", arguments, expected)
	}
}

func protectedWorkflowRunScript(t *testing.T, root string) string {
	t.Helper()
	workflow := readGuardFile(t, filepath.Join(root, ".github", "workflows", "ci.yml"))
	marker := "        run: |\n"
	start := strings.Index(workflow, marker)
	if start < 0 {
		t.Fatal("protected workflow run script was not found")
	}
	var lines []string
	for line := range strings.Lines(workflow[start+len(marker):]) {
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			lines = append(lines, "")
			continue
		}
		if !strings.HasPrefix(line, "          ") {
			break
		}
		lines = append(lines, strings.TrimPrefix(line, "          "))
	}
	return strings.Join(lines, "\n")
}

func mountSourceForTarget(t *testing.T, arguments []string, target string) string {
	t.Helper()
	for index := range arguments {
		if arguments[index] != "--mount" || index+1 >= len(arguments) {
			continue
		}
		parts := strings.Split(arguments[index+1], ",")
		values := make(map[string]string, len(parts))
		for _, part := range parts {
			key, value, ok := strings.Cut(part, "=")
			if ok {
				values[key] = value
			}
		}
		if target == "" && values["source"] == values["target"] && strings.Contains(values["source"], "super-dolphin-gate-runtime.") {
			return values["source"]
		}
		if values["target"] == target {
			return values["source"]
		}
	}
	t.Fatalf("mount target %q was not found in %#v", target, arguments)
	return ""
}

func writeFakeDocker(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> \"$FAKE_DOCKER_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func dockerLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func writeFakeCoordinator(t *testing.T) (logPath, binaryPath string) {
	t.Helper()
	dir := t.TempDir()
	logPath = filepath.Join(dir, "coordinator.log")
	binaryPath = filepath.Join(dir, "super-dolphin-gate")
	script := "#!/usr/bin/env bash\nset -euo pipefail\nprintf '%s\\n' \"$*\" >> \"${FAKE_COORDINATOR_LOG:?}\"\n"
	if err := os.WriteFile(binaryPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return logPath, binaryPath
}

func ciTruthImageEnv(path, logPath string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "PATH=") && !strings.HasPrefix(item, "FAKE_COORDINATOR_LOG=") {
			environment = append(environment, item)
		}
	}
	return append(environment, "PATH="+path, "FAKE_COORDINATOR_LOG="+logPath)
}

func coordinatorLogLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error=%v output=%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
