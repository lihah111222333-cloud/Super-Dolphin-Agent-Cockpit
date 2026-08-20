//go:build windows && e2e

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// requireRealLanguageServersForE2E 在 Windows 上先复用产品缓存，缺失时授予受控安装动作。
// 显式 resolver 始终优先，禁止 PATH 命中；安装失败直接阻断真实 E2E。
func requireRealLanguageServersForE2E(t *testing.T, cases []realLSPDiagnosticsCase) {
	t.Helper()
	alignWindowsProductRootForE2E(t)
	provider, err := setupInstallerWithError()
	if err != nil {
		t.Fatalf("setup Windows product installer for real system e2e: %v", err)
	}

	seen := map[string]struct{}{}
	for _, tc := range cases {
		languageID := strings.TrimSpace(tc.languageID)
		if languageID == "" {
			t.Fatalf("real system e2e requirement has empty language ID")
		}
		if _, ok := seen[languageID]; ok {
			continue
		}
		seen[languageID] = struct{}{}

		preflightContext, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		result, err := ensureWindowsRealLanguageServerForE2E(preflightContext, provider, languageID)
		cancel()
		if err != nil {
			t.Fatalf("real Windows system e2e requires product asset for language %q: %v", languageID, err)
		}
		if !filepath.IsAbs(result.Path) {
			t.Fatalf("real Windows system e2e product resolver returned non-absolute path for language %q: %q", languageID, result.Path)
		}
	}
}

// ensureWindowsRealLanguageServerForE2E 只接受 Windows 产品的显式 resolver 和受控 InstallAction。
// 调用方授予安装能力后，Provider 会先检查缓存；只有缓存缺失时才执行一次安装动作。
func ensureWindowsRealLanguageServerForE2E(ctx context.Context, provider *installer.Provider, languageID string) (installer.InstallResult, error) {
	languageID = strings.TrimSpace(languageID)
	if languageID == "" {
		return installer.InstallResult{}, fmt.Errorf("real Windows system e2e language ID is empty")
	}
	if provider == nil {
		return installer.InstallResult{}, fmt.Errorf("real Windows system e2e product installer is nil")
	}
	cfg, ok := provider.ConfigForLanguage(languageID)
	if !ok {
		return installer.InstallResult{}, fmt.Errorf("real Windows system e2e has no product installer config for language %q", languageID)
	}
	if cfg.InstalledBinaryPathResolver == nil {
		return installer.InstallResult{}, fmt.Errorf("real Windows system e2e language %q has no explicit product-cache resolver", languageID)
	}
	if cfg.InstallAction == nil || !cfg.AllowInstallCommand {
		return installer.InstallResult{}, fmt.Errorf("real Windows system e2e language %q has no controlled product InstallAction", languageID)
	}
	return provider.EnsureInstalledDetailed(installer.WithInstallCommandCapability(ctx), languageID)
}

// TestWindowsRealLanguageServerPreflightUsesCachedProductPathWithoutInstall 锁定缓存命中不执行安装动作。
func TestWindowsRealLanguageServerPreflightUsesCachedProductPathWithoutInstall(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	productRoot := t.TempDir()
	cachedPath := filepath.Join(productRoot, "cache", "lsp-assets", "clangd", "bin", "clangd.exe")
	if err := os.MkdirAll(filepath.Dir(cachedPath), 0o700); err != nil {
		t.Fatalf("create cached product directory: %v", err)
	}
	if err := os.WriteFile(cachedPath, []byte("cached clangd"), 0o700); err != nil {
		t.Fatalf("write cached product binary: %v", err)
	}

	installCalls := 0
	provider := installer.NewProvider()
	provider.Register("c", installer.InstallerConfig{
		BinaryName:                  "clangd.exe",
		AllowInstallCommand:         true,
		InstalledBinaryPathResolver: func(context.Context) (string, error) { return cachedPath, nil },
		InstallAction: func(context.Context) (installer.InstallResult, error) {
			installCalls++
			return installer.InstallResult{Path: cachedPath}, nil
		},
	})

	result, err := ensureWindowsRealLanguageServerForE2E(context.Background(), provider, "c")
	if err != nil {
		t.Fatalf("cached product preflight: %v", err)
	}
	if installCalls != 0 {
		t.Fatalf("cached product install calls = %d, want 0", installCalls)
	}
	if result.Status != installer.InstallStatusPathFound {
		t.Fatalf("cached product status = %q, want %q", result.Status, installer.InstallStatusPathFound)
	}
	if result.Path != cachedPath || !filepath.IsAbs(result.Path) {
		t.Fatalf("cached product path = %q, want absolute cached path %q", result.Path, cachedPath)
	}
}

// TestWindowsRealLanguageServerPreflightInstallsMissingProductAssetOnce 锁定缺失缓存只执行一次受控安装并返回产品绝对路径。
func TestWindowsRealLanguageServerPreflightInstallsMissingProductAssetOnce(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	productRoot := t.TempDir()
	installedPath := filepath.Join(productRoot, "cache", "lsp-assets", "clangd", "bin", "clangd.exe")
	installCalls := 0
	provider := installer.NewProvider()
	provider.Register("c", installer.InstallerConfig{
		BinaryName:                  "clangd.exe",
		AllowInstallCommand:         true,
		InstalledBinaryPathResolver: func(context.Context) (string, error) { return installedPath, nil },
		InstallAction: func(context.Context) (installer.InstallResult, error) {
			installCalls++
			if err := os.MkdirAll(filepath.Dir(installedPath), 0o700); err != nil {
				return installer.InstallResult{}, err
			}
			if err := os.WriteFile(installedPath, []byte("installed clangd"), 0o700); err != nil {
				return installer.InstallResult{}, err
			}
			return installer.InstallResult{Path: installedPath}, nil
		},
	})

	result, err := ensureWindowsRealLanguageServerForE2E(context.Background(), provider, "c")
	if err != nil {
		t.Fatalf("missing product preflight: %v", err)
	}
	if installCalls != 1 {
		t.Fatalf("missing product install calls = %d, want 1", installCalls)
	}
	if result.Status != installer.InstallStatusInstalledPath {
		t.Fatalf("missing product status = %q, want %q", result.Status, installer.InstallStatusInstalledPath)
	}
	if result.Path != installedPath || !filepath.IsAbs(result.Path) {
		t.Fatalf("installed product path = %q, want absolute product path %q", result.Path, installedPath)
	}
}

// TestAlignWindowsProductRootForE2EUsesWorkspaceScopedProductHome 锁定未配置产品根时的隔离路径契约。
func TestAlignWindowsProductRootForE2EUsesWorkspaceScopedProductHome(t *testing.T) {
	t.Setenv("SUPER_DOLPHIN_HOME", "")
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", repoRootForMcpLSPBinaryTest(t))

	alignWindowsProductRootForE2E(t)

	productRoot := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME"))
	if productRoot == "" || !filepath.IsAbs(productRoot) {
		t.Fatalf("workspace product root = %q, want an absolute path", productRoot)
	}
	repoRoot := filepath.Clean(repoRootForMcpLSPBinaryTest(t))
	if filepath.Clean(productRoot) == filepath.Join(repoRoot, ".super-dolphin") {
		t.Fatalf("workspace product root must not reuse the repository as runtime resources: %q", productRoot)
	}
	if !pathWithinWindowsE2ERoot(t, filepath.Join(repoRoot, ".build-cache"), productRoot) {
		t.Fatalf("workspace product root = %q, want an isolated root under %q", productRoot, filepath.Join(repoRoot, ".build-cache"))
	}
	if got := os.Getenv("PROJECT_ROOT"); got != "" {
		t.Fatalf("PROJECT_ROOT = %q, want empty after explicit product-root alignment", got)
	}
	if got := os.Getenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR"); got != "" {
		t.Fatalf("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR = %q, want empty after explicit product-root alignment", got)
	}
}

// TestAlignWindowsProductRootForE2EReusesAbsoluteProductHome 锁定已有产品缓存根不被替换。
func TestAlignWindowsProductRootForE2EReusesAbsoluteProductHome(t *testing.T) {
	productRoot := filepath.Join(t.TempDir(), "reusable-product-root")
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", filepath.Join(t.TempDir(), "source-root"))
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", filepath.Join(t.TempDir(), "resource-root"))

	alignWindowsProductRootForE2E(t)

	if got := filepath.Clean(os.Getenv("SUPER_DOLPHIN_HOME")); got != filepath.Clean(productRoot) {
		t.Fatalf("reusable product root = %q, want %q", got, productRoot)
	}
}

func pathWithinWindowsE2ERoot(t *testing.T, root, candidate string) bool {
	t.Helper()
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(candidate))
	if err != nil {
		t.Fatalf("resolve product-root scope: %v", err)
	}
	return relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

// alignWindowsProductRootForE2E 与 sidecar 测试启动器保持同一产品资源根；显式宿主配置优先。
func alignWindowsProductRootForE2E(t *testing.T) {
	t.Helper()
	if strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_HOME")) != "" {
		return
	}
	repoRoot := filepath.Clean(repoRootForMcpLSPBinaryTest(t))
	productRoot := filepath.Join(repoRoot, ".build-cache", "lsp-real-system-windows-product")
	if err := os.MkdirAll(productRoot, 0o700); err != nil {
		t.Fatalf("create workspace-scoped Windows LSP product root %q: %v", productRoot, err)
	}
	t.Setenv("SUPER_DOLPHIN_HOME", productRoot)
	t.Setenv("PROJECT_ROOT", "")
	t.Setenv("SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR", "")
}
