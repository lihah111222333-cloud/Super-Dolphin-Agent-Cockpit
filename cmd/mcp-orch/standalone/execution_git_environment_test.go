package standalone

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCLIEnvironmentKeepsGitWorkspaceBoundary 验证父进程 Git 上下文不能改变 provider 的工作仓库。
func TestCLIEnvironmentKeepsGitWorkspaceBoundary(t *testing.T) {
	git := workspaceBoundaryGit(t)
	clean := workspaceBoundarySetupEnv()
	fixture := t.TempDir()
	keys := strings.Fields(workspaceBoundaryGitOutput(t, git, fixture, clean, "rev-parse", "--local-env-vars"))
	if len(keys) == 0 {
		t.Fatal("git returned no local environment keys")
	}
	keys = append(keys, "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0", "GIT_CONFIG_KEY_17", "GIT_CONFIG_VALUE_17")
	t.Run("cwd_selects_repository", func(t *testing.T) {
		repoA, repoB := t.TempDir(), t.TempDir()
		for _, repo := range []string{repoA, repoB} {
			workspaceBoundaryGitOutput(t, git, repo, clean, "init", "--quiet")
		}
		t.Setenv("GIT_DIR", filepath.Join(repoB, ".git"))
		t.Setenv("GIT_WORK_TREE", repoB)
		t.Setenv("GIT_INDEX_FILE", filepath.Join(repoB, ".git", "index"))
		for _, name := range []string{"codex", "claude"} {
			t.Run(name, func(t *testing.T) {
				provider := Provider{Name: name, Home: t.TempDir()}
				root := workspaceBoundaryGitOutput(t, git, repoA, cliEnvironment(provider), "rev-parse", "--show-toplevel")
				got, want := workspaceBoundaryCanonical(t, strings.TrimSpace(root)), workspaceBoundaryCanonical(t, repoA)
				if got != want {
					t.Errorf("provider %s Git root = %q, want cwd repository %q", name, got, want)
				}
			})
		}
	})
	t.Run("local_context_removed_auth_preserved", func(t *testing.T) {
		for _, key := range keys {
			t.Setenv(key, "workspace-boundary-sentinel")
		}
		for _, name := range []string{"codex", "claude"} {
			t.Run(name, func(t *testing.T) {
				assertWorkspaceBoundaryEnvironment(t, Provider{Name: name, Home: t.TempDir()}, keys)
			})
		}
	})
}

func TestClaudeHostDefaultAuthModePreservesDefaultAuthenticationContext(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "parent-boundary-sentinel")
	env := cliEnvironment(Provider{Name: "claude", Home: t.TempDir(), AuthMode: ClaudeAuthHostDefault})
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if key == "CLAUDE_CONFIG_DIR" {
			t.Fatal("host_default Claude auth unexpectedly forced CLAUDE_CONFIG_DIR")
		}
	}
}

// workspaceBoundaryGit 解析并验证本机 Git 绝对路径，缺失时直接失败。
func workspaceBoundaryGit(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("locate git: %v", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatalf("absolute git path: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat git: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatal("git executable is not a regular file")
	}
	return path
}

// workspaceBoundarySetupEnv 仅为 fixture 初始化和 Git 自描述查询隔离继承上下文，不用于被测命令。
func workspaceBoundarySetupEnv() []string {
	var result []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			result = append(result, entry)
		}
	}
	return append(result, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
}

// workspaceBoundaryGitOutput 有界执行本机 Git，不输出可能来自父环境的敏感内容。
func workspaceBoundaryGitOutput(t *testing.T, git, cwd string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, git, args...)
	cmd.Dir, cmd.Env = cwd, env
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s failed: %v", args[0], err)
	}
	return string(output)
}

// workspaceBoundaryCanonical 消除临时目录的符号链接和平台路径分隔差异。
func workspaceBoundaryCanonical(t *testing.T, path string) string {
	t.Helper()
	path, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		t.Fatalf("absolute fixture path: %v", err)
	}
	return filepath.Clean(path)
}

// assertWorkspaceBoundaryEnvironment 同时固定 Git 隔离、认证继承和已有 V3/数据库隔离契约。
func assertWorkspaceBoundaryEnvironment(t *testing.T, provider Provider, localKeys []string) {
	t.Helper()
	blocked := []string{"GO_AGENT_CTL_SESSION_TOKEN", "SUPER_DOLPHIN_HOME", "PROJECT_ROOT", "DATABASE_URL"}
	for _, key := range append(blocked, "CODEX_HOME", "CLAUDE_CONFIG_DIR") {
		t.Setenv(key, "parent-boundary-sentinel")
	}
	auth := []string{"GIT_ASKPASS", "GIT_SSH_COMMAND", "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL"}
	for _, key := range auth {
		t.Setenv(key, "auth-boundary-sentinel")
	}
	env := make(map[string]string)
	for _, entry := range cliEnvironment(provider) {
		key, value, _ := strings.Cut(entry, "=")
		env[key] = value
	}
	for _, key := range append(localKeys, blocked...) {
		if _, ok := env[key]; ok {
			t.Errorf("parent context key %s leaked to %s provider", key, provider.Name)
		}
	}
	for _, key := range auth {
		if env[key] != "auth-boundary-sentinel" {
			t.Errorf("explicit authentication key %s was not preserved", key)
		}
	}
	assertWorkspaceBoundaryProviderHome(t, provider, env)
}

// assertWorkspaceBoundaryProviderHome 验证明示的 provider home 不受父进程配置影响。
func assertWorkspaceBoundaryProviderHome(t *testing.T, provider Provider, env map[string]string) {
	t.Helper()
	homeKey, otherKey := "CODEX_HOME", "CLAUDE_CONFIG_DIR"
	if provider.Name == "claude" {
		homeKey, otherKey = otherKey, homeKey
	}
	if env[homeKey] != provider.Home {
		t.Errorf("%s did not use explicit provider home", homeKey)
	}
	if _, ok := env[otherKey]; ok {
		t.Errorf("unselected provider home %s leaked", otherKey)
	}
}
