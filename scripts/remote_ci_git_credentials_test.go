package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	remoteCITestUsername   = "fixture-user"
	remoteCITestGHCRToken  = "fixture-ghcr-token"
	remoteCITestAgentToken = "sdci1_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

// TestRemoteCIGitCredentialLauncherInjectsSecretsOnlyIntoGitProcess 验证凭据从外部存储进入当前 Git 进程链，且不会出现在输出或 Git 配置中。
func TestRemoteCIGitCredentialLauncherInjectsSecretsOnlyIntoGitProcess(t *testing.T) {
	fixture := newRemoteCIGitCredentialFixture(t, true)
	output, err := fixture.run(t, nil, "commit", "--allow-empty", "-m", "测试远程 CI 凭据启动器")
	if err != nil {
		t.Fatalf("credential-aware commit failed: %v\n%s", err, output)
	}
	assertRemoteCICredentialSecretsAbsent(t, output)
	if marker, err := os.ReadFile(fixture.marker); err != nil || strings.TrimSpace(string(marker)) != "ok" {
		t.Fatalf("hook credential marker = %q, %v", marker, err)
	}
	config := remoteCITestGitOutput(t, fixture.root, "config", "--local", "--list")
	assertRemoteCICredentialSecretsAbsent(t, []byte(config))
}

// TestRemoteCIGitCredentialLauncherPreservesCompleteCallerCredentials 验证完整调用方环境无需访问 GitHub CLI 或重新签发 token。
func TestRemoteCIGitCredentialLauncherPreservesCompleteCallerCredentials(t *testing.T) {
	fixture := newRemoteCIGitCredentialFixture(t, false)
	environment := map[string]string{
		"SUPER_DOLPHIN_CI_AGENT_TOKEN":   remoteCITestAgentToken,
		"SUPER_DOLPHIN_CI_GHCR_USERNAME": remoteCITestUsername,
		"SUPER_DOLPHIN_CI_GHCR_TOKEN":    remoteCITestGHCRToken,
	}
	output, err := fixture.run(t, environment, "commit", "--allow-empty", "-m", "测试复用调用方凭据")
	if err != nil {
		t.Fatalf("caller-owned credential commit failed: %v\n%s", err, output)
	}
	assertRemoteCICredentialSecretsAbsent(t, output)
}

// TestRemoteCIGitCredentialLauncherRejectsPartialCredentialPair 验证半组 GHCR 凭据在执行 Git 前 fail-fast。
func TestRemoteCIGitCredentialLauncherRejectsPartialCredentialPair(t *testing.T) {
	fixture := newRemoteCIGitCredentialFixture(t, true)
	environment := map[string]string{
		"SUPER_DOLPHIN_CI_AGENT_TOKEN":   remoteCITestAgentToken,
		"SUPER_DOLPHIN_CI_GHCR_USERNAME": remoteCITestUsername,
	}
	output, err := fixture.run(t, environment, "commit", "--allow-empty", "-m", "不得创建的提交")
	if err == nil || !strings.Contains(string(output), "GHCR username and token must be supplied together") {
		t.Fatalf("partial credential result error=%v output=%s", err, output)
	}
	if got := remoteCITestGitOutput(t, fixture.root, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("partial credential unexpectedly executed Git commit; commit count=%s", got)
	}
}

// TestRemoteCIGitCredentialLauncherRejectsMalformedBootstrap 验证伪造签发响应不能把 GitHub 凭据带入 Git。
func TestRemoteCIGitCredentialLauncherRejectsMalformedBootstrap(t *testing.T) {
	fixture := newRemoteCIGitCredentialFixture(t, true)
	writeRemoteCITestFile(t, filepath.Join(fixture.root, ".fixture-gate"), "#!/usr/bin/env bash\nprintf '{}\\n'\nexit 1\n", 0o755)
	output, err := fixture.run(t, nil, "commit", "--allow-empty", "-m", "不得创建的伪造签发提交")
	if err == nil || !strings.Contains(string(output), "invalid agent-token bootstrap") {
		t.Fatalf("malformed bootstrap result error=%v output=%s", err, output)
	}
	if got := remoteCITestGitOutput(t, fixture.root, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("malformed bootstrap unexpectedly executed Git commit; commit count=%s", got)
	}
}

// TestRemoteCIGitCredentialLauncherRejectsUnavailableGitHubIdentity 验证钥匙串身份读取失败时绝不执行 Git。
func TestRemoteCIGitCredentialLauncherRejectsUnavailableGitHubIdentity(t *testing.T) {
	fixture := newRemoteCIGitCredentialFixture(t, false)
	environment := map[string]string{"SUPER_DOLPHIN_CI_AGENT_TOKEN": remoteCITestAgentToken}
	output, err := fixture.run(t, environment, "commit", "--allow-empty", "-m", "不得创建的身份失败提交")
	if err == nil || !strings.Contains(string(output), "GitHub CLI authentication status is unavailable") {
		t.Fatalf("unavailable GitHub identity result error=%v output=%s", err, output)
	}
	if got := remoteCITestGitOutput(t, fixture.root, "rev-list", "--count", "HEAD"); got != "1" {
		t.Fatalf("unavailable GitHub identity unexpectedly executed Git commit; commit count=%s", got)
	}
}

// TestRemoteCIInitInstallsCredentialAwareGitAlias 锁定跨仓库启动器的无密钥 Git 别名。
func TestRemoteCIInitInstallsCredentialAwareGitAlias(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(scriptRepoRoot(t), "scripts", "init_remote_ci_local.sh"))
	if err != nil {
		t.Fatal(err)
	}
	want := `git config --local alias.remote-ci '!./scripts/git_with_remote_ci_credentials.sh --repository . --'`
	if !strings.Contains(string(contents), want) {
		t.Fatalf("remote CI initializer must install %q", want)
	}
}

// TestRemoteCIGitCredentialLauncherKeepsCredentialBoundaryInMemory 锁定启动器只经环境执行 Git，不把密钥写入配置或 argv。
func TestRemoteCIGitCredentialLauncherKeepsCredentialBoundaryInMemory(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(scriptRepoRoot(t), "scripts", "git_with_remote_ci_credentials.sh"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	for _, required := range []string{
		"set +x",
		"gh auth status --active --hostname github.com --json hosts",
		"gh auth token --hostname github.com",
		"export SUPER_DOLPHIN_CI_AGENT_TOKEN",
		"export SUPER_DOLPHIN_CI_GHCR_USERNAME",
		"export SUPER_DOLPHIN_CI_GHCR_TOKEN",
		`exec git -C "$repo_root" "$@"`,
	} {
		if !strings.Contains(source, required) {
			t.Errorf("credential launcher is missing boundary %q", required)
		}
	}
	for _, forbidden := range []string{
		"git config --global",
		"git config --local super-dolphin.agent-token",
		"security add-generic-password",
		"--show-token",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("credential launcher contains persistent or observable secret path %q", forbidden)
		}
	}
}

type remoteCIGitCredentialFixture struct {
	root   string
	bin    string
	marker string
	script string
}

func newRemoteCIGitCredentialFixture(t *testing.T, workingGitHubCLI bool) remoteCIGitCredentialFixture {
	t.Helper()
	root := t.TempDir()
	owner := t.TempDir()
	bin := filepath.Join(root, "fixture-bin")
	marker := filepath.Join(root, "credential-marker")
	for _, directory := range []string{bin, filepath.Join(root, ".git", "hooks"), filepath.Join(owner, ".githooks"), filepath.Join(owner, "scripts")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	remoteCITestGit(t, root, "init", "-q")
	remoteCITestGit(t, root, "config", "user.name", "Credential Test")
	remoteCITestGit(t, root, "config", "user.email", "credential@example.invalid")
	writeRemoteCITestFile(t, filepath.Join(root, "README.md"), "fixture\n", 0o644)
	remoteCITestGit(t, root, "add", "README.md")
	remoteCITestGit(t, root, "commit", "-q", "-m", "初始化凭据测试仓库")
	writeRemoteCITestLauncher(t, owner)
	writeRemoteCITestGate(t, root)
	writeRemoteCITestHook(t, root, marker)
	writeRemoteCITestGitHubCLI(t, bin, workingGitHubCLI)
	script := filepath.Join(owner, "scripts", "git_with_remote_ci_credentials.sh")
	sourceScript := filepath.Join(scriptRepoRoot(t), "scripts", "git_with_remote_ci_credentials.sh")
	contents, err := os.ReadFile(sourceScript)
	if err != nil {
		t.Fatal(err)
	}
	writeRemoteCITestFile(t, script, string(contents), 0o755)
	return remoteCIGitCredentialFixture{root: root, bin: bin, marker: marker, script: script}
}

func (fixture remoteCIGitCredentialFixture) run(t *testing.T, overrides map[string]string, arguments ...string) ([]byte, error) {
	t.Helper()
	command := exec.Command("bash", append([]string{fixture.script, "--repository", fixture.root, "--"}, arguments...)...)
	command.Env = remoteCITestEnvironment(fixture.bin, overrides)
	return command.CombinedOutput()
}

func remoteCITestEnvironment(bin string, overrides map[string]string) []string {
	values := map[string]string{"PATH": bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok && key != "SUPER_DOLPHIN_CI_AGENT_TOKEN" && key != "SUPER_DOLPHIN_CI_GHCR_USERNAME" && key != "SUPER_DOLPHIN_CI_GHCR_TOKEN" && key != "PATH" {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	environment := make([]string, 0, len(values))
	for key, value := range values {
		environment = append(environment, key+"="+value)
	}
	return environment
}

func writeRemoteCITestLauncher(t *testing.T, owner string) {
	t.Helper()
	body := "#!/usr/bin/env bash\ntrusted_gate_launcher() { printf '%s\\n' \"$1/.fixture-gate\"; }\n"
	writeRemoteCITestFile(t, filepath.Join(owner, ".githooks", "trusted-gate-launcher.sh"), body, 0o644)
}

func writeRemoteCITestGate(t *testing.T, root string) {
	t.Helper()
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(remoteCITestAgentToken)))
	payload, err := json.Marshal(map[string]any{
		"schema_version": 1, "kind": "remote_ci_agent_token_bootstrap",
		"agent_token": remoteCITestAgentToken, "agent_token_digest": digest,
		"issued": true, "retry_required": true, "execute_ci": false,
		"reuse_environment_name":  "SUPER_DOLPHIN_CI_AGENT_TOKEN",
		"reuse_environment_value": remoteCITestAgentToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	body := "#!/usr/bin/env bash\nprintf '%s\\n' '" + string(payload) + "'\nexit 1\n"
	writeRemoteCITestFile(t, filepath.Join(root, ".fixture-gate"), body, 0o755)
}

func writeRemoteCITestHook(t *testing.T, root, marker string) {
	t.Helper()
	body := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
[[ "$SUPER_DOLPHIN_CI_AGENT_TOKEN" == %q ]]
[[ "$SUPER_DOLPHIN_CI_GHCR_USERNAME" == %q ]]
[[ "$SUPER_DOLPHIN_CI_GHCR_TOKEN" == %q ]]
printf 'ok\n' >%q
`, remoteCITestAgentToken, remoteCITestUsername, remoteCITestGHCRToken, marker)
	writeRemoteCITestFile(t, filepath.Join(root, ".git", "hooks", "pre-commit"), body, 0o755)
}

func writeRemoteCITestGitHubCLI(t *testing.T, bin string, working bool) {
	t.Helper()
	body := "#!/usr/bin/env bash\nexit 97\n"
	if working {
		body = `#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
	"auth status") printf 'fixture-user\n' ;;
  "auth token") printf 'fixture-ghcr-token\n' ;;
  *) exit 98 ;;
esac
`
	}
	writeRemoteCITestFile(t, filepath.Join(bin, "gh"), body, 0o755)
}

func writeRemoteCITestFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func remoteCITestGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func remoteCITestGitOutput(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output))
}

func assertRemoteCICredentialSecretsAbsent(t *testing.T, output []byte) {
	t.Helper()
	for _, secret := range []string{remoteCITestGHCRToken, remoteCITestAgentToken} {
		if strings.Contains(string(output), secret) {
			t.Fatalf("credential secret leaked into observable output")
		}
	}
}
