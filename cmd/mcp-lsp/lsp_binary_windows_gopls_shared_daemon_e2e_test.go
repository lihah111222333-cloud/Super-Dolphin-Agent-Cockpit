//go:build windows && e2e

package main

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	fakeWindowsGoplsIdle    = 2 * time.Second
	fakeWindowsGoplsVersion = "fake-gopls-e2e-v1"
)

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsGoplsSharedDaemonLifecycleE2E(t *testing.T) {
	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	argsLog := filepath.Join(t.TempDir(), "gopls-args.log")
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	install := buildWindowsGoplsShortIdlePrecheckTestInstall(t)
	env := windowsGoplsSidecarEnv(t, install, fakeGoplsArgsLogEnv+"="+argsLog, "AGENT_LSP_SHARED_CACHE_DIR="+cacheRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	clients := startWindowsGoplsClients(t, ctx, install.Binary, roots, filepath.Dir(install.Gopls), env, targets)
	endpoint, daemonPID := requireWindowsGoplsTopology(t, waitForFakeGoplsInvocations(t, argsLog, 3), install.Gopls)
	record := waitForWindowsGoplsBrokerRecord(t, cacheRoot)
	requireWindowsGoplsBrokerRecord(t, record, install, endpoint, daemonPID)

	clients[0].close(t)
	result := clients[1].callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": targets[1]})
	requireMCPToolSuccess(t, clients[1], result, "second Windows forwarder after first closed")
	requireWindowsProcessAlive(t, daemonPID, "gopls daemon")
	requireWindowsProcessAlive(t, record.OwnerPID, "gopls broker")
	clients[1].close(t)
	requireWindowsProcessExit(t, daemonPID, "gopls daemon")
	requireWindowsProcessExit(t, record.OwnerPID, "gopls broker")
}

func startWindowsGoplsClients(t *testing.T, ctx context.Context, binary string, roots [2]string, binDir string, env []string, targets [2]string) []*mcpLSPBinaryClient {
	t.Helper()
	clients := make([]*mcpLSPBinaryClient, 2)
	for index := range clients {
		clients[index] = startWindowsGoplsMCPBinaryForTest(t, ctx, binary, roots[index], binDir, env)
		client := clients[index]
		t.Cleanup(func() { client.close(t) })
		client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
		result := client.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": targets[index]})
		requireMCPToolSuccess(t, client, result, "Windows shared gopls forwarder warmup structure")
	}
	return clients
}

func requireWindowsGoplsTopology(t *testing.T, invocations [][]string, executable string) (string, int) {
	return requireWindowsGoplsTopologyWithIdle(t, invocations, executable, fakeWindowsGoplsIdle)
}

func requireWindowsGoplsTopologyWithIdle(t *testing.T, invocations [][]string, executable string, idle time.Duration) (string, int) {
	t.Helper()
	daemon, forwarders := splitWindowsGoplsInvocations(invocations)
	endpoint, daemonPID := requireWindowsGoplsDaemonInvocationWithIdle(t, daemon, executable, idle)
	requireWindowsGoplsForwarders(t, forwarders, endpoint, executable)
	return endpoint, daemonPID
}

func splitWindowsGoplsInvocations(invocations [][]string) ([]string, [][]string) {
	var daemon []string
	var forwarders [][]string
	for _, args := range invocations {
		if len(args) > 0 && args[0] == "serve" {
			daemon = args
		} else {
			forwarders = append(forwarders, args)
		}
	}
	return daemon, forwarders
}

func requireWindowsGoplsDaemonInvocation(t *testing.T, daemon []string, executable string) (string, int) {
	return requireWindowsGoplsDaemonInvocationWithIdle(t, daemon, executable, fakeWindowsGoplsIdle)
}

func requireWindowsGoplsDaemonInvocationWithIdle(t *testing.T, daemon []string, executable string, idle time.Duration) (string, int) {
	t.Helper()
	requireWindowsGoplsInvocationExecutable(t, daemon, executable, "daemon")
	address := windowsGoplsArg(daemon, "-listen=tcp;")
	if address == "" || !strings.HasPrefix(address, "127.0.0.1:") {
		t.Fatalf("gopls daemon args = %v, want explicit loopback TCP listener", daemon)
	}
	if windowsGoplsArg(daemon, "-listen.timeout=") != idle.String() {
		t.Fatalf("gopls daemon args = %v, want idle timeout %s", daemon, idle)
	}
	daemonPID, err := strconv.Atoi(windowsGoplsArg(daemon, "pid="))
	if err != nil || daemonPID <= 1 {
		t.Fatalf("gopls daemon PID in %v: %v", daemon, err)
	}
	return "tcp;" + address, daemonPID
}

func requireWindowsGoplsForwarders(t *testing.T, forwarders [][]string, endpoint, executable string) {
	t.Helper()
	if len(forwarders) != 2 {
		t.Fatalf("gopls forwarder invocations = %v, want two", forwarders)
	}
	for index, args := range forwarders {
		requireWindowsGoplsInvocationExecutable(t, args, executable, "forwarder")
		if windowsGoplsArg(args, "-remote=") != endpoint {
			t.Fatalf("gopls forwarder %d args = %v, want endpoint %s", index, args, endpoint)
		}
	}
	if windowsGoplsArg(forwarders[0], "pid=") == windowsGoplsArg(forwarders[1], "pid=") {
		t.Fatalf("gopls forwarders reused one process: %v", forwarders)
	}
}

func requireWindowsGoplsInvocationExecutable(t *testing.T, args []string, want, role string) {
	t.Helper()
	encoded := windowsGoplsArg(args, "exe_hex=")
	raw, err := hex.DecodeString(encoded)
	got := string(raw)
	if err != nil || !strings.EqualFold(filepath.Clean(got), filepath.Clean(want)) {
		t.Fatalf("gopls %s actual executable = %q (%v), want native %q; args=%v", role, got, err, want, args)
	}
}

func requireWindowsProcessAlive(t *testing.T, pid int, label string) {
	t.Helper()
	if alive, err := processAliveForE2E(pid); err != nil || !alive {
		t.Fatalf("%s PID %d after first close: alive=%t err=%v", label, pid, alive, err)
	}
}

func requireWindowsProcessExit(t *testing.T, pid int, label string) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for {
		alive, err := processAliveForE2E(pid)
		if err != nil {
			t.Fatalf("check %s PID %d: %v", label, pid, err)
		}
		if !alive {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s PID %d remained after last-forwarder idle timeout", label, pid)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func windowsGoplsArg(args []string, prefix string) string {
	for _, arg := range args {
		if value, ok := strings.CutPrefix(arg, prefix); ok {
			return value
		}
	}
	return ""
}
