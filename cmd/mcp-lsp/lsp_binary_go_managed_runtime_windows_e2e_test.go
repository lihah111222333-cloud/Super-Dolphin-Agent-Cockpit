//go:build windows && e2e

package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// TestWindowsMcpLSPBinaryGoManagedRuntimeWithoutGoPATH_E2E 锁定真实生产启动契约：
// Go 产品由 Windows 安装器预检并进入产品缓存后，sidecar 仍必须在 PATH 为空时完成 Go LSP 请求。
func TestWindowsMcpLSPBinaryGoManagedRuntimeWithoutGoPATH_E2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Windows managed Go runtime e2e test in short mode")
	}

	alignWindowsProductRootForE2E(t)
	productRoot := filepath.Join(t.TempDir(), "product-root")
	if err := os.MkdirAll(productRoot, 0o700); err != nil {
		t.Fatalf("create managed Go product root: %v", err)
	}
	if err := securefs.RestrictOwnerOnly(productRoot, 0o700); err != nil {
		t.Fatalf("restrict managed Go product root ACL: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
	provider, err := setupInstallerWithError()
	if err != nil {
		t.Fatalf("setup Windows product installer for managed Go e2e: %v", err)
	}
	config, ok := provider.ConfigForLanguage("go")
	if !ok || config.InstalledBinaryPathResolver == nil || config.InstallAction == nil {
		t.Fatalf("production Go installer is incomplete: configured=%v resolver=%v action=%v", ok, config.InstalledBinaryPathResolver != nil, config.InstallAction != nil)
	}
	managedGOROOT := provisionManagedGoProductFixtureForE2E(t, productRoot)

	root := repoRootForMcpLSPBinaryTest(t)
	binary := prepareManagedGoPackageForE2E(t, buildMcpLSPBinaryForTest(t), productRoot, root)
	target := writeManagedGoLocalFixtureForE2E(t, root)
	cacheRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
	resolved, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductGoGopls, cacheRoot)
	if err != nil {
		t.Fatalf("resolve managed Go product after provisioning: %v", err)
	}
	goVersion, versionErr := exec.Command(resolved.ExecutablePath, "version").CombinedOutput()
	t.Logf("managed Go resolve: executable=%s server=%s version=%q exit_error=%v", resolved.ExecutablePath, resolved.ServerPath, strings.TrimSpace(string(goVersion)), versionErr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	client := startWindowsGoplsMCPBinaryForTest(t, ctx, binary, root, t.TempDir(), []string{
		"PATH=",
		"SUPER_DOLPHIN_HOME=" + productRoot,
		"PROJECT_ROOT=",
		"SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=" + root,
		"USERPROFILE=" + productRoot,
		"HOME=" + productRoot,
		"AGENT_LSP_SHARED_CACHE_DIR=" + filepath.Join(productRoot, "cache", "lsp-shared"),
		"GOROOT=" + managedGOROOT,
		"GOTOOLCHAIN=local",
		"GOPROXY=off",
		"MCP_LSP_TRACE_TIMING=1",
	})
	defer client.close(t)
	packagedServerPath := filepath.Join(filepath.Dir(binary), "lsp", "bin", "gopls.exe")
	t.Cleanup(func() { cleanupManagedGoServerProcessForE2E(t, packagedServerPath) })

	callManagedGoInitializeWithTimeout(t, client)
	diagnostics := callManagedGoToolWithTimeout(t, client, "file", map[string]any{
		"action":    "diagnostics",
		"file_path": target,
	}, "publishDiagnostics/diagnostic")
	requireMCPToolSuccess(t, client, diagnostics, "managed Go diagnostics without PATH")

	symbols := callManagedGoToolWithTimeout(t, client, "structure", map[string]any{
		"action":    "document_symbol",
		"file_path": target,
	}, "workspace load/didOpen/documentSymbol")
	requireMCPToolSuccess(t, client, symbols, "managed Go document symbols without PATH")
}

func callManagedGoInitializeWithTimeout(t *testing.T, client *mcpLSPBinaryClient) mcpLSPBinaryResponse {
	t.Helper()
	result := make(chan mcpLSPBinaryResponse, 1)
	go func() { result <- client.call(t, "initialize", map[string]any{"protocolVersion": "2024-11-05"}) }()
	select {
	case response := <-result:
		t.Logf("managed Go E2E stage=initialize completed stderr=%s", summarizeManagedGoE2EStderr(client.stderrString()))
		return response
	case <-time.After(90 * time.Second):
		t.Logf("managed Go E2E stage=launch/initialize timed out; stderr=%s", summarizeManagedGoE2EStderr(client.stderrString()))
		client.close(t)
		t.Fatalf("managed Go E2E initialize timed out after 90s")
		return mcpLSPBinaryResponse{}
	}
}

func callManagedGoToolWithTimeout(t *testing.T, client *mcpLSPBinaryClient, tool string, args map[string]any, stage string) mcpLSPBinaryResponse {
	t.Helper()
	result := make(chan mcpLSPBinaryResponse, 1)
	go func() { result <- client.callTool(t, tool, args) }()
	select {
	case response := <-result:
		t.Logf("managed Go E2E stage=%s tool=%s completed stderr=%s", stage, tool, summarizeManagedGoE2EStderr(client.stderrString()))
		return response
	case <-time.After(90 * time.Second):
		t.Logf("managed Go E2E stage=%s tool=%s timed out; stderr=%s", stage, tool, summarizeManagedGoE2EStderr(client.stderrString()))
		client.close(t)
		t.Fatalf("managed Go E2E stage %s timed out after 90s", stage)
		return mcpLSPBinaryResponse{}
	}
}

func summarizeManagedGoE2EStderr(stderr string) string {
	const limit = 12000
	stderr = strings.TrimSpace(stderr)
	if len(stderr) <= limit {
		return stderr
	}
	return stderr[:limit] + "...<truncated>"
}

func cleanupManagedGoServerProcessForE2E(t *testing.T, serverPath string) {
	t.Helper()
	target := strings.ReplaceAll(filepath.Clean(serverPath), "'", "''")
	script := "$target='" + target + "'; Get-CimInstance Win32_Process -Filter \"Name='gopls.exe'\" | Where-Object { ($_.ExecutablePath -and $_.ExecutablePath -eq $target) -or ($_.CommandLine -and $_.CommandLine -like ('*' + $target + '*')) } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }; exit 0"
	if output, err := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script).CombinedOutput(); err != nil {
		t.Logf("cleanup managed Go E2E gopls path %s: %v output=%s", filepath.Base(serverPath), err, strings.TrimSpace(string(output)))
	}
}

func writeManagedGoLocalFixtureForE2E(t *testing.T, repoRoot string) string {
	t.Helper()
	parent := filepath.Join(repoRoot, "bin", "LSP", "test", "go")
	dir, err := os.MkdirTemp(parent, ".managed-go-e2e-")
	if err != nil {
		t.Fatalf("create local managed Go fixture under bin/LSP/test/go: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/managedhello\n\ngo 1.19\n"), 0o600); err != nil {
		t.Fatalf("write local managed Go fixture go.mod: %v", err)
	}
	target := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(target, []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatalf("write local managed Go fixture hello.go: %v", err)
	}
	return target
}

func prepareManagedGoPackageForE2E(t *testing.T, builtBinary, productRoot, repoRoot string) string {
	t.Helper()
	installRoot := filepath.Join(productRoot, "bin", "LSP")
	if err := os.MkdirAll(installRoot, 0o700); err != nil {
		t.Fatalf("create managed Go package bin: %v", err)
	}
	installed := filepath.Join(installRoot, "mcp-lsp-managed-go-e2e.exe")
	payload, err := os.ReadFile(builtBinary)
	if err != nil {
		t.Fatalf("read built managed Go sidecar: %v", err)
	}
	if err := os.WriteFile(installed, payload, 0o700); err != nil {
		t.Fatalf("install managed Go sidecar into package bin: %v", err)
	}
	copyRoot := filepath.Join(repoRoot, "bin", "LSP", "lsp")
	bundleRoot := filepath.Join(installRoot, "lsp")
	if err := filepath.WalkDir(copyRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(copyRoot, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(bundleRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o700)
	}); err != nil {
		t.Fatalf("copy managed Go package LSP bundle: %v", err)
	}
	t.Setenv("SUPER_DOLPHIN_LSP_BUNDLE_DIR", bundleRoot)
	t.Setenv("SUPER_DOLPHIN_LSP_MANIFEST", filepath.Join(bundleRoot, "lsp-manifest.json"))
	return installed
}

// provisionManagedGoProductFixtureForE2E 以真实 Windows runtime dependency provisioner 预置可复验产品缓存。
// 它只替换测试下载与安装命令，保留生产 catalog、ready manifest、原子发布和 resolver 校验。
func provisionManagedGoProductFixtureForE2E(t *testing.T, productRoot string) string {
	t.Helper()
	goPath, err := exec.LookPath("go.exe")
	if err != nil {
		goPath, err = exec.LookPath("go")
	}
	if err != nil {
		t.Fatalf("locate host Go executable for managed product fixture: %v", err)
	}
	goPayload, err := os.ReadFile(goPath)
	if err != nil {
		t.Fatalf("read host Go executable for managed product fixture: %v", err)
	}
	goRootOutput, err := exec.Command(goPath, "env", "GOROOT").Output()
	if err != nil || strings.TrimSpace(string(goRootOutput)) == "" {
		t.Fatalf("resolve host GOROOT for managed product fixture: %v", err)
	}
	managedGOROOT := strings.TrimSpace(string(goRootOutput))
	repoRoot := repoRootForMcpLSPBinaryTest(t)
	goplsPayload, err := os.ReadFile(filepath.Join(repoRoot, "bin", "LSP", "lsp", "bin", "gopls.exe"))
	if err != nil {
		t.Fatalf("read existing gopls executable for managed product fixture: %v", err)
	}
	platform, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		t.Fatalf("detect Windows host platform for managed product fixture: %v", err)
	}
	cacheRoot := filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
	fetch := func(_ context.Context, asset installer.WindowsRuntimeDependencyAsset, destination string) error {
		files := map[string][]byte{}
		switch asset.Component {
		case "go":
			files["go/bin/go.exe"] = goPayload
			// 官方 Go 可执行文件是 trimmed 的；生产归档还包含 src，使 go
			// 在 GOROOT 被 cohort 清除后仍能从可执行文件相对路径识别根目录。
			files["go/src/managed-go-e2e.marker"] = []byte("managed Go E2E fixture\n")
			files["go/pkg/tool/managed-go-e2e.marker"] = []byte("managed Go E2E fixture\n")
		case "gopls":
			files["gopls@v0.23.0/go.mod"] = []byte("module golang.org/x/tools/gopls\n")
		default:
			return fmt.Errorf("unexpected managed Go fixture asset %q", asset.Component)
		}
		return writeManagedGoFixtureZIP(destination, files)
	}
	runCommand := func(_ context.Context, _ string, workingDir string, _ []string, _ []string) error {
		binDir := filepath.Join(workingDir, "bin")
		if err := os.MkdirAll(binDir, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(binDir, "gopls.exe"), goplsPayload, 0o700)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err = installer.ProvisionWindowsRuntimeDependencyWithOptions(ctx, installer.WindowsRuntimeDependencyProductGoGopls, installer.WindowsRuntimeDependencyProvisionOptions{
		CacheRoot:  cacheRoot,
		Platform:   &platform,
		FetchAsset: fetch,
		RunCommand: runCommand,
	})
	if err != nil {
		t.Fatalf("provision managed Go product fixture: %v", err)
	}
	return managedGOROOT
}

// writeManagedGoFixtureZIP 写入 provisioner 所需的最小 ZIP 资产，目录与 catalog 的固定路径完全一致。
func writeManagedGoFixtureZIP(destination string, files map[string][]byte) error {
	output, err := os.Create(destination)
	if err != nil {
		return err
	}
	archive := zip.NewWriter(output)
	for name, data := range files {
		entry, entryErr := archive.Create(name)
		if entryErr != nil {
			_ = output.Close()
			return entryErr
		}
		if _, entryErr = entry.Write(data); entryErr != nil {
			_ = output.Close()
			return entryErr
		}
	}
	if err := archive.Close(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
