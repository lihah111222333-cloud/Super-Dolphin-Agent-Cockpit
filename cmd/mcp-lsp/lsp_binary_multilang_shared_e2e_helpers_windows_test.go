//go:build e2e && windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func writeFakeMultilangDiagnosticsLangservers(t *testing.T) string {
	t.Helper()
	return writeFakeMultilangDiagnosticsLangserversWithBuilder(t, func(t *testing.T, target, _ string) {
		writeFakeWindowsMultilangGoplsExecutable(t, target, "")
	})
}

// startFakeMultilangDiagnosticsClientForTest 为 Windows gopls fake 使用合法的
// package-layout binary、bundle manifest 与 SHA256 链；其它语言继续复用 PATH fake。
func startFakeMultilangDiagnosticsClientForTest(t *testing.T, ctx context.Context, binary, root, fakeServersBinDir string, extraEnv []string, languageID string) *mcpLSPBinaryClient {
	t.Helper()
	if languageID != "go" && languageID != "gomod" && languageID != "gosum" && languageID != "gowork" {
		return startMcpLSPBinaryForTestWithEnv(t, ctx, binary, root, fakeServersBinDir, extraEnv)
	}
	bundle := writeFakeWindowsGoplsProtocolBundle(t, binary, languageID)
	productRoot := filepath.Clean(filepath.Join(bundle.bundleDir, "..", "..", ".."))
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict fake Windows gopls product root: %v", err)
	}
	cacheRoot := filepath.Join(productRoot, "lsp-cache")
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatalf("create fake Windows gopls shared cache root: %v", err)
	}
	if err := securefs.RestrictPrivateOwnerOnly(cacheRoot, 0o700); err != nil {
		t.Fatalf("restrict fake Windows gopls shared cache root: %v", err)
	}
	env := append([]string{
		"SUPER_DOLPHIN_LSP_BUNDLE_DIR=" + bundle.bundleDir,
		"SUPER_DOLPHIN_LSP_MANIFEST=" + bundle.manifestPath,
		"SUPER_DOLPHIN_HOME=" + productRoot,
		"AGENT_LSP_SHARED_CACHE_DIR=" + cacheRoot,
		"USERPROFILE=" + productRoot,
		"HOME=" + productRoot,
	}, bundle.extraEnv...)
	// writeFakeWindowsGoplsProtocolBundle also serves short-idle lifecycle tests;
	// this 42-ID matrix must use the production minimum idle contract.
	env = append(env, "MCP_LSP_IDLE_TIMEOUT=15m")
	env = append(env, extraEnv...)
	journalPath := fakeWindowsGoplsLifecycleJournalPath(env)
	if journalPath == "" {
		journalPath = filepath.Join(productRoot, "fake-gopls-lifecycle.jsonl")
		env = append(env, fakeMultilangLifecycleJournalEnv+"="+journalPath)
	}
	// 仅清理本次 bundle journal 明确记录的 fake gopls PID，避免依赖受限 CI
	// token 无法访问的 WMI 全量进程枚举，也不触碰生产进程。
	t.Cleanup(func() { cleanupFakeWindowsGoplsProcesses(t, journalPath) })
	t.Cleanup(func() { cleanupFakeWindowsGoplsBundleProcesses(t, bundle.binary) })
	return startWindowsGoplsMCPBinaryForTest(t, ctx, bundle.binary, root, fakeServersBinDir, env)
}

func fakeWindowsGoplsLifecycleJournalPath(env []string) string {
	prefix := fakeMultilangLifecycleJournalEnv + "="
	for _, value := range env {
		if strings.HasPrefix(value, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(value, prefix))
		}
	}
	return ""
}

func cleanupFakeWindowsGoplsProcesses(t *testing.T, journalPath string) {
	t.Helper()
	payload, err := os.ReadFile(journalPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			t.Logf("read fake Windows gopls lifecycle journal %s: %v", journalPath, err)
		}
		return
	}
	pids := make(map[int]struct{})
	for _, line := range bytes.Split(payload, []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var entry fakeMultilangLifecycleJournalEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Logf("decode fake Windows gopls lifecycle journal %s: %v", journalPath, err)
			continue
		}
		if entry.Server == "gopls" && entry.PID > 0 {
			pids[entry.PID] = struct{}{}
		}
	}
	for pid := range pids {
		process, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Logf("kill fake Windows gopls PID %d: %v", pid, err)
		}
	}
}

func cleanupFakeWindowsGoplsBundleProcesses(t *testing.T, binary string) {
	t.Helper()
	quotedBinary := strings.ReplaceAll(filepath.Clean(binary), "'", "''")
	// 无匹配进程表示目标唯一版本已正常退出；显式返回成功，避免 PowerShell
	// 把空管道误记成清理失败。路径精确匹配仍保证不会终止其他版本。
	script := "$binary='" + quotedBinary + "'; Get-Process -Name mcp-lsp -ErrorAction SilentlyContinue | Where-Object { $_.Path -and $_.Path -eq $binary } | Stop-Process -Force -ErrorAction SilentlyContinue; exit 0"
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Logf("cleanup fake Windows gopls bundle process %s: %v; output=%s", binary, err, output)
	}
}

func writeFakeMultilangDiagnosticsLangserversWithBuilder(t *testing.T, build func(*testing.T, string, string)) string {
	t.Helper()
	dir := t.TempDir()
	genericPath := filepath.Join(dir, "generic-fake-multilang.exe")
	build(t, genericPath, "")
	payload, err := os.ReadFile(genericPath)
	if err != nil {
		t.Fatalf("read generic fake Windows multilang executable: %v", err)
	}
	for _, name := range fakeMultilangDiagnosticsLangserverNames {
		for _, executablePath := range []string{
			filepath.Join(dir, name+".exe"),
			filepath.Join(dir, name),
		} {
			if err := os.WriteFile(executablePath, payload, 0o700); err != nil {
				t.Fatalf("copy fake %s Windows executable: %v", name, err)
			}
		}
	}
	return dir
}

func TestWriteFakeMultilangDiagnosticsLangserversBuildsOnce(t *testing.T) {
	var buildCount int
	dir := writeFakeMultilangDiagnosticsLangserversWithBuilder(t, func(t *testing.T, target, _ string) {
		buildCount++
		if err := os.WriteFile(target, []byte("fake executable"), 0o700); err != nil {
			t.Fatalf("write fake executable: %v", err)
		}
	})
	if buildCount != 1 {
		t.Fatalf("fake Windows multilang executable build count = %d, want 1", buildCount)
	}
	for _, name := range fakeMultilangDiagnosticsLangserverNames {
		for _, path := range []string{filepath.Join(dir, name+".exe"), filepath.Join(dir, name)} {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("stat fake %s Windows executable %s: %v", name, path, err)
			}
		}
	}
}
