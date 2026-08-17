//go:build windows && arm64 && e2e

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

const (
	nativeCatalogClangdColdDiagnosticEnv      = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_CLANGD_C_COLD_DIAGNOSTIC"
	nativeCatalogClangdColdDiagnosticRootEnv  = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_CLANGD_C_COLD_ROOT"
	nativeCatalogClangdColdDiagnosticEvidence = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_CLANGD_C_COLD_EVIDENCE_DIR"
	nativeCatalogClangdColdDiagnosticOnlyEnv  = "MCP_LSP_WINDOWS_ARM64_PROCESS_ARM64_NATIVE_CATALOG_CLANGD_C_COLD_ONLY_CLANGD"
)

// TestWindowsARM64ProcessARM64NativeCatalogClangdCColdDiagnosticE2E 只诊断冷安装后
// clangd 的 C stdio 边界；它不是生命周期证明，receipt 必须保持 NON_PASS_DIAGNOSTIC_NOT_LIFECYCLE。
func TestWindowsARM64ProcessARM64NativeCatalogClangdCColdDiagnosticE2E(t *testing.T) {
	if os.Getenv(nativeCatalogClangdColdDiagnosticEnv) != "1" {
		t.Skipf("set %s=1 for the bounded cold clangd diagnostic", nativeCatalogClangdColdDiagnosticEnv)
	}
	if runtime.GOOS != "windows" || runtime.GOARCH != "arm64" {
		t.Fatalf("cold clangd diagnostic requires windows/arm64, got %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	repoRoot := realNodeRepoRoot(t)
	root := strings.TrimSpace(os.Getenv(nativeCatalogClangdColdDiagnosticRootEnv))
	ownedRoot := root == ""
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "sd-node-production-windows-native-catalog-clangd-c-cold-")
		if err != nil {
			t.Fatalf("create cold diagnostic product root: %v", err)
		}
	}
	if ownedRoot {
		t.Cleanup(func() {
			if err := removeRealWindowsProductRoot(root); err != nil {
				t.Errorf("remove cold diagnostic product root: %v", err)
			}
		})
	}
	if err := securefs.RestrictPrivateOwnerOnly(root, 0o700); err != nil {
		t.Fatalf("restrict cold diagnostic product root: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", root)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRoot)
	t.Setenv("APPDATA", "")
	provider := setupInstaller()
	installLines := []string{"status=NON_PASS_DIAGNOSTIC_NOT_LIFECYCLE", "formal_lifecycle=not_run", "native_arch=arm64", "process_arch=arm64"}
	languages := nativeCatalog15x36LanguageIDs
	if os.Getenv(nativeCatalogClangdColdDiagnosticOnlyEnv) == "1" {
		languages = []string{"c"}
	}
	for _, languageID := range languages {
		if ctx.Err() != nil {
			t.Fatalf("cold diagnostic install deadline: %v", ctx.Err())
		}
		result, err := provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), languageID)
		if err != nil {
			t.Fatalf("cold diagnostic install %s: %v", languageID, err)
		}
		installLines = append(installLines, fmt.Sprintf("install.%s=%s", languageID, result.Status))
	}
	evidenceDir := strings.TrimSpace(os.Getenv(nativeCatalogClangdColdDiagnosticEvidence))
	if evidenceDir == "" {
		evidenceDir = filepath.Join(repoRoot, ".build-cache", "lsp-test-results", "windows-arm64-process-arm64-native-catalog-clangd-c-cold-diagnostic")
	}
	if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
		t.Fatalf("create cold diagnostic evidence directory: %v", err)
	}
	receiptPath := filepath.Join(evidenceDir, "windows-arm64-process-arm64-native-catalog-clangd-c-cold-diagnostic.receipt")
	writeReceipt := func(lines []string) {
		if err := node17x36WriteReceipt(receiptPath, append(installLines, lines...)); err != nil {
			t.Errorf("write cold diagnostic receipt: %v", err)
		}
	}
	binary := buildRealMcpLSPBinary(t, repoRoot)
	fixtureRoot := t.TempDir()
	fixture := writeRealMCPLanguageFixture(t, fixtureRoot, nativeCatalog15x36ServerCases()[0])
	results := make([]string, 0, 3)
	for trial := 1; trial <= 3; trial++ {
		if ctx.Err() != nil {
			break
		}
		clientCtx, clientCancel := context.WithTimeout(ctx, 3*time.Minute)
		client := startRealMcpLSPBinary(t, clientCtx, binary, fixtureRoot, repoRoot, "", "", root)
		pid := client.cmd.Process.Pid
		start, startErr := windowsGoplsProcessStartIdentity(pid)
		tracked := map[realMCPProcessKey]realMCPProcessIdentity{{PID: pid, StartToken: start}: {PID: pid, StartToken: start, Name: "mcp-lsp", Language: "c-cold"}}
		status := "runtime_failure"
		initialize := client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}})
		if initialize.Error == nil && nativeCatalog15x36Notify(client, "notifications/initialized", map[string]any{}) == nil {
			open := client.callTool(t, "file", realMCPWindowsToolArguments("c", fixtureRoot, "file", "open_file", map[string]any{"action": "open_file", "file_path": fixture.targetFile}))
			if open.Error == nil && strings.TrimSpace(open.Result.ContentText()) != "" {
				status = "semantic_success"
			}
		}
		exitSent := node17x36CloseWithExitProof(t, client)
		requireRealMCPProcessIdentitiesGone(t, tracked)
		clientCancel()
		results = append(results, fmt.Sprintf("trial.%d=%s;pid=%d;start_captured=%t;exit_sent=%t", trial, status, pid, startErr == nil, exitSent))
		if status == "runtime_failure" {
			break
		}
	}
	writeReceipt(append([]string{"trials=" + fmt.Sprint(len(results))}, results...))
	t.Logf("cold clangd diagnostic receipt=%s status=NON_PASS_DIAGNOSTIC_NOT_LIFECYCLE", receiptPath)
}
