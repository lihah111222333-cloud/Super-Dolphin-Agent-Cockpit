package archtest

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCITruthImageScriptBindsNonReleaseProfilesToActiveStagedTree(t *testing.T) {
	root := newCITruthImageScriptRepository(t, true)
	scriptPath := filepath.Join(root, "scripts", "ci_truth_image_gate.sh")
	logPath, fakeCoordinator := writeFakeCoordinator(t, root)
	configureCITruthImageLauncher(t, root, fakeCoordinator)
	path := filepath.Dir(fakeCoordinator) + string(os.PathListSeparator) + os.Getenv("PATH")
	commitSHA := gitOutput(t, root, "rev-parse", "HEAD")
	stagedTree := gitOutput(t, root, "write-tree")
	repositoryRoot := gitOutput(t, root, "rev-parse", "--show-toplevel")
	remoteConfig := gitOutput(t, root, "config", "--local", "--get", "super-dolphin.remote.config")
	remoteLedger := gitOutput(t, root, "config", "--local", "--get", "super-dolphin.remote.ledger")
	for _, profile := range []string{"local-fast", "push"} {
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
			want := ""
			switch profile {
			case "local-fast":
				want = "remote hook pre-commit --config " + remoteConfig + " --ledger " + remoteLedger +
					" --repository " + repositoryRoot + " --tree " + stagedTree + " --parent " + commitSHA
			case "push":
				want = "remote hook pre-push --config " + remoteConfig + " --ledger " + remoteLedger +
					" --repository " + repositoryRoot + " origin https://example.invalid/super-dolphin.git"
			}
			if got := coordinatorLogLines(t, logPath); !slices.Equal(got, []string{want}) {
				t.Fatalf("CI script profile %s coordinator argv = %#v, want %q", profile, got, want)
			}
		})
	}
}

func TestCITruthImageScriptBindsReleaseToCurrentCommit(t *testing.T) {
	root := newCITruthImageScriptRepository(t, false)
	logPath, fakeCoordinator := writeFakeCoordinator(t, root)
	configureCITruthImageLauncher(t, root, fakeCoordinator)
	path := filepath.Dir(fakeCoordinator) + string(os.PathListSeparator) + os.Getenv("PATH")
	commitSHA := gitOutput(t, root, "rev-parse", "HEAD")
	repositoryRoot := gitOutput(t, root, "rev-parse", "--show-toplevel")
	remoteConfig := gitOutput(t, root, "config", "--local", "--get", "super-dolphin.remote.config")
	remoteLedger := gitOutput(t, root, "config", "--local", "--get", "super-dolphin.remote.ledger")
	command := exec.Command("bash", filepath.Join(root, "scripts", "ci_truth_image_gate.sh"), "release")
	command.Dir = root
	command.Env = ciTruthImageEnv(path, logPath)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("CI script release delegation error=%v output=%s", err, output)
	}
	want := "remote run --config " + remoteConfig + " --ledger " + remoteLedger +
		" --repository " + repositoryRoot + " --scenario full --profile release --entrypoint release --commit " + commitSHA
	if got := coordinatorLogLines(t, logPath); !slices.Equal(got, []string{want}) {
		t.Fatalf("CI script release coordinator argv = %#v, want %q", got, want)
	}
}

func TestCITruthImageScriptContainsOnlyLiveRemoteEntrypoints(t *testing.T) {
	script := readCITruthImageFile(t, filepath.Join(ciTruthImageRepoRoot(t), "scripts", "ci_truth_image_gate.sh"))
	for _, required := range []string{"remote hook pre-commit", "remote hook pre-push", "remote run"} {
		if !strings.Contains(script, required) {
			t.Fatalf("CI truth-image adapter is missing live entrypoint %q", required)
		}
	}
	for _, forbidden := range []string{"submit", "_production-launcher", "workflow-host", "remote-required"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("CI truth-image adapter retains retired entrypoint %q", forbidden)
		}
	}
}

func TestCITruthImageScriptRejectsRetiredReleaseGrantArguments(t *testing.T) {
	root := newCITruthImageScriptRepository(t, false)
	logPath, fakeCoordinator := writeFakeCoordinator(t, root)
	configureCITruthImageLauncher(t, root, fakeCoordinator)
	path := filepath.Dir(fakeCoordinator) + string(os.PathListSeparator) + os.Getenv("PATH")
	grantPath := filepath.Join(t.TempDir(), "grant.json")
	command := exec.Command("bash", filepath.Join(root, "scripts", "ci_truth_image_gate.sh"), "release",
		"--release-grant-output", grantPath)
	command.Dir = root
	command.Env = ciTruthImageEnv(path, logPath)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "usage: ci_truth_image_gate.sh [local-fast|push|release]") {
		t.Fatalf("CI script accepted retired release grant arguments: error=%v output=%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("retired grant arguments must fail before coordinator delegation, log stat error=%v", err)
	}
}

func TestCITruthImageScriptUsesHookConfigEnvironmentOverrides(t *testing.T) {
	root := newCITruthImageScriptRepository(t, false)
	logPath, fakeCoordinator := writeFakeCoordinator(t, root)
	configureCITruthImageLauncher(t, root, fakeCoordinator)
	commitSHA := gitOutput(t, root, "rev-parse", "HEAD")
	stagedTree := gitOutput(t, root, "write-tree")
	repositoryRoot := gitOutput(t, root, "rev-parse", "--show-toplevel")
	envConfig := filepath.Join(root, "env-remote-ci.json")
	envLedger := filepath.Join(root, "env-duration-ledger.sqlite")
	command := exec.Command("bash", filepath.Join(root, "scripts", "ci_truth_image_gate.sh"), "local-fast")
	command.Dir = root
	command.Env = ciTruthImageEnvWithAuthority(filepath.Dir(fakeCoordinator)+string(os.PathListSeparator)+os.Getenv("PATH"), logPath, envConfig, envLedger)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("CI script environment authority delegation error=%v output=%s", err, output)
	}
	want := "remote hook pre-commit --config " + envConfig + " --ledger " + envLedger +
		" --repository " + repositoryRoot + " --tree " + stagedTree + " --parent " + commitSHA
	if got := coordinatorLogLines(t, logPath); !slices.Equal(got, []string{want}) {
		t.Fatalf("CI script ignored hook-compatible config/ledger environment: got %#v want %q", got, want)
	}
}

func TestCITruthImageScriptUsesRealRemoteCLIHandshake(t *testing.T) {
	root := newCITruthImageScriptRepository(t, false)
	logPath, gateBinary := writeHandshakeCoordinator(t, root)
	configureCITruthImageLauncher(t, root, gateBinary)
	for _, testCase := range []struct {
		name     string
		token    string
		required []string
	}{
		{name: "guidance", required: []string{"remote_ci_agent_token_guidance", "remote CI agent token issuance required", "remote", "run", "--agent-token=issue"}},
		{name: "issue", token: "issue", required: []string{"remote_ci_agent_token_bootstrap", "remote CI agent token bootstrap issued", "remote", "run"}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			command := exec.Command("bash", filepath.Join(root, "scripts", "ci_truth_image_gate.sh"), "local-fast")
			command.Dir = root
			environment := ciTruthImageEnvWithoutToken(filepath.Dir(gateBinary) + string(os.PathListSeparator) + os.Getenv("PATH"))
			environment = append(environment, "FAKE_COORDINATOR_LOG="+logPath)
			if testCase.token != "" {
				environment = append(environment, "SUPER_DOLPHIN_CI_AGENT_TOKEN="+testCase.token)
			}
			command.Env = environment
			output, err := command.CombinedOutput()
			if err == nil {
				t.Fatalf("real remote CLI handshake unexpectedly executed CI: %s", output)
			}
			text := string(output)
			for _, required := range testCase.required {
				if !strings.Contains(text, required) {
					t.Fatalf("real CLI handshake output missing %q: %s", required, text)
				}
			}
			for _, retired := range []string{"submit --wait", "_production-launcher", "workflow-host"} {
				if strings.Contains(text, retired) {
					t.Fatalf("real CLI handshake reached retired launcher path %q: %s", retired, text)
				}
			}
		})
	}
}

func TestCITruthImageScriptRejectsReleaseWithStagedChanges(t *testing.T) {
	root := newCITruthImageScriptRepository(t, true)
	scriptPath := filepath.Join(root, "scripts", "ci_truth_image_gate.sh")
	logPath, fakeCoordinator := writeFakeCoordinator(t, root)
	configureCITruthImageLauncher(t, root, fakeCoordinator)
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
	gitOutput(t, root, "config", "--local", "super-dolphin.remote.config", filepath.Join(root, "remote-ci.json"))
	gitOutput(t, root, "config", "--local", "super-dolphin.remote.ledger", filepath.Join(root, "remote-ci.baseline-state.sqlite"))
	launcherPath := filepath.Join(ciTruthImageRepoRoot(t), ".githooks", "trusted-gate-launcher.sh")
	launcher, err := os.ReadFile(launcherPath)
	if err != nil {
		t.Fatalf("read trusted gate launcher fixture: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, ".githooks"), 0o700); err != nil {
		t.Fatalf("create trusted gate launcher fixture directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ".githooks", "trusted-gate-launcher.sh"), launcher, 0o700); err != nil {
		t.Fatalf("write trusted gate launcher fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitOutput(t, root, "add", "tracked.txt")
	gitOutput(t, root, "commit", "-m", "initial")
	branchName := gitOutput(t, root, "symbolic-ref", "--short", "HEAD")
	gitOutput(t, root, "remote", "add", "origin", "https://example.invalid/super-dolphin.git")
	gitOutput(t, root, "config", "--local", "branch."+branchName+".merge", "refs/heads/"+branchName)
	if stageChange {
		if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("staged candidate\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitOutput(t, root, "add", "tracked.txt")
	}
	copyCITruthImageScriptFixtures(t, root)
	return root
}

func copyCITruthImageScriptFixtures(t *testing.T, root string) {
	t.Helper()
	appRoot := ciTruthImageRepoRoot(t)
	for _, path := range []string{"scripts/ci_truth_image_gate.sh", ".githooks/trusted-gate-launcher.sh"} {
		contents, err := os.ReadFile(filepath.Join(appRoot, path))
		if err != nil {
			t.Fatal(err)
		}
		destination := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, contents, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCITruthImageScriptFailsClosedWithoutTrustedCoordinator(t *testing.T) {
	root := newCITruthImageScriptRepository(t, false)
	logPath, fakeCoordinator := writeFakeCoordinator(t, root)
	command := exec.Command("bash", filepath.Join(root, "scripts", "ci_truth_image_gate.sh"))
	command.Dir = root
	command.Env = ciTruthImageEnv(filepath.Dir(fakeCoordinator)+string(os.PathListSeparator)+os.Getenv("PATH"), logPath)
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "trusted super-dolphin-gate launcher is unavailable") {
		t.Fatalf("missing trusted coordinator result error=%v output=%s", err, output)
	}
	if _, err := os.Stat(logPath); !os.IsNotExist(err) {
		t.Fatalf("malicious PATH coordinator was executed: %v", err)
	}
}

func writeFakeCoordinator(t *testing.T, root string) (logPath, binaryPath string) {
	t.Helper()
	logPath = filepath.Join(t.TempDir(), "coordinator.log")
	script := []byte("#!/usr/bin/env bash\nset -euo pipefail\nif [[ \"${1:-}\" == launcher ]]; then exit 0; fi\nprintf '%s\\n' \"$*\" >> \"${FAKE_COORDINATOR_LOG:?}\"\n")
	binaryPath = writeCITruthImageLauncher(t, root, script)
	return logPath, binaryPath
}

func writeHandshakeCoordinator(t *testing.T, root string) (logPath, binaryPath string) {
	t.Helper()
	tempRoot := t.TempDir()
	logPath = filepath.Join(tempRoot, "coordinator.log")
	realBinary := filepath.Join(tempRoot, "real-super-dolphin-gate")
	build := exec.Command("go", "build", "-o", realBinary, "./cmd/super-dolphin-gate")
	build.Dir = ciTruthImageRepoRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real super-dolphin-gate CLI: %v\n%s", err, output)
	}
	script := fmt.Appendf(nil, "#!/usr/bin/env bash\nset -euo pipefail\nif [[ \"${1:-}\" == launcher ]]; then exit 0; fi\nexec %q \"$@\"\n", realBinary)
	binaryPath = writeCITruthImageLauncher(t, root, script)
	return logPath, binaryPath
}

func writeCITruthImageLauncher(t *testing.T, root string, script []byte) string {
	t.Helper()
	launcherRoot := secureCITruthImageLauncherRoot(t)
	tree := gitOutput(t, root, "write-tree")
	digest := sha256.Sum256(script)
	binaryPath := filepath.Join(launcherRoot, "v1", tree, fmt.Sprintf("%x", digest[:]), "super-dolphin-gate")
	if err := os.MkdirAll(filepath.Dir(binaryPath), 0o700); err != nil {
		t.Fatalf("create fake coordinator directory: %v", err)
	}
	for _, directory := range []string{launcherRoot, filepath.Join(launcherRoot, "v1"), filepath.Join(launcherRoot, "v1", tree), filepath.Dir(binaryPath)} {
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatalf("secure fake coordinator directory: %v", err)
		}
	}
	if err := os.WriteFile(binaryPath, script, 0o500); err != nil {
		t.Fatal(err)
	}
	return binaryPath
}

func secureCITruthImageLauncherRoot(t *testing.T) string {
	t.Helper()
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("resolve current user home for launcher fixture: %v", err)
	}
	if !filepath.IsAbs(homeDir) || filepath.Clean(homeDir) != homeDir {
		t.Fatalf("current user home for launcher fixture is not a canonical absolute path: %q", homeDir)
	}
	homeDir, err = filepath.EvalSymlinks(homeDir)
	if err != nil {
		t.Fatalf("resolve canonical current user home for launcher fixture: %v", err)
	}
	launcherRoot, err := os.MkdirTemp(homeDir, ".super-dolphin-ci-truth-launcher-test-")
	if err != nil {
		t.Fatalf("create private CI truth-image launcher root: %v", err)
	}
	if err := os.Chmod(launcherRoot, 0o700); err != nil {
		_ = os.RemoveAll(launcherRoot)
		t.Fatalf("restrict private CI truth-image launcher root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(launcherRoot) })
	return launcherRoot
}

func configureCITruthImageLauncher(t *testing.T, root, launcher string) {
	t.Helper()
	launcherRoot := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(launcher))))
	gitOutput(t, root, "config", "--local", "superdolphin.gateLauncher", launcher)
	gitOutput(t, root, "config", "--local", "superdolphin.gateLauncherRoot", launcherRoot)
}

func ciTruthImageEnv(path, logPath string) []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "PATH=") && !strings.HasPrefix(item, "FAKE_COORDINATOR_LOG=") && !strings.HasPrefix(item, "SUPER_DOLPHIN_CI_AGENT_TOKEN=") && !strings.HasPrefix(item, "SUPER_DOLPHIN_GATE_REMOTE_CONFIG=") && !strings.HasPrefix(item, "SUPER_DOLPHIN_GATE_LEDGER=") {
			environment = append(environment, item)
		}
	}
	return append(environment, "PATH="+path, "FAKE_COORDINATOR_LOG="+logPath, "SUPER_DOLPHIN_CI_AGENT_TOKEN=test-token")
}

func ciTruthImageEnvWithAuthority(path, logPath, configPath, ledgerPath string) []string {
	environment := ciTruthImageEnv(path, logPath)
	return append(environment, "SUPER_DOLPHIN_GATE_REMOTE_CONFIG="+configPath, "SUPER_DOLPHIN_GATE_LEDGER="+ledgerPath)
}

func ciTruthImageEnvWithoutToken(path string) []string {
	environment := make([]string, 0, len(os.Environ())+1)
	for _, item := range os.Environ() {
		if !strings.HasPrefix(item, "PATH=") && !strings.HasPrefix(item, "FAKE_COORDINATOR_LOG=") && !strings.HasPrefix(item, "SUPER_DOLPHIN_CI_AGENT_TOKEN=") && !strings.HasPrefix(item, "SUPER_DOLPHIN_GATE_REMOTE_CONFIG=") && !strings.HasPrefix(item, "SUPER_DOLPHIN_GATE_LEDGER=") {
			environment = append(environment, item)
		}
	}
	return append(environment, "PATH="+path)
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

func ciTruthImageRepoRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatalf("repository root not found from %s", directory)
		}
		directory = parent
	}
}

func readCITruthImageFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
