package manager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

func TestRegistryUsesInstallerResolvedBinaryPath(t *testing.T) {
	resolved := filepath.Join(t.TempDir(), "gopls")
	registry := NewRegistryWithInstaller(fakeInstaller{path: resolved})
	mgr := &capturingBinaryManager{}
	registry.Register("go", mgr)

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: t.TempDir()})
	if _, err := registry.ResolveManagerForLanguage(ctx, "go"); err != nil {
		t.Fatalf("ResolveManagerForLanguage() error = %v", err)
	}
	if got := mgr.binaryOverride(); got != resolved {
		t.Fatalf("binary override = %q, want %q", got, resolved)
	}
}

func TestRegistryUsesDetailedInstallerResolvedBinaryPath(t *testing.T) {
	resolved := filepath.Join(t.TempDir(), "gopls.exe")
	registry := NewRegistryWithInstaller(fakeDetailedInstaller{result: installer.InstallResult{
		Path:   resolved,
		Status: installer.InstallStatusInstalledFallback,
		Lang:   "go",
		Binary: "gopls",
	}})
	mgr := &capturingBinaryManager{}
	registry.Register("go", mgr)

	ctx := common.WithToolScope(context.Background(), common.ToolScope{CWD: t.TempDir()})
	if _, err := registry.ResolveManagerForLanguage(ctx, "go"); err != nil {
		t.Fatalf("ResolveManagerForLanguage() error = %v", err)
	}
	if got := mgr.binaryOverride(); got != resolved {
		t.Fatalf("binary override = %q, want %q", got, resolved)
	}
}

type fakeInstaller struct {
	path string
}

func (f fakeInstaller) EnsureInstalled(context.Context, string) (string, error) {
	return f.path, nil
}

type fakeDetailedInstaller struct {
	result installer.InstallResult
}

func (f fakeDetailedInstaller) EnsureInstalled(context.Context, string) (string, error) {
	return f.result.Path, nil
}

func (f fakeDetailedInstaller) EnsureInstalledDetailed(context.Context, string) (installer.InstallResult, error) {
	return f.result, nil
}

type capturingBinaryManager struct {
	registryDiagnosticsManager
	binary string
}

func (m *capturingBinaryManager) SetBinaryPath(path string) {
	m.binary = path
}

func (m *capturingBinaryManager) binaryOverride() string {
	return m.binary
}
