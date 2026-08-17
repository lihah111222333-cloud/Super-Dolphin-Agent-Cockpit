//go:build windows && e2e

package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestMcpLSPBinaryWindowsAllNativeArchitecturesProductionAutoInstallerToolsListE2E
// 在当前 Windows NativeArch（ARM64、x64 或 x86）启动真实 sidecar，并证明空 PATH、
// 无 bundle 时仍由 production 自动安装器公开精确七个工具族。该短测只证明清单门控，
// 不替代任一语言的 36-action 或十五分钟生命周期证明。
func TestMcpLSPBinaryWindowsAllNativeArchitecturesProductionAutoInstallerToolsListE2E(t *testing.T) {
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows native/process architecture: %v", err)
	}
	switch host.NativeArch {
	case installer.WindowsHostArchARM64, installer.WindowsHostArchX64, installer.WindowsHostArchX86:
	default:
		t.Fatalf("unsupported Windows NativeArch %q", host.NativeArch)
	}

	repoRoot := realNodeRepoRoot(t)
	binary := buildRealMcpLSPBinary(t, repoRoot)
	productRoot, err := os.MkdirTemp("", "sd-node-production-windows-"+host.NativeArch+"-tools-list-")
	if err != nil {
		t.Fatalf("create Windows production tools/list product root: %v", err)
	}
	t.Cleanup(func() {
		if err := removeRealWindowsProductRoot(productRoot); err != nil {
			t.Errorf("cleanup Windows production tools/list product root: %v", err)
		}
	})
	if err := securefs.RestrictPrivateOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict Windows production tools/list product root: %v", err)
	}
	requireWindowsProductionToolsListProductRootEmpty(t, productRoot, "before sidecar start")

	fixtureRoot := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", "")
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", "")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := startRealMcpLSPBinary(t, ctx, binary, fixtureRoot, repoRoot, "", "", productRoot)
	mcpPID := client.cmd.Process.Pid
	tracked := make(map[realMCPProcessKey]realMCPProcessIdentity)
	defer func() {
		if client != nil && client.cmd != nil {
			trackRealMCPProcessTree(t, mcpPID, "tools/list-deferred-close", tracked)
			client.close(t)
		}
		requireRealMCPProcessIdentitiesGone(t, tracked)
	}()
	mcpStart, err := windowsGoplsProcessStartIdentity(mcpPID)
	if err != nil {
		t.Fatalf("capture Windows production tools/list sidecar start identity PID %d: %v", mcpPID, err)
	}
	tracked[realMCPProcessKey{PID: mcpPID, StartToken: mcpStart}] = realMCPProcessIdentity{
		PID: mcpPID, StartToken: mcpStart, Name: "mcp-lsp", Language: "tools/list-only",
	}

	initialize := client.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "super-dolphin-windows-all-native-architectures-tools-list-e2e",
			"version": "1",
		},
	})
	if initialize.Error != nil {
		t.Fatalf("Windows production tools/list initialize error: %v", initialize.Error)
	}
	requireRealMCPToolFamilies(t, callMcpLSPBinaryRaw(t, client, "tools/list", map[string]any{}))
	if !trackRealMCPProcessTree(t, mcpPID, "tools/list-only", tracked) {
		t.Fatal("Windows production tools/list process-tree snapshot failed")
	}
	if len(tracked) != 1 {
		logRealMCPProcessIdentities(t, tracked)
		t.Fatalf("Windows production tools/list started semantic installer/server descendants; tracked_processes=%d, want only sidecar owner", len(tracked))
	}
	requireWindowsProductionToolsListProductRootEmpty(t, productRoot, "after tools/list")
	shutdown := client.call(t, "shutdown", map[string]any{})
	if shutdown.Error != nil {
		t.Fatalf("Windows production tools/list shutdown error: %v", shutdown.Error)
	}
	client.close(t)
	requireRealMCPProcessIdentitiesGone(t, tracked)
	requireWindowsProductionToolsListProductRootEmpty(t, productRoot, "after sidecar close")

	stderr := client.stderrString()
	if !strings.Contains(stderr, "windows_production_auto_installer") {
		t.Fatalf("Windows production tools/list did not log its auto-installer visibility source; stderr_bytes=%d", len(stderr))
	}
	t.Logf("Windows tools/list auto-installer proof native_arch=%s process_arch=%s windows_build=%d", host.NativeArch, host.ProcessArch, host.WindowsBuild)
}

// requireWindowsProductionToolsListProductRootEmpty 锁定短清单门控的共享语义：
// tools/list 只能证明 Windows 自动安装器可用，不能下载、落盘或启动任一语言服务器。
func requireWindowsProductionToolsListProductRootEmpty(t *testing.T, productRoot, phase string) {
	t.Helper()
	entries, err := os.ReadDir(productRoot)
	if err != nil {
		t.Fatalf("inspect Windows production tools/list product root at %s: %v", phase, err)
	}
	if len(entries) == 0 {
		return
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	t.Fatalf("Windows production tools/list wrote product-root entries at %s: %v", phase, names)
}
