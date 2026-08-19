//go:build !windows

package tools

import (
	"os"
	"path/filepath"
	"testing"

	lspinstaller "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/middleware"
)

func TestFileReadKeepsNormalTimeoutTier(t *testing.T) {
	deadline, ok := fileToolDeadlineForAction(t, "read_file")
	if !ok {
		t.Fatal("file read_file context deadline missing")
	}
	assertDeadlineNear(t, deadline, middleware.TierNormal, "read_file")
}

func TestSameDiagnosticURIKeepsNonWindowsCaseSensitivity(t *testing.T) {
	if sameDiagnosticURI("file:///tmp/Work/main.mq4", "file:///tmp/work/main.mq4") {
		t.Fatal("distinct non-Windows diagnostic paths unexpectedly matched")
	}
}

func requireInstallerMarkerPresent(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("stat installer marker: %v", err)
	}
}

func TestSemanticInspectAndStructureAutoInstallMissingBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("write go fixture: %v", err)
	}
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	marker := filepath.Join(t.TempDir(), "installer-ran")
	t.Setenv("INSTALL_MARKER", marker)
	t.Setenv("FAKE_BIN_DIR", binDir)
	installScript := filepath.Join(t.TempDir(), "install-lsp")
	if err := os.WriteFile(installScript, []byte("#!/bin/sh\nset -eu\n: >> \"$INSTALL_MARKER\"\ntarget=\"$1\"\nprintf '#!/bin/sh\\nexit 0\\n' > \"$FAKE_BIN_DIR/$target\"\n/bin/chmod +x \"$FAKE_BIN_DIR/$target\"\n"), 0o755); err != nil {
		t.Fatalf("write fake installer: %v", err)
	}
	inst := lspinstaller.NewProvider()
	inst.Register("go", lspinstaller.InstallerConfig{BinaryName: "gopls", InstallCmd: installScript, InstallArgs: []string{"gopls"}, AllowInstallCommand: true})
	inst.Register("typescript", lspinstaller.InstallerConfig{BinaryName: "typescript-language-server", InstallCmd: installScript, InstallArgs: []string{"typescript-language-server"}, AllowInstallCommand: true})
	registry := lspmanager.NewRegistryWithInstaller(inst)
	registry.Register("go", &structureTestManager{})
	registry.Register("typescript", &structureTestManager{})

	t.Run("inspect", func(t *testing.T) {
		payload := mustMarshalToolPayload(t, map[string]any{"action": "hover", "pos": filepath.Join(root, "main.go") + ":1:1"})
		if _, err := NewInspectHandler(registry)(testToolContext(root), payload); err != nil {
			t.Fatalf("inspect handler error = %v, want auto-install success", err)
		}
		requireInstallerMarkerPresent(t, marker)
	})
	t.Run("structure", func(t *testing.T) {
		payload := mustMarshalToolPayload(t, map[string]any{"action": "workspace_symbol", "workspace_language": "typescript", "query": "anything"})
		if _, err := NewStructureHandler(registry)(testToolContext(root), payload); err != nil {
			t.Fatalf("structure handler error = %v, want auto-install success", err)
		}
		requireInstallerMarkerPresent(t, marker)
	})
}
