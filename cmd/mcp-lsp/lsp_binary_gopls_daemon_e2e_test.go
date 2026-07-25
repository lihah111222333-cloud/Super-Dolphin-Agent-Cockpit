//go:build e2e

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	gostdruntime "runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const fakeGoplsArgsLogEnv = "MCP_LSP_FAKE_GOPLS_ARGS_LOG"

func TestMcpLSPBinaryConcurrentAgentsUseSharedGoplsDaemon_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}

	roots := []string{t.TempDir(), t.TempDir()}
	targets := []string{
		writeFakeGoplsGoFixture(t, roots[0]),
		writeFakeGoplsGoFixture(t, roots[1]),
	}
	binary := buildMcpLSPBinaryForTest(t)
	argsLog := filepath.Join(t.TempDir(), "gopls-args.log")
	fakeGoplsBinDir := writeFakeGoplsArgsLangserver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	clients := make([]*mcpLSPBinaryClient, 0, 2)
	t.Cleanup(func() {
		for _, client := range clients {
			client.close(t)
		}
	})
	for index, root := range roots {
		client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
			fakeGoplsArgsLogEnv + "=" + argsLog,
		})
		clients = append(clients, client)
		client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
		result := client.callTool(t, "structure", map[string]any{
			"action":    "document_symbol",
			"file_path": targets[index],
		})
		requireMCPToolSuccess(t, client, result, "shared gopls daemon document_symbol")
	}

	invocations := waitForFakeGoplsInvocations(t, argsLog, len(clients))
	for index, args := range invocations {
		if !slices.ContainsFunc(args, func(arg string) bool {
			return strings.HasPrefix(arg, "-remote=auto;sdmcp2-")
		}) {
			t.Fatalf("gopls invocation %d args = %v, want product-owned remote cohort", index, args)
		}
		if !slices.Contains(args, "-remote.listen.timeout=1m") {
			t.Fatalf("gopls invocation %d args = %v, want explicit daemon idle timeout", index, args)
		}
	}
	if !slices.Equal(invocations[0], invocations[1]) {
		t.Fatalf("concurrent agents received different gopls daemon args: first=%v second=%v", invocations[0], invocations[1])
	}
}

func TestMcpLSPBinaryExitsAfterProcessIdleTimeout_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary lifecycle e2e test in short mode")
	}

	root := t.TempDir()
	binary := buildMcpLSPBinaryForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, t.TempDir(), []string{
		"MCP_LSP_PROCESS_IDLE_TIMEOUT=200ms",
	})
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	waitForMcpLSPNaturalExit(t, client, 3*time.Second)
}

func TestMcpLSPBinaryRealGoplsDaemonExitsAfterLastForwarder_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real gopls daemon lifecycle e2e test in short mode")
	}
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		t.Skipf("gopls is not installed: %v", err)
	}

	roots := []string{t.TempDir(), t.TempDir()}
	targets := []string{
		writeFakeGoplsGoFixture(t, roots[0]),
		writeFakeGoplsGoFixture(t, roots[1]),
	}
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
		"AGENT_LSP_GO_RSS_LIMIT_MB=384",
		"GOWORK=off",
		"MCP_LSP_GOPLS_DAEMON_IDLE_TIMEOUT=2s",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	first := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, roots[0], goplsBinDir, env)
	second := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, roots[1], goplsBinDir, env)
	for index, client := range []*mcpLSPBinaryClient{first, second} {
		client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
		result := client.callTool(t, "structure", map[string]any{
			"action":    "document_symbol",
			"file_path": targets[index],
		})
		requireMCPToolSuccess(t, client, result, "real shared gopls daemon")
	}

	waitForGoplsDaemonState(t, goplsPath, runtimeDir, true, 5*time.Second)
	first.close(t)
	waitForGoplsDaemonState(t, goplsPath, runtimeDir, true, 2*time.Second)
	result := second.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": targets[1],
	})
	requireMCPToolSuccess(t, second, result, "remaining worktree after first forwarder exits")
	second.close(t)
	waitForGoplsDaemonState(t, goplsPath, runtimeDir, false, 10*time.Second)
}

func waitForMcpLSPNaturalExit(t *testing.T, client *mcpLSPBinaryClient, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	var waiters sync.WaitGroup
	waiters.Go(func() { done <- client.cmd.Wait() })
	defer waiters.Wait()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Fatalf("mcp-lsp idle exit: %v; stderr=%s", err, client.stderrString())
		}
	case <-time.After(timeout):
		_ = client.cmd.Process.Kill()
		t.Fatalf("mcp-lsp did not exit after configured idle timeout; stderr=%s", client.stderrString())
	}
}

func waitForGoplsDaemonState(t *testing.T, goplsPath, runtimeDir string, wantRunning bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	probeInterval := 50 * time.Millisecond
	if !wantRunning {
		probeInterval = 3 * time.Second
	}
	var lastOutput []byte
	var lastErr error
	for {
		cmd := exec.Command(goplsPath, goplsE2ERemoteArg(filepath.Dir(goplsPath)), "remote", "sessions")
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

func goplsE2ERemoteArg(goplsBinDir string) string {
	env := runtimeServerGoplsEnvironment([]string{
		"GOOS=" + gostdruntime.GOOS,
		"GOARCH=" + gostdruntime.GOARCH,
		"GOMEMLIMIT=384MiB",
		"GOWORK=off",
		"PATH=" + goplsBinDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	})
	env = append(env, runtimeServerGoplsDaemonArgs([]string{
		"-remote=auto;sdmcp2",
		"-remote.listen.timeout=2s",
	}))
	fingerprint := runtimeServerEnvironmentFingerprint(env)
	return "-remote=auto;sdmcp2-" + fingerprint
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
