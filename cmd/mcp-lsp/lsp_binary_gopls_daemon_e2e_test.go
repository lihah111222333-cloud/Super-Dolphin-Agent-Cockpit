//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	gostdruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
)

const fakeGoplsArgsLogEnv = "MCP_LSP_FAKE_GOPLS_ARGS_LOG"

const realGoplsRemoteListenTimeout = 15 * time.Minute

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryConcurrentAgentsRespectGoplsRootCohortIsolation_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mcp-lsp binary e2e test in short mode")
	}
	if gostdruntime.GOOS == "windows" {
		t.Skip("Windows uses independent gopls processes plus the cross-worktree RSS cohort ledger")
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
			"MCP_LSP_IDLE_TIMEOUT=1s",
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
		if !slices.Contains(args, "-remote.listen.timeout=1s") {
			t.Fatalf("gopls invocation %d args = %v, want explicit daemon idle timeout", index, args)
		}
	}
	if slices.Equal(invocations[0], invocations[1]) {
		t.Fatalf("unrelated roots unexpectedly received identical gopls daemon args: first=%v second=%v", invocations[0], invocations[1])
	}
}

// TestMcpLSPBinaryCodexHostIgnoresUnrelatedGOEnvironmentForRootCohort_E2E 锁定
// Codex 宿主并发 sidecar 的无关 GO 前缀变量不得拆分同一项目根的 gopls cohort。
func TestMcpLSPBinaryCodexHostIgnoresUnrelatedGOEnvironmentForRootCohort_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Codex host root-cohort environment E2E in short mode")
	}
	if gostdruntime.GOOS == "windows" {
		t.Skip("gopls auto daemon root cohorts are unsupported on Windows")
	}

	root := t.TempDir()
	target := writeFakeGoplsGoFixture(t, root)
	binary := buildMcpLSPBinaryForTest(t)
	fakeGoplsBinDir := writeFakeGoplsArgsLangserver(t)
	cacheRoot := filepath.Join(t.TempDir(), "codex-host-lsp-cache")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	first := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		"AGENT_LSP_SHARED_CACHE_DIR=" + cacheRoot,
		"GOOGLE_APPLICATION_CREDENTIALS=" + filepath.Join(t.TempDir(), "codex-host-a.json"),
	})
	t.Cleanup(func() { first.close(t) })
	first.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	firstResult := first.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireMCPToolSuccess(t, first, firstResult, "Codex host sidecar A document_symbol")

	second := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeGoplsBinDir, []string{
		"AGENT_LSP_SHARED_CACHE_DIR=" + cacheRoot,
		"GOOGLE_APPLICATION_CREDENTIALS=" + filepath.Join(t.TempDir(), "codex-host-b.json"),
	})
	t.Cleanup(func() { second.close(t) })
	second.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	secondResult := second.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireMCPToolSuccess(t, second, secondResult, "Codex host sidecar B document_symbol with unrelated GO environment drift")
}

// TestMcpLSPBinaryNewGenerationReplacesResidualGoplsDaemonSameRoot_E2E 复现同一
// canonical cwd 的上一代 sidecar 异常退出、daemon 仍存活且 active 状态保留上一代
// completion receipt；新一代必须在旧 daemon 被精确终止后完成配置轮换并重新提供 LSP。
// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryNewGenerationReplacesResidualGoplsDaemonSameRoot_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping residual gopls generation replacement E2E in short mode")
	}
	if gostdruntime.GOOS == "windows" {
		t.Skip("gopls auto daemon IDs are unsupported on Windows")
	}
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		t.Fatalf("gopls is required for residual generation E2E: %v", err)
	}

	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	root, target := roots[0], targets[0]
	binary := buildMcpLSPBinaryForTest(t)
	runtimeDir, err := os.MkdirTemp("/tmp", "mcp-lsp-gopls-replace-")
	if err != nil {
		t.Fatalf("create residual gopls runtime dir: %v", err)
	}
	t.Cleanup(func() {
		killAllGoplsDaemonProcessesForE2E(t, goplsPath, runtimeDir)
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Errorf("remove residual gopls runtime dir: %v", err)
		}
	})
	baseEnv := []string{
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(runtimeDir, "lsp-resource"),
		"AGENT_LSP_GO_RSS_LIMIT_MB=384",
		"GOWORK=off",
		"MCP_LSP_IDLE_TIMEOUT=" + realGoplsRemoteListenTimeout.String(),
	}
	oldEnv := append(slices.Clone(baseEnv), "GOFLAGS=-tags=old_gopls_generation")
	newEnv := append(slices.Clone(baseEnv), "GOFLAGS=-tags=new_gopls_generation")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	first := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, filepath.Dir(goplsPath), oldEnv)
	t.Cleanup(func() { first.close(t) })
	first.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	firstResult := first.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireMCPToolSuccess(t, first, firstResult, "old generation document_symbol")
	oldDaemon := requireSingleGoplsDaemonProcess(t, goplsPath, runtimeDir)

	first.close(t)
	second := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, filepath.Dir(goplsPath), oldEnv)
	t.Cleanup(func() { second.close(t) })
	second.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	secondResult := second.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireMCPToolSuccess(t, second, secondResult, "old generation re-admission before crash")
	if got := requireSingleGoplsDaemonProcess(t, goplsPath, runtimeDir).PID; got != oldDaemon.PID {
		t.Fatalf("old generation daemon PID changed before crash: got=%d want=%d", got, oldDaemon.PID)
	}
	next := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, filepath.Dir(goplsPath), newEnv)
	t.Cleanup(func() { next.close(t) })
	next.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	conflict := next.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireGoplsRootCohortConflict(t, next, conflict)
	killGoplsDaemonProcessForE2E(t, goplsPath, runtimeDir, oldDaemon)
	if processes := requireGoplsDaemonProcesses(t, goplsPath, runtimeDir); len(processes) != 0 {
		t.Fatalf("old generation daemon remained after exact-PID termination: %#v", processes)
	}
	killMcpLSPBinaryClientAbruptly(t, second)
	nextResult := next.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	})
	requireMCPToolSuccess(t, next, nextResult, "new generation document_symbol after residual daemon termination")
	newDaemon := requireSingleGoplsDaemonProcess(t, goplsPath, runtimeDir)
	if newDaemon.PID == oldDaemon.PID {
		t.Fatalf("new generation reused terminated daemon PID %d", newDaemon.PID)
	}
	t.Logf("new gopls generation replaced residual daemon: old_pid=%d new_pid=%d", oldDaemon.PID, newDaemon.PID)
}

func requireGoplsRootCohortConflict(t *testing.T, client *mcpLSPBinaryClient, response mcpLSPBinaryResponse) {
	t.Helper()
	if !response.Result.IsError || !strings.Contains(response.Result.ContentText(), multilsp.ErrGoplsRootCohortConfigConflict.Error()) {
		t.Fatalf("new generation did not reproduce immutable-config conflict before exact cleanup: text=%q structured=%s stderr=%s",
			response.Result.ContentText(), response.Result.StructuredContent, client.stderrString())
	}
}

// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryRealGoplsDaemonExitsAfterLastForwarder_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real gopls daemon lifecycle e2e test in short mode")
	}
	if gostdruntime.GOOS == "windows" {
		t.Skip("gopls auto daemon IDs are unsupported on Windows")
	}
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		t.Fatalf("gopls is required for real daemon lifecycle e2e: %v", err)
	}

	roots, targets := writeRealGoplsLinkedWorktreeFixtures(t)
	binary := buildMcpLSPBinaryForTest(t)
	runtimeDir, err := os.MkdirTemp("/tmp", "mcp-lsp-gopls-")
	if err != nil {
		t.Fatalf("create gopls runtime dir: %v", err)
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
		"MCP_LSP_IDLE_TIMEOUT=" + realGoplsRemoteListenTimeout.String(),
	}
	assertRealGoplsLinkedWorktreeCohort(t, goplsPath, roots, env)
	ctx, cancel := context.WithTimeout(context.Background(), realGoplsRemoteListenTimeout+2*time.Minute)
	defer cancel()

	first := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, roots[0], goplsBinDir, env)
	t.Cleanup(func() { first.close(t) })
	first.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	result := first.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": targets[0],
	})
	requireMCPToolSuccess(t, first, result, "real gopls daemon before idle release")

	runningBeforeRelease := waitForGoplsDaemonState(t, goplsPath, runtimeDir, roots[0], true, 30*time.Second)
	daemonBeforeRelease := requireSingleGoplsDaemonProcess(t, goplsPath, runtimeDir)
	assertGoplsDaemonCommandTimeout(t, daemonBeforeRelease)
	t.Logf("real gopls daemon admitted: pid=%d command=%q sessions=%s", daemonBeforeRelease.PID, daemonBeforeRelease.Command, runningBeforeRelease.Output)

	// A is deliberately idle after its one request. Closing the real stdio sidecar
	// exercises the production ReleaseWithOwner path and closes its forwarder.
	first.close(t)
	// `remote sessions` itself is a transient gopls client, so an idle daemon
	// reports exactly one client after the sidecar forwarder has closed.
	afterIdleRelease := waitForGoplsDaemonStateWithClientCount(t, goplsPath, runtimeDir, roots[0], true, 1, 30*time.Second)
	daemonAfterRelease := requireSingleGoplsDaemonProcess(t, goplsPath, runtimeDir)
	if daemonAfterRelease.PID != daemonBeforeRelease.PID {
		t.Fatalf("daemon PID changed after A idle Release/forwarder close: before=%d after=%d before_cmd=%q after_cmd=%q",
			daemonBeforeRelease.PID, daemonAfterRelease.PID, daemonBeforeRelease.Command, daemonAfterRelease.Command)
	}
	t.Logf("A idle Release closed its forwarder while daemon stayed resident: pid=%d sessions=%s", daemonAfterRelease.PID, afterIdleRelease.Output)

	// B is admitted only after A has released. Its request must reach the same
	// resident daemon rather than starting a second daemon or resurrecting A.
	second := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, roots[1], goplsBinDir, env)
	t.Cleanup(func() { second.close(t) })
	second.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	result = second.callTool(t, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": targets[1],
	})
	requireMCPToolSuccess(t, second, result, "B re-admit after A idle Release")
	afterReAdmit := waitForGoplsDaemonStateWithClientCount(t, goplsPath, runtimeDir, roots[1], true, 2, 30*time.Second)
	daemonAfterReAdmit := requireSingleGoplsDaemonProcess(t, goplsPath, runtimeDir)
	if daemonAfterReAdmit.PID != daemonBeforeRelease.PID {
		t.Fatalf("daemon PID changed during B re-admit: before=%d after=%d command=%q", daemonBeforeRelease.PID, daemonAfterReAdmit.PID, daemonAfterReAdmit.Command)
	}
	t.Logf("B re-admitted on the old resident daemon: pid=%d sessions=%s", daemonAfterReAdmit.PID, afterReAdmit.Output)

	second.close(t)
	lastForwarderClosedAt := time.Now()
	afterLastForwarder := waitForGoplsDaemonStateWithClientCount(t, goplsPath, runtimeDir, roots[1], true, 1, 30*time.Second)
	t.Logf("last forwarder closed at %s; daemon still resident for configured timeout: sessions=%s", lastForwarderClosedAt.Format(time.RFC3339Nano), afterLastForwarder.Output)

	exitWaitStarted := time.Now()
	stopped := waitForGoplsDaemonSelfExit(t, goplsPath, runtimeDir, roots[1], realGoplsRemoteListenTimeout+90*time.Second)
	actualWait := time.Since(exitWaitStarted)
	if actualWait < realGoplsRemoteListenTimeout-30*time.Second {
		t.Fatalf("gopls daemon exited too early: wait=%s configured=%s err=%v output=%s", actualWait, realGoplsRemoteListenTimeout, stopped.Err, stopped.Output)
	}
	if len(requireGoplsDaemonProcesses(t, goplsPath, runtimeDir)) != 0 {
		t.Fatalf("gopls daemon process remained after endpoint became unreachable")
	}
	t.Logf("real gopls daemon self-exited after last forwarder: configured=%s actual_wait=%s endpoint_error=%v output=%s",
		realGoplsRemoteListenTimeout, actualWait, stopped.Err, stopped.Output)
}

type goplsRemoteState struct {
	GoplsPath string            `json:"goplsPath"`
	Clients   []json.RawMessage `json:"clients"`
}

type goplsRemoteProbe struct {
	Running bool
	State   goplsRemoteState
	Output  []byte
	Err     error
}

type goplsDaemonProcess struct {
	PID     int
	Command string
}

func waitForGoplsDaemonState(t *testing.T, goplsPath, runtimeDir, workspaceRoot string, wantRunning bool, timeout time.Duration) goplsRemoteProbe {
	t.Helper()
	deadline := time.Now().Add(timeout)
	probeInterval := 50 * time.Millisecond
	if !wantRunning {
		probeInterval = 2 * time.Second
	}
	var last goplsRemoteProbe
	for {
		last = queryGoplsDaemon(t, goplsPath, runtimeDir, workspaceRoot)
		if last.Running == wantRunning {
			if wantRunning {
				return last
			}
			processes, processErr := listGoplsDaemonProcesses(goplsPath, runtimeDir)
			if processErr == nil && len(processes) == 0 {
				return last
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("gopls daemon running = %v, want %v; err=%v output=%s", last.Running, wantRunning, last.Err, last.Output)
		}
		time.Sleep(probeInterval)
	}
}

func waitForGoplsDaemonStateWithClientCount(t *testing.T, goplsPath, runtimeDir, workspaceRoot string, wantRunning bool, wantClients int, timeout time.Duration) goplsRemoteProbe {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last goplsRemoteProbe
	for {
		last = queryGoplsDaemon(t, goplsPath, runtimeDir, workspaceRoot)
		if last.Running == wantRunning && (!wantRunning || len(last.State.Clients) == wantClients) {
			return last
		}
		if time.Now().After(deadline) {
			t.Fatalf("gopls daemon state = running:%v clients:%d, want running:%v clients:%d; err=%v output=%s",
				last.Running, len(last.State.Clients), wantRunning, wantClients, last.Err, last.Output)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// waitForGoplsDaemonSelfExit observes only the process table until the
// configured timeout elapses. Querying `remote sessions` while the daemon is
// idle would itself open a client and reset gopls's listen timeout, so the
// endpoint is probed exactly once after the daemon process disappears.
func waitForGoplsDaemonSelfExit(t *testing.T, goplsPath, runtimeDir, workspaceRoot string, timeout time.Duration) goplsRemoteProbe {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		processes, err := listGoplsDaemonProcesses(goplsPath, runtimeDir)
		if err != nil {
			t.Fatalf("list gopls daemon processes while waiting for self-exit: %v", err)
		}
		if len(processes) == 0 {
			probe := queryGoplsDaemon(t, goplsPath, runtimeDir, workspaceRoot)
			if probe.Running {
				t.Fatalf("gopls endpoint remained reachable after daemon process disappeared: output=%s", probe.Output)
			}
			return probe
		}
		if time.Now().After(deadline) {
			probe := queryGoplsDaemon(t, goplsPath, runtimeDir, workspaceRoot)
			t.Fatalf("gopls daemon did not self-exit by deadline: processes=%#v endpoint_running=%v err=%v output=%s",
				processes, probe.Running, probe.Err, probe.Output)
		}
		time.Sleep(2 * time.Second)
	}
}

func queryGoplsDaemon(t *testing.T, goplsPath, runtimeDir, workspaceRoot string) goplsRemoteProbe {
	t.Helper()
	cmd := exec.Command(goplsPath, goplsE2ERemoteArg(t, goplsPath, filepath.Dir(goplsPath), workspaceRoot), "remote", "sessions")
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+runtimeDir)
	output, err := cmd.CombinedOutput()
	probe := goplsRemoteProbe{Running: err == nil, Output: output, Err: err}
	if err == nil {
		if unmarshalErr := json.Unmarshal(output, &probe.State); unmarshalErr != nil {
			probe.Running = false
			probe.Err = fmt.Errorf("decode gopls remote sessions: %w", unmarshalErr)
		}
	}
	return probe
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
			"-remote.listen.timeout=" + realGoplsRemoteListenTimeout.String(),
		},
	}
	cohortID, err := runtimeServerGoplsCohortID(command, goplsPath, env, workspaceRoot)
	if err != nil {
		t.Fatalf("derive real gopls E2E cohort ID: %v", err)
	}
	return "-remote=auto;" + cohortID
}

func requireSingleGoplsDaemonProcess(t *testing.T, goplsPath, runtimeDir string) goplsDaemonProcess {
	t.Helper()
	processes := requireGoplsDaemonProcesses(t, goplsPath, runtimeDir)
	if len(processes) != 1 {
		t.Fatalf("gopls daemon processes for runtime %s = %#v, want exactly one", runtimeDir, processes)
	}
	return processes[0]
}

func requireGoplsDaemonProcesses(t *testing.T, goplsPath, runtimeDir string) []goplsDaemonProcess {
	t.Helper()
	processes, err := listGoplsDaemonProcesses(goplsPath, runtimeDir)
	if err != nil {
		t.Fatalf("list gopls daemon processes: %v", err)
	}
	return processes
}

func listGoplsDaemonProcesses(goplsPath, runtimeDir string) ([]goplsDaemonProcess, error) {
	cmd := exec.Command("ps", "-axo", "pid=,command=")
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var processes []goplsDaemonProcess
	for _, line := range strings.Split(string(output), "\n") {
		if process, ok := parseGoplsDaemonProcessLine(line, goplsPath, runtimeDir); ok {
			processes = append(processes, process)
		}
	}
	return processes, nil
}

func parseGoplsDaemonProcessLine(line, goplsPath, runtimeDir string) (goplsDaemonProcess, bool) {
	line = strings.TrimSpace(line)
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return goplsDaemonProcess{}, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil || pid <= 1 {
		return goplsDaemonProcess{}, false
	}
	command := strings.TrimSpace(strings.TrimPrefix(line, fields[0]))
	if !goplsDaemonCommandOwnsRuntime(command, goplsPath, runtimeDir) {
		return goplsDaemonProcess{}, false
	}
	return goplsDaemonProcess{PID: pid, Command: command}, true
}

func goplsDaemonCommandOwnsRuntime(command, goplsPath, runtimeDir string) bool {
	fields := strings.Fields(command)
	if len(fields) < 3 || filepath.Clean(fields[0]) != filepath.Clean(goplsPath) || fields[1] != "serve" {
		return false
	}
	listen, ok := goplsDaemonListenAddress(fields[2:])
	if !ok || !strings.HasPrefix(listen, "unix;") {
		return false
	}
	return pathIsWithinDirectory(strings.TrimPrefix(listen, "unix;"), runtimeDir)
}

// super-dolphin-ci: compile-group-exclusive
func TestGoplsDaemonCommandOwnsRuntimeRequiresExactBinaryAndSocketRoot(t *testing.T) {
	goplsPath := "/opt/tools/gopls"
	runtimeDir := "/tmp/mcp-lsp-owned"
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{name: "separate listen argument", command: goplsPath + " serve -listen unix;" + runtimeDir + "/daemon.sock", want: true},
		{name: "equals listen argument", command: goplsPath + " serve -listen=unix;" + runtimeDir + "/daemon.sock", want: true},
		{name: "different binary", command: "/opt/tools/other-gopls serve -listen unix;" + runtimeDir + "/daemon.sock"},
		{name: "sibling runtime prefix", command: goplsPath + " serve -listen unix;" + runtimeDir + "-other/daemon.sock"},
		{name: "non daemon command", command: goplsPath + " remote -listen unix;" + runtimeDir + "/daemon.sock"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := goplsDaemonCommandOwnsRuntime(test.command, goplsPath, runtimeDir); got != test.want {
				t.Fatalf("goplsDaemonCommandOwnsRuntime(%q) = %v, want %v", test.command, got, test.want)
			}
		})
	}
}

func TestParseGoplsDaemonProcessLineAcceptsPaddedLowPID(t *testing.T) {
	goplsPath := "/opt/tools/gopls"
	runtimeDir := "/tmp/mcp-lsp-owned"
	line := "  247 " + goplsPath + " serve -listen unix;" + runtimeDir + "/daemon.sock -listen.timeout 15m0s"
	process, ok := parseGoplsDaemonProcessLine(line, goplsPath, runtimeDir)
	if !ok || process.PID != 247 || !strings.HasPrefix(process.Command, goplsPath+" serve") {
		t.Fatalf("padded low-PID process parse = (%+v, %t)", process, ok)
	}
}

func goplsDaemonListenAddress(arguments []string) (string, bool) {
	for index, argument := range arguments {
		if strings.HasPrefix(argument, "-listen=") {
			return strings.TrimPrefix(argument, "-listen="), true
		}
		if argument == "-listen" && index+1 < len(arguments) {
			return arguments[index+1], true
		}
	}
	return "", false
}

func pathIsWithinDirectory(path, directory string) bool {
	relative, err := filepath.Rel(directory, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func killMcpLSPBinaryClientAbruptly(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
	if client == nil || client.cmd == nil || client.cmd.Process == nil {
		t.Fatal("mcp-lsp client process is unavailable for abrupt termination")
	}
	cmd := client.cmd
	client.cmd = nil
	_ = client.stdin.Close()
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill old mcp-lsp sidecar: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("abruptly terminated mcp-lsp sidecar exited successfully")
	}
}

func killGoplsDaemonProcessForE2E(t *testing.T, goplsPath, runtimeDir string, expected goplsDaemonProcess) {
	t.Helper()
	processes := requireGoplsDaemonProcesses(t, goplsPath, runtimeDir)
	if len(processes) != 1 || processes[0] != expected {
		t.Fatalf("refuse ambiguous gopls kill: got=%#v want exact runtime-owned process=%#v", processes, expected)
	}
	if err := syscall.Kill(expected.PID, 0); err != nil {
		t.Fatalf("exact residual gopls PID %d is not live before kill: %v", expected.PID, err)
	}
	process, err := os.FindProcess(expected.PID)
	if err != nil {
		t.Fatalf("find residual gopls daemon %d: %v", expected.PID, err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("kill residual gopls daemon %d: %v", expected.PID, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		remaining := requireGoplsDaemonProcesses(t, goplsPath, runtimeDir)
		if !slices.ContainsFunc(remaining, func(process goplsDaemonProcess) bool { return process.PID == expected.PID }) {
			if err := syscall.Kill(expected.PID, 0); !errors.Is(err, syscall.ESRCH) {
				t.Fatalf("exact residual gopls PID %d was not proven exited: %v", expected.PID, err)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("residual gopls daemon PID %d remained after kill: %#v", expected.PID, remaining)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func killAllGoplsDaemonProcessesForE2E(t *testing.T, goplsPath, runtimeDir string) {
	t.Helper()
	processes, err := listGoplsDaemonProcesses(goplsPath, runtimeDir)
	if err != nil {
		t.Errorf("list residual gopls daemons during cleanup: %v", err)
		return
	}
	for _, process := range processes {
		killGoplsDaemonProcessForE2E(t, goplsPath, runtimeDir, process)
	}
}

func assertGoplsDaemonCommandTimeout(t *testing.T, daemon goplsDaemonProcess) {
	t.Helper()
	if !strings.Contains(daemon.Command, "15m0s") {
		t.Fatalf("gopls daemon command = %q, want -listen.timeout 15m0s", daemon.Command)
	}
}

func assertRealGoplsLinkedWorktreeCohort(t *testing.T, goplsPath string, roots [2]string, env []string) {
	t.Helper()
	command := multilsp.ServerCommand{
		Executable: "gopls",
		Args: []string{
			"-remote=auto;sdmcp2",
			"-remote.listen.timeout=" + realGoplsRemoteListenTimeout.String(),
		},
	}
	first, err := runtimeServerGoplsRootCohortConfig(command, goplsPath, roots[0], env)
	if err != nil {
		t.Fatalf("derive linked worktree A gopls root cohort config: %v", err)
	}
	second, err := runtimeServerGoplsRootCohortConfig(command, goplsPath, roots[1], env)
	if err != nil {
		t.Fatalf("derive linked worktree B gopls root cohort config: %v", err)
	}
	if first != second {
		t.Fatalf("linked worktrees produced different gopls root cohort configs: A=%#v B=%#v", first, second)
	}
	t.Logf("linked worktrees share gopls root cohort proof: cohort=%s canonical_root_digest=%s", first.CohortID, first.RepositoryInstanceProof.CanonicalRootDigest)
}

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

func runRealGoplsGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	cmdArgs := append([]string{"-C", repository}, args...)
	cmd := exec.Command("git", cmdArgs...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v; output=%s", strings.Join(args, " "), err, output)
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
