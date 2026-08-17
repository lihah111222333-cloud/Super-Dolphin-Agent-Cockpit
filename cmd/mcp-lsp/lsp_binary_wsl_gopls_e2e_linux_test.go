//go:build linux && e2e

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
	"golang.org/x/sync/errgroup"
)

// TestMcpLSPBinaryWSLAllToolActionsCoverAllLSPClientLanguages_E2E 是 Windows
// 挂载盘到 WSL2 的全语言入口。语言集合由生产 adapter 动态导出；新增语言未添加夹具时
// 会 fail-fast。该测试复用完整 7 工具 action 矩阵，不以少量代表语言替代全覆盖。
// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWSLAllToolActionsCoverAllLSPClientLanguages_E2E(t *testing.T) {
	runMcpLSPBinaryWSLAllToolActionsShard(t, 0)
}

func TestMcpLSPBinaryWSLAllToolActionsShard2_E2E(t *testing.T) {
	runMcpLSPBinaryWSLAllToolActionsShard(t, 1)
}

func TestMcpLSPBinaryWSLAllToolActionsShard3_E2E(t *testing.T) {
	runMcpLSPBinaryWSLAllToolActionsShard(t, 2)
}

func TestMcpLSPBinaryWSLAllToolActionsShard4_E2E(t *testing.T) {
	runMcpLSPBinaryWSLAllToolActionsShard(t, 3)
}

func runMcpLSPBinaryWSLAllToolActionsShard(t *testing.T, shard int) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping WSL all-language semantic tool E2E in short mode")
	}
	requireWindowsMountedWSLWorkspace(t, repoRootForMcpLSPBinaryTest(t))
	binary := buildMcpLSPBinaryForTest(t)
	fakeServersBinDir := writeFakeMultilangDiagnosticsLangservers(t)
	for index, tc := range binaryColdStartLanguageCases(t) {
		if index%4 != shard {
			continue
		}
		t.Run(tc.languageID, func(t *testing.T) {
			runMcpLSPBinaryAllToolActionsForLanguageE2E(t, binary, fakeServersBinDir, tc)
		})
	}
}

// TestMcpLSPBinaryWSLToolsListPinsSevenShortNamesAndActions_E2E 锁定 Codex 可见的
// 7 个短名及 action schema，防止 handler 存在但 tools/list 漏暴露或旧名回流。
func TestMcpLSPBinaryWSLToolsListPinsSevenShortNamesAndActions_E2E(t *testing.T) {
	requireWindowsMountedWSLWorkspace(t, repoRootForMcpLSPBinaryTest(t))
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root := t.TempDir()
	client := startMcpLSPBinaryForTest(t, ctx, buildMcpLSPBinaryForTest(t), root, writeFakeMultilangDiagnosticsLangservers(t))
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	raw := callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{})
	var payload struct {
		Tools []struct {
			Name        string `json:"name"`
			InputSchema struct {
				Properties map[string]struct {
					Enum []string `json:"enum"`
				} `json:"properties"`
			} `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode WSL tools/list result: %v; raw=%s", err, raw)
	}
	wantActions := map[string][]string{
		"file":       {"open_file", "read_file", "diagnostics"},
		"inspect":    {"hover", "definition", "implementation", "type_definition", "signature_help"},
		"xref":       {"references", "call_hierarchy", "type_hierarchy"},
		"grep":       {"text_search", "ast_search"},
		"structure":  {"document_symbol", "workspace_symbol", "folding_range", "semantic_tokens"},
		"patch_edit": {"replace_range", "rename", "code_action", "format"},
		"completion": nil,
	}
	if len(payload.Tools) != len(wantActions) {
		t.Fatalf("WSL tools/list count = %d, want %d; raw=%s", len(payload.Tools), len(wantActions), raw)
	}
	for _, tool := range payload.Tools {
		want, ok := wantActions[tool.Name]
		if !ok {
			t.Fatalf("WSL tools/list exposed unexpected tool %q", tool.Name)
		}
		got := tool.InputSchema.Properties["action"].Enum
		if !slices.Equal(got, want) {
			t.Fatalf("WSL tool %s action enum = %#v, want %#v", tool.Name, got, want)
		}
	}
}

// TestMcpLSPBinaryWSLCodexConcurrentSemanticToolsShareRealGopls_E2E 锁定 Windows
// Codex 经 WSL2 在同一挂载工作区并发启动多个 stdio sidecar 时，真实 gopls cohort
// 不得互相推进为 stale workspace generation，也不得因 forwarder 退出产生进程身份失配。
// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWSLCodexConcurrentSemanticToolsShareRealGopls_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping WSL Codex concurrent real-gopls E2E in short mode")
	}
	if !runningUnderWSL(t) {
		t.Skip("WSL2 process and mounted-workspace semantics are required")
	}
	fixture := newWSLConcurrentGoplsFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	t.Cleanup(cancel)
	t.Cleanup(func() { cleanupWSLGoplsDaemonProcessesForE2E(t, fixture.goplsProcessPath, fixture.runtimeDir) })
	clients := startWSLConcurrentGoplsClients(t, ctx, fixture)
	requireWSLConcurrentStructureCalls(t, clients, fixture.target)
	requireWSLSemanticActions(t, clients[0], fixture.target)
	requireWSLGoplsRecovery(t, ctx, clients, fixture.binary, fixture.root, fixture.goplsPath, fixture.goplsProcessPath, fixture.runtimeDir, fixture.baseEnv, fixture.target)
	requireWSLClientLogsClean(t, clients)
}

type wslConcurrentGoplsFixture struct {
	goplsPath        string
	goplsProcessPath string
	root             string
	target           string
	binary           string
	runtimeDir       string
	baseEnv          []string
}

func newWSLConcurrentGoplsFixture(t *testing.T) wslConcurrentGoplsFixture {
	t.Helper()
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		t.Fatalf("gopls is required for WSL Codex concurrent E2E: %v", err)
	}
	goplsProcessPath, err := filepath.EvalSymlinks(goplsPath)
	if err != nil {
		t.Fatalf("resolve real gopls executable for WSL process evidence: %v", err)
	}
	root, target := writeWSLConcurrentGoplsWorkspace(t, repoRootForMcpLSPBinaryTest(t))
	runtimeDir, err := os.MkdirTemp("/tmp", "sd-wsl-")
	if err != nil {
		t.Fatalf("create short WSL gopls runtime dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Errorf("remove WSL gopls runtime dir: %v", err)
		}
	})
	baseEnv := []string{
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(runtimeDir, "lsp-resource"),
		"AGENT_LSP_GO_RSS_LIMIT_MB=384", "GOWORK=off",
		"MCP_LSP_IDLE_TIMEOUT=" + realGoplsRemoteListenTimeout.String(),
	}
	return wslConcurrentGoplsFixture{goplsPath: goplsPath, goplsProcessPath: goplsProcessPath, root: root, target: target, binary: buildMcpLSPBinaryForTest(t), runtimeDir: runtimeDir, baseEnv: baseEnv}
}

func writeWSLConcurrentGoplsWorkspace(t *testing.T, repositoryRoot string) (string, string) {
	t.Helper()
	workspaceParent := filepath.Join(repositoryRoot, ".workspace")
	if err := os.MkdirAll(workspaceParent, 0o700); err != nil {
		t.Fatalf("create mounted WSL E2E workspace parent: %v", err)
	}
	root, err := os.MkdirTemp(workspaceParent, "lsp-wsl-e2e-")
	if err != nil {
		t.Fatalf("create mounted WSL E2E workspace: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove mounted WSL E2E workspace: %v", err)
		}
	})
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/wsl-lsp-e2e\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatalf("write mounted WSL E2E go.mod: %v", err)
	}
	target := filepath.Join(root, "main.go")
	if err := os.WriteFile(target, []byte("package main\n\nfunc lifecycleValue() string { return \"ready\" }\n\nfunc main() { _ = lifecycleValue() }\n"), 0o600); err != nil {
		t.Fatalf("write mounted WSL E2E Go source: %v", err)
	}
	return root, target
}

func startWSLConcurrentGoplsClients(t *testing.T, ctx context.Context, fixture wslConcurrentGoplsFixture) []*mcpLSPBinaryClient {
	t.Helper()
	const clientCount = 3
	clients := make([]*mcpLSPBinaryClient, 0, clientCount)
	for range clientCount {
		client := startMcpLSPBinaryForTestWithEnv(t, ctx, fixture.binary, fixture.root, filepath.Dir(fixture.goplsPath), fixture.baseEnv)
		clients = append(clients, client)
		t.Cleanup(func() { client.close(t) })
		client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	}
	return clients
}

func requireWSLConcurrentStructureCalls(t *testing.T, clients []*mcpLSPBinaryClient, target string) {
	t.Helper()
	results := make([]mcpLSPBinaryResponse, len(clients))
	callErrors := make([]error, len(clients))
	var calls errgroup.Group
	for index, client := range clients {
		calls.Go(func() error {
			results[index], callErrors[index] = callMCPToolForConcurrentE2E(client, "structure", map[string]any{"action": "document_symbol", "file_path": target})
			return nil
		})
	}
	if err := calls.Wait(); err != nil {
		t.Fatalf("wait concurrent WSL semantic calls: %v", err)
	}
	for index, result := range results {
		if callErrors[index] != nil {
			t.Fatalf("concurrent WSL document_symbol client %d failed: %v; stderr=%s", index, callErrors[index], clients[index].stderrString())
		}
		requireMCPToolSuccess(t, clients[index], result, fmt.Sprintf("concurrent WSL document_symbol client %d", index))
	}
}

func requireWSLSemanticActions(t *testing.T, client *mcpLSPBinaryClient, target string) {
	t.Helper()
	checks := []struct {
		name string
		args map[string]any
	}{
		{name: "inspect", args: map[string]any{"action": "hover", "pos": target + ":3:6"}},
		{name: "xref", args: map[string]any{"action": "references", "pos": target + ":3:6"}},
		{name: "completion", args: map[string]any{"pos": target + ":5:16"}},
	}
	for _, check := range checks {
		result := client.callTool(t, check.name, check.args)
		requireMCPToolSuccess(t, client, result, "WSL Codex "+check.name)
	}
}

func requireWSLGoplsRecovery(t *testing.T, ctx context.Context, clients []*mcpLSPBinaryClient, binary, root, goplsPath, goplsProcessPath, runtimeDir string, baseEnv []string, target string) {
	t.Helper()
	clients[1].close(t)
	afterPeerClose := clients[0].callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": target})
	requireMCPToolSuccess(t, clients[0], afterPeerClose, "WSL peer remains usable after sibling sidecar shutdown")
	daemonBeforeRecovery := requireSingleGoplsDaemonProcess(t, goplsProcessPath, runtimeDir)
	killMcpLSPBinaryClientAbruptly(t, clients[2])
	replacement := startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, filepath.Dir(goplsPath), baseEnv)
	t.Cleanup(func() { replacement.close(t) })
	replacement.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	afterSidecarKill := replacement.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": target})
	requireMCPToolSuccess(t, replacement, afterSidecarKill, "WSL replacement sidecar reattaches after abrupt peer exit")
	daemonAfterReattach := requireSingleGoplsDaemonProcess(t, goplsProcessPath, runtimeDir)
	if daemonAfterReattach.PID != daemonBeforeRecovery.PID {
		t.Fatalf("WSL replacement sidecar changed healthy daemon PID: before=%d after=%d", daemonBeforeRecovery.PID, daemonAfterReattach.PID)
	}
	killWSLGoplsDaemonForE2E(t, goplsProcessPath, runtimeDir, daemonAfterReattach)
	afterDaemonKill := replacement.callTool(t, "structure", map[string]any{"action": "document_symbol", "file_path": target})
	requireMCPToolSuccess(t, replacement, afterDaemonKill, "WSL read-only request restarts killed gopls daemon")
	if daemonAfterRestart := requireSingleGoplsDaemonProcess(t, goplsProcessPath, runtimeDir); daemonAfterRestart.PID == daemonAfterReattach.PID {
		t.Fatalf("WSL gopls restart reused terminated PID %d", daemonAfterRestart.PID)
	}
}

func requireWSLClientLogsClean(t *testing.T, clients []*mcpLSPBinaryClient) {
	t.Helper()
	for index, client := range clients {
		stderr := client.stderrString()
		for _, forbidden := range []string{"stale workspace generation", "process-tree identity mismatch"} {
			if strings.Contains(stderr, forbidden) {
				t.Fatalf("WSL Codex client %d stderr contains %q: %s", index, forbidden, stderr)
			}
		}
	}
}

// TestMcpLSPBinaryWSLRepositoryRootColdStartRealGopls_E2E 锁定 Windows 挂载的
// 真实仓库根冷启动：首次全模块索引即使超过单请求预算，也不能杀死健康 transport；
// 新构建 sidecar 必须在不重启客户端的前提下复用索引进度并完成定义跳转。
// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWSLRepositoryRootColdStartRealGopls_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping WSL repository-root cold-start real-gopls E2E in short mode")
	}
	if !runningUnderWSL(t) {
		t.Skip("WSL2 mounted repository semantics are required")
	}
	goplsPath, err := exec.LookPath("gopls")
	if err != nil {
		t.Fatalf("gopls is required for WSL repository-root E2E: %v", err)
	}
	goplsProcessPath, err := filepath.EvalSymlinks(goplsPath)
	if err != nil {
		t.Fatalf("resolve real gopls executable: %v", err)
	}

	repositoryRoot := repoRootForMcpLSPBinaryTest(t)
	target := filepath.Join(repositoryRoot, "cmd", "mcp-lsp", "multilsp", "client.go")
	runtimeDir, err := os.MkdirTemp("/tmp", "sd-wsl-repository-root-")
	if err != nil {
		t.Fatalf("create WSL repository-root runtime dir: %v", err)
	}
	t.Cleanup(func() {
		cleanupWSLGoplsDaemonProcessesForE2E(t, goplsProcessPath, runtimeDir)
		if err := os.RemoveAll(runtimeDir); err != nil {
			t.Errorf("remove WSL repository-root runtime dir: %v", err)
		}
	})
	wrapperDir, forwarderLog, daemonLog := writeWSLGoplsLoggingWrapper(t, runtimeDir, goplsPath)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	t.Cleanup(cancel)
	client := startMcpLSPBinaryForTestWithEnv(t, ctx, buildMcpLSPBinaryForTest(t), repositoryRoot, wrapperDir, []string{
		"XDG_RUNTIME_DIR=" + runtimeDir,
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(runtimeDir, "lsp-resource"),
		"AGENT_LSP_GO_RSS_LIMIT_MB=384",
		"MCP_LSP_IDLE_TIMEOUT=" + realGoplsRemoteListenTimeout.String(),
	})
	t.Cleanup(func() { client.close(t) })
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})

	definitionArgs := map[string]any{
		"action": "definition",
		"pos":    target + ":365:25",
	}
	result := callWSLRepositoryRootDefinition(t, client, definitionArgs, target, repositoryRoot)
	t.Logf("gopls forwarder log:\n%s", readWSLE2ELogTail(t, forwarderLog))
	t.Logf("gopls daemon log:\n%s", readWSLE2ELogTail(t, daemonLog))
	requireMCPToolSuccess(t, client, result, "WSL repository-root cold-start definition")
	if !strings.Contains(result.Result.ContentText(), "prepareProcessTreeShutdown") {
		t.Fatalf("WSL repository-root definition missed target symbol: text=%q stderr=%s",
			result.Result.ContentText(), client.stderrString())
	}
}

func writeWSLGoplsLoggingWrapper(t *testing.T, runtimeDir, goplsPath string) (string, string, string) {
	t.Helper()
	forwarderLog := filepath.Join(runtimeDir, "gopls-forwarder.log")
	daemonLog := filepath.Join(runtimeDir, "gopls-daemon.log")
	wrapperDir := filepath.Join(runtimeDir, "bin")
	if err := os.MkdirAll(wrapperDir, 0o700); err != nil {
		t.Fatalf("create gopls logging wrapper dir: %v", err)
	}
	wrapperPath := filepath.Join(wrapperDir, "gopls")
	wrapper := fmt.Sprintf("#!/bin/sh\nexec %q -vv -logfile=%q -remote.logfile=%q \"$@\"\n", goplsPath, forwarderLog, daemonLog)
	if err := os.WriteFile(wrapperPath, []byte(wrapper), 0o700); err != nil {
		t.Fatalf("write gopls logging wrapper: %v", err)
	}
	return wrapperDir, forwarderLog, daemonLog
}

func callWSLRepositoryRootDefinition(t *testing.T, client *mcpLSPBinaryClient, args map[string]any, target, repositoryRoot string) mcpLSPBinaryResponse {
	t.Helper()
	result, err := callMCPToolForScopedE2E(client, "inspect", args, filepath.Dir(target), []string{repositoryRoot})
	if err != nil {
		t.Fatalf("call WSL repository-root definition: %v; stderr=%s", err, client.stderrString())
	}
	if mcpToolResultSucceeded(result) {
		return result
	}
	result, err = callMCPToolForScopedE2E(client, "inspect", args, filepath.Dir(target), []string{repositoryRoot})
	if err != nil {
		t.Fatalf("retry WSL repository-root definition without client restart: %v; stderr=%s", err, client.stderrString())
	}
	return result
}

func mcpToolResultSucceeded(result mcpLSPBinaryResponse) bool {
	if result.Error != nil || result.Result.IsError {
		return false
	}
	doc, err := lineprotocol.Parse(result.Result.ContentText())
	return err == nil && doc.Error == nil
}

func readWSLE2ELogTail(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("read %s: %v", path, err)
	}
	const maxLogBytes = 64 * 1024
	if len(raw) > maxLogBytes {
		raw = raw[len(raw)-maxLogBytes:]
	}
	return string(raw)
}

func cleanupWSLGoplsDaemonProcessesForE2E(t *testing.T, goplsPath, runtimeDir string) {
	t.Helper()
	for {
		processes := requireGoplsDaemonProcesses(t, goplsPath, runtimeDir)
		if len(processes) == 0 {
			return
		}
		if len(processes) != 1 {
			t.Errorf("refuse ambiguous WSL gopls cleanup: %#v", processes)
			return
		}
		killWSLGoplsDaemonForE2E(t, goplsPath, runtimeDir, processes[0])
	}
}

// killWSLGoplsDaemonForE2E 以精确 runtime/binary/PID 证据终止 daemon。
// WSL init 可能短暂保留 zombie PID，因此以 daemon 命令退出清单为终止边界。
func killWSLGoplsDaemonForE2E(t *testing.T, goplsPath, runtimeDir string, expected goplsDaemonProcess) {
	t.Helper()
	processes := requireGoplsDaemonProcesses(t, goplsPath, runtimeDir)
	if len(processes) != 1 || processes[0] != expected {
		t.Fatalf("refuse ambiguous WSL gopls kill: got=%#v want=%#v", processes, expected)
	}
	process, err := os.FindProcess(expected.PID)
	if err != nil {
		t.Fatalf("find WSL gopls daemon %d: %v", expected.PID, err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("kill WSL gopls daemon %d: %v", expected.PID, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		remaining := requireGoplsDaemonProcesses(t, goplsPath, runtimeDir)
		if len(remaining) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("WSL gopls daemon remained after kill: %#v", remaining)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestMcpLSPBinaryWSLLocalToolActions_E2E 覆盖不依赖语言服务器 RPC、但仍属于
// 7 工具公开契约的 text_search、ast_search 与 replace_range Linux/WSL 路径。
func TestMcpLSPBinaryWSLLocalToolActions_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping WSL local tool actions E2E in short mode")
	}
	if !runningUnderWSL(t) {
		t.Skip("WSL2 mounted-workspace semantics are required")
	}
	repositoryRoot := repoRootForMcpLSPBinaryTest(t)
	root, target, goTarget := writeWSLLocalToolFixture(t, filepath.Dir(repositoryRoot))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTest(t, ctx, buildMcpLSPBinaryForTest(t), root, "/usr/bin")
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	requireWSLLocalToolActions(t, client, target, goTarget)
}

func writeWSLLocalToolFixture(t *testing.T, workspaceParent string) (string, string, string) {
	t.Helper()
	root, err := os.MkdirTemp(workspaceParent, ".lsp-wsl-local-")
	if err != nil {
		t.Fatalf("create WSL local-actions workspace: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove WSL local-actions workspace: %v", err)
		}
	})
	target := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(target, []byte("pinnedValue=before\n"), 0o600); err != nil {
		t.Fatalf("write WSL local-actions fixture: %v", err)
	}
	goTarget := filepath.Join(root, "main.go")
	if err := os.WriteFile(goTarget, []byte("package main\n\nfunc pinnedValue() string { return \"before\" }\n"), 0o600); err != nil {
		t.Fatalf("write WSL AST fixture: %v", err)
	}
	if output, err := exec.Command("git", "init", "--quiet", root).CombinedOutput(); err != nil {
		t.Fatalf("initialize WSL local-actions Git repository: %v; output=%s", err, output)
	}
	return root, target, goTarget
}

func requireWSLLocalToolActions(t *testing.T, client *mcpLSPBinaryClient, target, goTarget string) {
	t.Helper()
	checks := []struct {
		label string
		tool  string
		args  map[string]any
		want  string
	}{
		{label: "text search", tool: "grep", args: map[string]any{"action": "text_search", "query": "pinnedValue", "path": target}, want: "pinnedValue"},
		{label: "AST search", tool: "grep", args: map[string]any{"action": "ast_search", "query": "func $F() string { $$$BODY }", "path": goTarget, "language": "go"}, want: "pinnedValue"},
		{label: "replace range", tool: "patch_edit", args: map[string]any{"action": "replace_range", "file_path": target, "patch": "@@\n-pinnedValue=before\n+pinnedValue=after\n"}, want: "applied"},
		{label: "read replaced file", tool: "file", args: map[string]any{"action": "read_file", "file_path": target}, want: "after"},
	}
	for _, check := range checks {
		result := client.callTool(t, check.tool, check.args)
		requireMCPToolSuccess(t, client, result, "WSL "+check.label)
		payload := result.Result.ContentText()
		if !strings.Contains(payload, check.want) {
			t.Fatalf("WSL %s payload missing %q: text=%q stderr=%s",
				check.label, check.want, result.Result.ContentText(), client.stderrString())
		}
	}
}

func runningUnderWSL(t *testing.T) bool {
	t.Helper()
	raw, err := os.ReadFile("/proc/sys/kernel/osrelease")
	if err != nil {
		t.Fatalf("read WSL kernel release: %v", err)
	}
	return strings.Contains(strings.ToLower(string(raw)), "microsoft")
}

func requireWindowsMountedWSLWorkspace(t *testing.T, root string) {
	t.Helper()
	if !runningUnderWSL(t) {
		t.Skip("WSL2 kernel semantics are required")
	}
	clean := filepath.ToSlash(filepath.Clean(root))
	parts := strings.Split(strings.TrimPrefix(clean, "/"), "/")
	if len(parts) < 3 || parts[0] != "mnt" || len(parts[1]) != 1 || parts[1][0] < 'a' || parts[1][0] > 'z' {
		t.Fatalf("WSL E2E workspace %q is not a Windows /mnt/<drive> mount", clean)
	}
}

func callMCPToolForConcurrentE2E(client *mcpLSPBinaryClient, name string, args map[string]any) (mcpLSPBinaryResponse, error) {
	return callMCPToolForScopedE2E(client, name, args, client.cmd.Dir, []string{client.cmd.Dir})
}
