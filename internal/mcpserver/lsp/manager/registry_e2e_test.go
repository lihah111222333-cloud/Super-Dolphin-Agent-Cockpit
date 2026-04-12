//go:build e2e
// +build e2e

package manager_test

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/installer"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
)

func createGenericManager(executable string, args []string, root string, log *slog.Logger) manager.Manager {
	return gopls.NewManager(gopls.Config{
		WorkspaceRoot: root,
		ClientFactory: gopls.ClientFactoryFunc(func(h protocol.NotificationHandler) (gopls.Client, error) {
			return gopls.NewClientWithOptions(gopls.Options{
				Binary:              executable,
				Args:                args,
				Dir:                 root,
				NotificationHandler: h,
			})
		}),
		Logger: log,
	})
}

// TestMultiLanguageAutoInstall_E2E validates that the LSP toolchain can correctly
// identify missing language support, trigger auto-installation via the system package manager (npm),
// and subsequently bootstrap and parse a Python document.
//
// Run with: go test -v -tags=e2e ./internal/mcpserver/lsp/manager/...
func TestMultiLanguageAutoInstall_E2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	inst := installer.NewProvider()
	inst.Register("python", installer.InstallerConfig{
		BinaryName: "pyright-langserver",
		InstallCmd: "npm",
		// In E2E try to install locally to avoid global env contamination
		InstallArgs: []string{"install", "--prefix", t.TempDir(), "pyright"},
	})
	reg := manager.NewRegistry(inst)

	root := t.TempDir()
	pyMgr := createGenericManager("pyright-langserver", []string{"--stdio"}, root, log)
	reg.Register("python", pyMgr)

	// Create Dummy Python File
	pyFile := filepath.Join(root, "dummy_test.py")
	content := `
def calculate_sum(a, b):
    """Calculates the sum of two numbers."""
    return a + b
result = calculate_sum(10, 20)
`
	if err := os.WriteFile(pyFile, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	t.Log("Registry initialized. Retrieving manager for Python file...")

	start := time.Now()
	// GetManagerForFile will trigger auto-installation if "pyright-langserver" is missing.
	mgr, err := reg.GetManagerForFile(ctx, pyFile)
	if err != nil {
		t.Fatalf("GetManagerForFile failed: %v", err)
	}
	t.Logf("Acquired Manager in %v", time.Since(start))

	if err := mgr.BootstrapDocument(ctx, "file://"+pyFile); err != nil {
		t.Fatalf("BootstrapDocument failed: %v", err)
	}

	// Wait briefly for pyright language server to finish indexing the new file.
	time.Sleep(3 * time.Second)

	symbols, err := mgr.DocumentSymbol(ctx, "file://"+pyFile)
	if err != nil {
		t.Fatalf("DocumentSymbol failed: %v", err)
	}

	t.Logf("Success! LSP Extracted %d symbols from the file via pyright.", len(symbols))
}
