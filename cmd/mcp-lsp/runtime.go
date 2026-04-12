package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/gopls"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/installer"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/lsp/protocol"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Manager is the local runtime hook point for LSP and exec resources.
type Manager struct {
	registry manager.Registry
	root     string
}

func newManager() (*Manager, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	log := pkglogger.Get()
	inst := setupInstaller()

	registry := manager.NewRegistry(inst)

	// Register Go manager
	goplsMgr := createGenericManager("gopls", nil, root, log)
	registry.Register("go", goplsMgr)
	registry.Register("gomod", goplsMgr)
	registry.Register("gosum", goplsMgr)
	registry.Register("gowork", goplsMgr)

	// Register JS/TS manager
	jsMgr := createGenericManager("typescript-language-server", []string{"--stdio"}, root, log)
	registry.Register("javascript", jsMgr)
	registry.Register("typescript", jsMgr)

	// Register Python manager
	pyMgr := createGenericManager("pyright-langserver", []string{"--stdio"}, root, log)
	registry.Register("python", pyMgr)

	// Register CSS manager
	cssMgr := createGenericManager("vscode-css-language-server", []string{"--stdio"}, root, log)
	registry.Register("css", cssMgr)

	// Register Rust manager
	rustMgr := createGenericManager("rust-analyzer", nil, root, log)
	registry.Register("rust", rustMgr)

	// Register Java manager
	javaMgr := createGenericManager("jdtls", nil, root, log, jdtlsInitOptions())
	registry.Register("java", javaMgr)

	return &Manager{registry: registry, root: root}, nil
}

func setupInstaller() *installer.Provider {
	inst := installer.NewProvider()

	inst.Register("javascript", installer.InstallerConfig{
		BinaryName:  "typescript-language-server",
		InstallCmd:  "npm",
		InstallArgs: []string{"install", "-g", "typescript-language-server", "typescript"},
	})
	inst.Register("typescript", installer.InstallerConfig{
		BinaryName:  "typescript-language-server",
		InstallCmd:  "npm",
		InstallArgs: []string{"install", "-g", "typescript-language-server", "typescript"},
	})
	inst.Register("python", installer.InstallerConfig{
		BinaryName:  "pyright-langserver",
		InstallCmd:  "npm",
		InstallArgs: []string{"install", "-g", "pyright"},
	})
	inst.Register("css", installer.InstallerConfig{
		BinaryName:  "vscode-css-language-server",
		InstallCmd:  "npm",
		InstallArgs: []string{"install", "-g", "vscode-langservers-extracted"},
	})
	inst.Register("rust", installer.InstallerConfig{
		BinaryName:  "rust-analyzer",
		InstallCmd:  "rustup",
		InstallArgs: []string{"component", "add", "rust-analyzer"},
	})
	inst.Register("java", installer.InstallerConfig{
		BinaryName:  "jdtls",
		InstallCmd:  "brew",
		InstallArgs: []string{"install", "jdtls"},
	})
	inst.Register("go", installer.InstallerConfig{
		BinaryName:  "gopls",
		InstallCmd:  "go",
		InstallArgs: []string{"install", "golang.org/x/tools/gopls@latest"},
	})

	return inst
}

func createGenericManager(executable string, args []string, root string, log *slog.Logger, initOpts ...map[string]any) manager.Manager {
	var opts map[string]any
	if len(initOpts) > 0 {
		opts = initOpts[0]
	}
	return gopls.NewManager(gopls.Config{
		WorkspaceRoot: root,
		ClientFactory: gopls.ClientFactoryFunc(func(h protocol.NotificationHandler) (gopls.Client, error) {
			return gopls.NewClientWithOptions(gopls.Options{
				Binary:              executable,
				Args:                args,
				Dir:                 root,
				InitOptions:         opts,
				NotificationHandler: h,
			})
		}),
		Logger: log,
	})
}

func jdtlsInitOptions() map[string]any {
	return map[string]any{
		"settings": map[string]any{
			"java": map[string]any{
				"configuration": map[string]any{
					"updateBuildConfiguration": "automatic",
				},
				"import": map[string]any{
					"gradle": map[string]any{"enabled": true},
					"maven":  map[string]any{"enabled": true},
				},
			},
		},
		"extendedClientCapabilities": map[string]any{
			"classFileContentsSupport": true,
		},
	}
}

func (m *Manager) Close() error {
	if m.registry != nil {
		return m.registry.Close()
	}
	return nil
}

type stdioRunner struct {
	server  *common.Server
	manager *Manager
}

func newStdioRunner(server *common.Server, manager *Manager) platformrunner.Runner {
	return stdioRunner{server: server, manager: manager}
}

func (r stdioRunner) Run(ctx context.Context) error {
	if r.server == nil {
		return errors.New("mcp-lsp server is not configured")
	}
	defer func() {
		if r.manager != nil {
			_ = r.manager.Close()
		}
	}()
	return r.server.Run(ctx)
}
