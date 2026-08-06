//go:build e2e

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	gostdruntime "runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

const fakeGoplsArgsLogEnv = "MCP_LSP_FAKE_GOPLS_ARGS_LOG"

func TestMcpLSPBinaryGoplsRootCohortSharesLinkedWorktreesAndIsolatesUnrelatedRoot_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if gostdruntime.GOOS == "windows" {
		t.Skip("Windows uses independent gopls processes plus the cross-worktree RSS cohort ledger")
	}

	fixture := newGoplsRootE2EFixture(t)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsArgsLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	clients := make([]*mcpLSPBinaryClient, 0, len(fixture.roots))
	t.Cleanup(func() {
		for _, client := range clients {
			client.close(t)
		}
	})
	argsLogs := make([]string, len(fixture.roots))
	for index, root := range fixture.roots {
		argsLogs[index] = filepath.Join(t.TempDir(), "gopls-args.log")
		client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
			fakeGoplsArgsLogEnv + "=" + argsLogs[index],
		})
		clients = append(clients, client)
		client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
		result := client.callTool(t, "structure", map[string]any{
			"action":    "document_symbol",
			"file_path": fixture.targets[index],
		})
		requireMCPToolSuccess(t, client, result, "root cohort document_symbol")
	}

	assertGoplsRootE2EInvocations(t, argsLogs)
}

// assertGoplsRootE2EInvocations 校验 fake gopls 的 root cohort 参数和跨 worktree 隔离。
func assertGoplsRootE2EInvocations(t *testing.T, argsLogs []string) {
	t.Helper()
	remoteIDs := make([]string, len(argsLogs))
	for index, path := range argsLogs {
		args := waitForFakeGoplsInvocations(t, path, 1)[0]
		remoteIDs[index] = goplsRemoteIDFromArgs(t, args)
		if !slices.ContainsFunc(args, func(arg string) bool { return strings.HasPrefix(arg, "-remote=auto;sdmcp2-") }) {
			t.Fatalf("gopls invocation %d args = %v, want root-scoped remote cohort", index, args)
		}
		if !slices.Contains(args, "-remote.listen.timeout=15m0s") {
			t.Fatalf("gopls invocation %d args = %v, want the 15-minute daemon idle contract", index, args)
		}
	}
	if len(remoteIDs) < 3 {
		t.Fatalf("gopls invocation IDs = %v, want linked and unrelated roots", remoteIDs)
	}
	if remoteIDs[0] != remoteIDs[1] {
		t.Fatalf("linked worktrees received different root cohort IDs: first=%q second=%q", remoteIDs[0], remoteIDs[1])
	}
	if remoteIDs[0] == remoteIDs[2] {
		t.Fatalf("unrelated root reused linked-worktree cohort ID %q", remoteIDs[0])
	}
}

func TestMcpLSPBinaryGoplsRootScopedProbeHonorsFifteenMinuteIdleContract_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real gopls daemon lifecycle e2e test in short mode")
	}
	if gostdruntime.GOOS == "windows" {
		t.Skip("gopls auto daemon IDs are unsupported on Windows")
	}
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		t.Skipf("gopls is not installed: %v", err)
	}

	fixture := newGoplsRootE2EFixture(t)
	binary := buildMcpLSPBinaryForTest(t)
	runtimeDir, err := os.MkdirTemp("/tmp", "mcp-lsp-gopls-")
	if err != nil {
		t.Fatalf("create short gopls runtime dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Errorf("remove gopls runtime dir: %v", err)
		}
	})
	goplsBinDir := filepath.Dir(goplsPath)
	env := []string{
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(runtimeDir, "lsp-resource"),
		"AGENT_LSP_GO_RSS_LIMIT_MB=384",
		"GOWORK=off",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	clients := make([]*mcpLSPBinaryClient, 0, len(fixture.roots))
	for index, root := range fixture.roots {
		client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, goplsBinDir, env)
		clients = append(clients, client)
		client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
		result := client.callTool(t, "structure", map[string]any{
			"action":    "document_symbol",
			"file_path": fixture.targets[index],
		})
		requireMCPToolSuccess(t, client, result, "real root cohort daemon")
	}

	for _, root := range fixture.roots {
		waitForGoplsDaemonState(t, goplsPath, runtimeDir, root, true, 5*time.Second)
	}
	if firstID, secondID := goplsE2ERemoteArg(t, goplsPath, goplsBinDir, fixture.roots[0]), goplsE2ERemoteArg(t, goplsPath, goplsBinDir, fixture.roots[1]); firstID != secondID {
		t.Fatalf("linked worktrees received different remote probe IDs: %q vs %q", firstID, secondID)
	}
	if linkedID, unrelatedID := goplsE2ERemoteArg(t, goplsPath, goplsBinDir, fixture.roots[0]), goplsE2ERemoteArg(t, goplsPath, goplsBinDir, fixture.roots[2]); linkedID == unrelatedID {
		t.Fatalf("unrelated root reused linked-worktree remote probe ID %q", linkedID)
	}
	clients[0].close(t)
	waitForGoplsDaemonState(t, goplsPath, runtimeDir, fixture.roots[0], true, 2*time.Second)
	result := clients[1].callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": fixture.targets[1],
	})
	requireMCPToolSuccess(t, clients[1], result, "remaining linked worktree after first forwarder exits")
	clients[1].close(t)
	// The root controller deliberately keeps the daemon alive until the default
	// 15-minute root-level idle window; a short probe must not claim completion.
	waitForGoplsDaemonState(t, goplsPath, runtimeDir, fixture.roots[0], true, 2*time.Second)
	clients[2].close(t)
	waitForGoplsDaemonState(t, goplsPath, runtimeDir, fixture.roots[2], true, 2*time.Second)
}

func waitForGoplsDaemonState(t *testing.T, goplsPath, runtimeDir, workspaceRoot string, wantRunning bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	probeInterval := 50 * time.Millisecond
	if !wantRunning {
		probeInterval = 3 * time.Second
	}
	var lastOutput []byte
	var lastErr error
	for {
		cmd := exec.Command(goplsPath, goplsE2ERemoteArg(t, goplsPath, filepath.Dir(goplsPath), workspaceRoot), "remote", "sessions")
		cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
		lastOutput, lastErr = cmd.CombinedOutput()
		if (lastErr == nil) == wantRunning {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("gopls daemon running = %v, want %v; err=%v output=%s", lastErr == nil, wantRunning, lastErr, lastOutput)
		}
		time.Sleep(probeInterval)
	}
}

func goplsE2ERemoteArg(t *testing.T, goplsPath, goplsBinDir, workspaceRoot string) string {
	t.Helper()
	env := runtimeServerGoplsEnvironment([]string{
		"GOOS=" + gostdruntime.GOOS,
		"GOARCH=" + gostdruntime.GOARCH,
		"GOMEMLIMIT=3584MiB",
		"GOWORK=off",
		"PATH=" + goplsBinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args: []string{
			"-remote=auto;sdmcp2",
			"-remote.listen.timeout=15m0s",
		},
	}
	cohortID, err := runtimeServerGoplsCohortID(command, goplsPath, env, workspaceRoot)
	if err != nil {
		t.Fatalf("derive real gopls E2E cohort ID: %v", err)
	}
	return "-remote=auto;" + cohortID
}

func goplsRemoteIDFromArgs(t *testing.T, args []string) string {
	t.Helper()
	for _, arg := range args {
		if strings.HasPrefix(arg, "-remote=auto;sdmcp2-") {
			return strings.TrimPrefix(arg, "-remote=auto;")
		}
	}
	t.Fatalf("gopls invocation args = %v, want a root-scoped remote ID", args)
	return ""
}

type goplsRootE2EFixture struct {
	roots   [3]string
	targets [3]string
}

func newGoplsRootE2EFixture(t *testing.T) goplsRootE2EFixture {
	t.Helper()
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatalf("create gopls E2E repository: %v", err)
	}
	runGoplsRootE2EGit(t, repository, "init")
	runGoplsRootE2EGit(t, repository, "config", "user.name", "gopls root E2E")
	runGoplsRootE2EGit(t, repository, "config", "user.email", "gopls-root-e2e@example.invalid")
	target := writeFakeGoplsGoFixture(t, repository)
	runGoplsRootE2EGit(t, repository, "add", "go.mod", "main.go")
	runGoplsRootE2EGit(t, repository, "commit", "-m", "初始化 gopls root E2E 仓库")
	linkedRoot := filepath.Join(root, "linked")
	if err := os.MkdirAll(linkedRoot, 0o700); err != nil {
		t.Fatalf("create linked worktree parent: %v", err)
	}
	fixture := goplsRootE2EFixture{targets: [3]string{filepath.Join(linkedRoot, "primary", filepath.Base(target)), filepath.Join(linkedRoot, "secondary", filepath.Base(target)), ""}}
	fixture.roots = [3]string{filepath.Join(linkedRoot, "primary"), filepath.Join(linkedRoot, "secondary"), filepath.Join(root, "unrelated")}
	for _, worktree := range fixture.roots[:2] {
		runGoplsRootE2EGit(t, repository, "worktree", "add", "--detach", worktree, "HEAD")
	}
	t.Cleanup(func() {
		for index := 1; index >= 0; index-- {
			cmd := exec.Command("git", "-C", repository, "worktree", "remove", "--force", fixture.roots[index])
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("remove gopls linked worktree %s: %v; output=%s", fixture.roots[index], err, output)
			}
		}
		runGoplsRootE2EGit(t, repository, "worktree", "prune")
	})
	if err := os.MkdirAll(fixture.roots[2], 0o700); err != nil {
		t.Fatalf("create unrelated gopls repository: %v", err)
	}
	runGoplsRootE2EGit(t, fixture.roots[2], "init")
	runGoplsRootE2EGit(t, fixture.roots[2], "config", "user.name", "gopls unrelated E2E")
	runGoplsRootE2EGit(t, fixture.roots[2], "config", "user.email", "gopls-unrelated-e2e@example.invalid")
	fixture.targets[2] = writeFakeGoplsGoFixture(t, fixture.roots[2])
	runGoplsRootE2EGit(t, fixture.roots[2], "add", "go.mod", "main.go")
	runGoplsRootE2EGit(t, fixture.roots[2], "commit", "-m", "初始化独立 gopls root E2E 仓库")
	return fixture
}

func runGoplsRootE2EGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s in %s: %v; output=%s", strings.Join(args, " "), dir, err, output)
	}
}

func writeFakeGoplsArgsLangserver(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "gopls")
	script := "#!/bin/sh\n" +
		"printf 'invocation\\t%s\\n' \"$*\" >> \"$" + fakeGoplsArgsLogEnv + "\"\n" +
		"MCP_LSP_FAKE_GOPLS_SHUTDOWN_WARNING=1 exec " + shellQuote(os.Args[0]) +
		" -test.run=TestFakeGoplsShutdownWarningHelper -- \"$@\"\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake gopls args langserver: %v", err)
	}
	return dir
}

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
