//go:build windows && e2e

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common/lineprotocol"
)

const windowsHostWSLFullMatrixTimeout = 45 * time.Minute

func TestWindowsHostWSLFullMatrixTimeoutExceedsTenMinutes(t *testing.T) {
	if windowsHostWSLFullMatrixTimeout <= 10*time.Minute {
		t.Fatalf("Windows-host WSL full matrix timeout = %s, want greater than 10 minutes", windowsHostWSLFullMatrixTimeout)
	}
}

// TestMcpLSPBinaryWindowsHostOrchestratesWSL2AllLanguagesFullMatrix_E2E 由 native
// Windows 测试进程经 wsl.exe 启动 Linux 专用全语言/全 action 与生命周期测试。
// 内层测试动态绑定生产 RequiresLSPClient 集合；新增 adapter 未补夹具会 fail-fast。
// super-dolphin-ci: compile-group-exclusive
func TestMcpLSPBinaryWindowsHostOrchestratesWSL2AllLanguagesFullMatrix_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Windows-host WSL2 full language matrix in short mode")
	}
	wslPath, err := exec.LookPath("wsl.exe")
	if err != nil {
		t.Fatalf("Windows-host WSL2 full matrix requires wsl.exe: %v", err)
	}
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	wslRepoRoot := windowsPathToWSLForE2E(t, wslPath, repoRoot)
	wslPATH := discoverWSLPathForE2E(t, wslPath)
	ctx, cancel := context.WithTimeout(context.Background(), windowsHostWSLFullMatrixTimeout)
	defer cancel()
	innerPattern := "^(TestMcpLSPBinaryWSLAllToolActionsCoverAllLSPClientLanguages_E2E|" +
		"TestMcpLSPBinaryWSLToolsListPinsSevenShortNamesAndActions_E2E|" +
		"TestMcpLSPBinaryWSLLocalToolActions_E2E|" +
		"TestMcpLSPBinaryAllLanguageLifecycleSingleSidecar_E2E|" +
		"TestMcpLSPBinaryMultilangIdleLeaseIsolationPrecheck_E2E)$"
	cmd := exec.CommandContext(ctx, wslPath,
		"--cd", wslRepoRoot,
		"--exec",
		"env", "PATH="+wslPATH,
		"go", "test", "-tags=e2e", "./cmd/mcp-lsp",
		"-run", innerPattern, "-count=1", "-timeout=40m", "-v",
	)
	output, runErr := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("Windows-host WSL2 full matrix exceeded %s: %v\n%s", windowsHostWSLFullMatrixTimeout, ctx.Err(), output)
	}
	if runErr != nil {
		t.Fatalf("Windows-host WSL2 full matrix failed: %v\n%s", runErr, output)
	}
	text := string(output)
	for _, languageID := range defaultBinaryLSPClientLanguageIDs(t) {
		marker := "--- PASS: TestMcpLSPBinaryWSLAllToolActionsCoverAllLSPClientLanguages_E2E/" + languageID
		if !strings.Contains(text, marker) {
			t.Fatalf("Windows-host WSL2 output lacks language PASS marker %q\n%s", marker, text)
		}
	}
	for _, testName := range []string{
		"TestMcpLSPBinaryWSLToolsListPinsSevenShortNamesAndActions_E2E",
		"TestMcpLSPBinaryWSLLocalToolActions_E2E",
		"TestMcpLSPBinaryAllLanguageLifecycleSingleSidecar_E2E",
		"TestMcpLSPBinaryMultilangIdleLeaseIsolationPrecheck_E2E",
	} {
		if !strings.Contains(text, "--- PASS: "+testName) {
			t.Fatalf("Windows-host WSL2 output lacks PASS marker for %s\n%s", testName, text)
		}
	}
	t.Logf("Windows-host WSL2 full matrix passed:\n%s", text)
}

// TestMcpLSPBinaryWindowsHostLaunchesWSL2LinuxSidecar_E2E 从 native Windows
// 测试进程通过 wsl.exe 启动刚编译的 Linux ELF，锁定 host cwd 与 sidecar root
// 分层、显式 Linux PATH、stdio MCP 以及真实 gopls 语义调用。
func TestMcpLSPBinaryWindowsHostLaunchesWSL2LinuxSidecar_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Windows-host WSL2 bridge E2E in short mode")
	}
	wslPath, err := exec.LookPath("wsl.exe")
	if err != nil {
		t.Fatalf("Windows-host WSL2 E2E requires wsl.exe: %v", err)
	}
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	wslRepoRoot := windowsPathToWSLForE2E(t, wslPath, repoRoot)
	if !strings.HasPrefix(wslRepoRoot, "/mnt/") {
		t.Fatalf("Windows repository %q maps to non-mounted WSL path %q", repoRoot, wslRepoRoot)
	}

	workspace, target := createWindowsHostWSLWorkspace(t, repoRoot)
	wslWorkspace := windowsPathToWSLForE2E(t, wslPath, workspace)
	wslTarget := windowsPathToWSLForE2E(t, wslPath, target)
	wslRuntimeDir := createPrivateWSLRuntimeDirForE2E(t, wslPath)
	wslCacheDir := wslRuntimeDir + "/lsp-cache"
	if output, err := exec.Command(wslPath, "--", "mkdir", "-m", "0700", wslCacheDir).CombinedOutput(); err != nil {
		t.Fatalf("create isolated WSL cache directory: %v\n%s", err, output)
	}

	binary := resolveWindowsHostWSLBinary(t, repoRoot)
	wslBinary := windowsPathToWSLForE2E(t, wslPath, binary)
	if output, err := exec.Command(wslPath, "--", "chmod", "+x", wslBinary).CombinedOutput(); err != nil {
		t.Fatalf("mark WSL sidecar executable: %v\n%s", err, output)
	}
	wslPATH := discoverWSLPathForE2E(t, wslPath)
	if output, err := exec.Command(wslPath, "--", "env", "PATH="+wslPATH, "sh", "-c", "command -v gopls >/dev/null").CombinedOutput(); err != nil {
		t.Fatalf("WSL PATH does not expose gopls: %v\n%s", err, output)
	}

	rootsJSON, err := json.Marshal([]string{wslWorkspace})
	if err != nil {
		t.Fatalf("marshal WSL roots: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	client := startMcpLSPBinaryForTestWithEnvConfigured(
		t, ctx, wslPath, workspace, "", nil,
		func(cmd *exec.Cmd) {
			cmd.Args = []string{
				wslPath, "--cd", wslWorkspace, "env",
				"SUPER_DOLPHIN_RUNTIME_MODE=dev",
				"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=" + wslRepoRoot,
				"SUPER_DOLPHIN_DEPENDENCY_PROFILE=production",
				"GO_AGENT_LSP_ROOT=" + wslWorkspace,
				"GO_AGENT_LSP_ROOTS=" + string(rootsJSON),
				"GO_AGENT_PEER_MODE=0",
				"XDG_RUNTIME_DIR=" + wslRuntimeDir,
				"AGENT_LSP_SHARED_CACHE_DIR=" + wslCacheDir,
				"PATH=" + wslPATH,
				wslBinary,
			}
		},
		func(*exec.Cmd) (func() error, error) { return nil, nil },
	)
	defer client.close(t)
	client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"})
	assertWindowsHostWSLToolsListContract(t, client)

	requireWindowsHostWSLSemantics(t, client, wslWorkspace, wslTarget)
	requireWindowsHostWSLRejectsNativeFilePath(t, client, target, wslWorkspace)
}

func createWindowsHostWSLWorkspace(t *testing.T, repoRoot string) (string, string) {
	t.Helper()
	workspace, err := os.MkdirTemp(repoRoot, ".windows-host-wsl-e2e-")
	if err != nil {
		t.Fatalf("create Windows-host WSL workspace: %v", err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(10 * time.Second)
		for {
			err := os.RemoveAll(workspace)
			if err == nil || !time.Now().Before(deadline) {
				if err != nil {
					t.Errorf("remove Windows-host WSL workspace after sidecar/daemon drain: %v", err)
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	writeBinaryColdStartFile(t, workspace, "go.mod", "module example.com/windows-host-wsl\n\ngo 1.25.0\n")
	target := writeBinaryColdStartFile(t, workspace, "main.go", "package bridge\n\nfunc BridgeSymbol() string { return \"bridge-pinned\" }\n")
	return workspace, target
}

func resolveWindowsHostWSLBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	binary := strings.TrimSpace(os.Getenv("MCP_LSP_E2E_LINUX_BINARY"))
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "mcp-lsp-linux-x86")
		build := exec.Command("go", "build", "-buildvcs=false", "-o", binary, "./cmd/mcp-lsp")
		build.Dir = repoRoot
		build.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("cross-build Linux mcp-lsp from Windows: %v\n%s", err, output)
		}
		return binary
	}
	if !filepath.IsAbs(binary) {
		t.Fatalf("MCP_LSP_E2E_LINUX_BINARY must be absolute: %q", binary)
	}
	if info, err := os.Stat(binary); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("MCP_LSP_E2E_LINUX_BINARY is not a regular file: %q info=%v err=%v", binary, info, err)
	}
	return binary
}

func requireWindowsHostWSLSemantics(t *testing.T, client *mcpLSPBinaryClient, wslWorkspace, wslTarget string) {
	t.Helper()
	checks := []struct {
		tool string
		args map[string]any
		want string
	}{
		{"file", map[string]any{"action": "read_file", "file_path": wslTarget}, "BridgeSymbol"},
		{"grep", map[string]any{"action": "text_search", "query": "bridge-pinned", "paths": []string{wslTarget}}, "bridge-pinned"},
		{"structure", map[string]any{"action": "document_symbol", "file_path": wslTarget}, "BridgeSymbol"},
	}
	for _, check := range checks {
		result, err := callMCPToolForScopedE2E(client, check.tool, check.args, wslWorkspace, []string{wslWorkspace})
		if err != nil {
			t.Fatalf("Windows-host WSL %s call: %v; stderr=%s", check.tool, err, client.stderrString())
		}
		requireMCPToolSuccess(t, client, result, "Windows-host WSL "+check.tool)
		payload := result.Result.ContentText()
		if !strings.Contains(payload, check.want) {
			t.Fatalf("Windows-host WSL %s missing %q: text=%q", check.tool, check.want, result.Result.ContentText())
		}
	}
}

func requireWindowsHostWSLRejectsNativeFilePath(t *testing.T, client *mcpLSPBinaryClient, target, wslWorkspace string) {
	t.Helper()
	wrongPath, err := callMCPToolForScopedE2E(client, "file", map[string]any{"action": "read_file", "file_path": target}, wslWorkspace, []string{wslWorkspace})
	if err != nil {
		t.Fatalf("call Windows-path rejection probe: %v", err)
	}
	if !wrongPath.Result.IsError {
		t.Fatalf("Linux sidecar accepted native Windows path %q; text=%q stderr=%s", target, wrongPath.Result.ContentText(), client.stderrString())
	}
	doc, err := lineprotocol.Parse(wrongPath.Result.ContentText())
	if err != nil || doc.Error == nil {
		t.Fatalf("parse Windows-path rejection line protocol: err=%v doc=%#v text=%q", err, doc, wrongPath.Result.ContentText())
	}
	if doc.Error.Code != "path_outside_workspace" {
		t.Fatalf("Windows-path rejection code = %q, want path_outside_workspace; text=%q", doc.Error.Code, wrongPath.Result.ContentText())
	}
}

func createPrivateWSLRuntimeDirForE2E(t *testing.T, wslPath string) string {
	t.Helper()
	output, err := exec.Command(wslPath, "--", "mktemp", "-d", "/tmp/sdmcp-windows-host-wsl-e2e.XXXXXX").CombinedOutput()
	if err != nil {
		t.Fatalf("create private WSL runtime directory: %v\n%s", err, output)
	}
	dir := strings.TrimSpace(string(output))
	if !strings.HasPrefix(dir, "/tmp/sdmcp-windows-host-wsl-e2e.") || strings.ContainsAny(dir, "\r\n\x00") {
		t.Fatalf("mktemp returned unsafe WSL runtime path %q", dir)
	}
	if output, err := exec.Command(wslPath, "--", "chmod", "0700", dir).CombinedOutput(); err != nil {
		t.Fatalf("secure WSL runtime directory %q: %v\n%s", dir, err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command(wslPath, "--", "rm", "-rf", "--", dir).CombinedOutput(); err != nil {
			t.Errorf("remove private WSL runtime directory %q: %v\n%s", dir, err, output)
		}
	})
	return dir
}

func assertWindowsHostWSLToolsListContract(t *testing.T, client *mcpLSPBinaryClient) {
	t.Helper()
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
		t.Fatalf("decode Windows-host WSL tools/list: %v; raw=%s", err, raw)
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
		t.Fatalf("Windows-host WSL tools/list count = %d, want %d; raw=%s", len(payload.Tools), len(wantActions), raw)
	}
	for _, tool := range payload.Tools {
		want, ok := wantActions[tool.Name]
		if !ok {
			t.Fatalf("Windows-host WSL tools/list exposed unexpected tool %q", tool.Name)
		}
		if got := tool.InputSchema.Properties["action"].Enum; !slices.Equal(got, want) {
			t.Fatalf("Windows-host WSL tool %s action enum = %#v, want %#v", tool.Name, got, want)
		}
	}
}

func windowsPathToWSLForE2E(t *testing.T, wslPath, path string) string {
	t.Helper()
	// wsl.exe 会把未转义的反斜杠再次交给 Linux argv 解析；先使用 Windows 同样
	// 接受的 drive/forward-slash 形式，避免 C:\x 被压成 C:x。
	windowsArg := filepath.ToSlash(path)
	output, err := exec.Command(wslPath, "--", "wslpath", "-a", "-u", windowsArg).CombinedOutput()
	if err != nil {
		t.Fatalf("map Windows path %q into WSL: %v\n%s", path, err, output)
	}
	mapped := strings.TrimSpace(string(output))
	if mapped == "" {
		t.Fatalf("wslpath returned empty mapping for %q", path)
	}
	return mapped
}

func discoverWSLPathForE2E(t *testing.T, wslPath string) string {
	t.Helper()
	output, err := exec.Command(wslPath, "--", "bash", "-lc", `printf '%s' "$PATH"`).CombinedOutput()
	if err != nil {
		t.Fatalf("discover WSL login PATH: %v\n%s", err, output)
	}
	path := strings.TrimSpace(string(output))
	if path == "" || strings.Contains(path, "$HOME") {
		t.Fatalf("WSL PATH is empty or contains an unexpanded home token: %q", path)
	}
	dirs := make([]string, 0, 3)
	for _, executable := range []string{"go", "gopls"} {
		resolved, err := exec.Command(wslPath, "--", "bash", "-lc", "command -v "+executable).CombinedOutput()
		if err != nil {
			t.Fatalf("discover WSL %s: %v\n%s", executable, err, resolved)
		}
		dir := strings.TrimSpace(string(resolved))
		if dir == "" || !strings.HasPrefix(dir, "/") {
			t.Fatalf("WSL %s resolved to non-absolute path %q", executable, dir)
		}
		dirs = append(dirs, filepath.ToSlash(filepath.Dir(dir)))
	}
	dirs = append(dirs, path)
	return strings.Join(dirs, ":")
}
