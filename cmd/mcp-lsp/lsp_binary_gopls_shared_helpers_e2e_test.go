//go:build e2e

package main

// 本文件是跨平台公共 E2E helper，故意只带 e2e 而不带 GOOS/GOARCH 标签：
// Windows、Darwin 和 Linux 的专用测试都复用相同的 Git worktree fixture 与
// fake-gopls 调用账本解析；平台进程控制和平台专用断言仍留在各自显式标签文件中。

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fakeGoplsArgsLogEnv = "MCP_LSP_FAKE_GOPLS_ARGS_LOG"

// writeRealGoplsLinkedWorktreeFixtures 创建各平台共用的真实 Git worktree 语义夹具。
func writeRealGoplsLinkedWorktreeFixtures(t *testing.T) ([2]string, [2]string) {
	t.Helper()
	base := t.TempDir()
	repository := filepath.Join(base, "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatalf("create real gopls E2E repository: %v", err)
	}
	runRealGoplsGit(t, repository, "init")
	runRealGoplsGit(t, repository, "config", "user.name", "真实 gopls E2E")
	runRealGoplsGit(t, repository, "config", "user.email", "gopls-e2e@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "go.mod"), []byte("module example.com/gopls-daemon-e2e\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatalf("write real gopls E2E go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write real gopls E2E main.go: %v", err)
	}
	runRealGoplsGit(t, repository, "add", "go.mod", "main.go")
	runRealGoplsGit(t, repository, "commit", "-m", "初始化真实 gopls E2E 仓库")

	var roots [2]string
	var targets [2]string
	for index, name := range []string{"one", "two"} {
		roots[index] = filepath.Join(base, "worktrees", name)
		runRealGoplsGit(t, repository, "worktree", "add", "--detach", roots[index], "HEAD")
		targets[index] = filepath.Join(roots[index], "main.go")
	}
	t.Cleanup(func() {
		for index := len(roots) - 1; index >= 0; index-- {
			cmd := exec.Command("git", "-C", repository, "worktree", "remove", "--force", roots[index])
			if output, err := cmd.CombinedOutput(); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove real gopls linked worktree %s: %v; output=%s", roots[index], err, output)
			}
		}
		cmd := exec.Command("git", "-C", repository, "worktree", "prune")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("prune real gopls linked worktrees: %v; output=%s", err, output)
		}
	})
	return roots, targets
}

// runRealGoplsGit 执行各平台共用的 Git fixture 命令并保留失败输出。
func runRealGoplsGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"-C", repository}, args...)
	cmd := exec.Command("git", cmdArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v; output=%s", strings.Join(args, " "), err, output)
	}
}

// waitForFakeGoplsInvocations 等待共用 fake-gopls 调用账本达到指定闭包。
func waitForFakeGoplsInvocations(t *testing.T, path string, count int) [][]string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		invocations, payload, err := readFakeGoplsInvocations(path, count)
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("read fake gopls args log: %v", err)
		}
		if len(invocations) >= count {
			return invocations
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake gopls invocations = %q, want at least %d", payload, count)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// readFakeGoplsInvocations 解析各平台写入的相同 fake-gopls 调用账本格式。
func readFakeGoplsInvocations(path string, count int) ([][]string, []byte, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")
	if len(lines) < count {
		return nil, payload, nil
	}
	invocations := make([][]string, 0, count)
	for _, line := range lines[:count] {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "invocation" {
			return nil, payload, nil
		}
		invocations = append(invocations, append([]string(nil), fields[1:]...))
	}
	return invocations, payload, nil
}
