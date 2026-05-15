package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/installer"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Manager is the local runtime hook point for LSP and exec resources.
type Manager struct {
	registry          manager.Registry
	root              string
	backgroundRunners []platformrunner.Runner
}

// BackgroundRunners returns the long-running owners this Manager
// contributes to the root `group:"runners"` aggregation. Currently
// the per-language ManagerPool recyclers. See P22 P2 LSP-S1
// (docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md §480-494).
func (m *Manager) BackgroundRunners() []platformrunner.Runner {
	if m == nil {
		return nil
	}
	return m.backgroundRunners
}

func newManager() (*Manager, error) {
	root := os.Getenv("GO_AGENT_LSP_ROOT")
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	log := pkglogger.Get()
	inst := setupInstaller()
	adapters := multilsp.NewDefaultLanguageAdapterRegistry()

	registry := manager.NewRegistry(inst)
	backgroundRunners := make([]platformrunner.Runner, 0, 6)
	registerLang := func(adapter multilsp.LanguageAdapter) error {
		if !adapter.CapabilityPolicy().RequiresLSPClient {
			return nil
		}
		mgr, err := createGenericManager(adapter, adapters, root, log)
		if err != nil {
			return err
		}
		scopedResolver := multilsp.NewRegistryScopedResolver(mgr)
		for _, langID := range adapter.LanguageIDs() {
			registry.Register(langID, mgr, scopedResolver)
		}
		if r := mgr.BackgroundRunner(); r != nil {
			backgroundRunners = append(backgroundRunners, r)
		}
		return nil
	}

	for _, primaryLanguageID := range []string{"go", "javascript", "python", "css", "rust", "java"} {
		adapter, ok := adapters.AdapterForLanguage(primaryLanguageID)
		if !ok {
			return nil, errors.New("missing LSP language adapter: " + primaryLanguageID)
		}
		if err := registerLang(adapter); err != nil {
			return nil, err
		}
	}

	return &Manager{registry: registry, root: root, backgroundRunners: backgroundRunners}, nil
}

func setupInstaller() *installer.Provider {
	inst := installer.NewProvider()

	inst.Register("javascript", installer.InstallerConfig{
		BinaryName:  "typescript-language-server",
		InstallCmd:  "npm",
		InstallArgs: []string{"install", "-g", "typescript-language-server", "typescript"},
	})
	inst.Register("javascriptreact", installer.InstallerConfig{
		BinaryName:  "typescript-language-server",
		InstallCmd:  "npm",
		InstallArgs: []string{"install", "-g", "typescript-language-server", "typescript"},
	})
	inst.Register("typescript", installer.InstallerConfig{
		BinaryName:  "typescript-language-server",
		InstallCmd:  "npm",
		InstallArgs: []string{"install", "-g", "typescript-language-server", "typescript"},
	})
	inst.Register("typescriptreact", installer.InstallerConfig{
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
	for _, alias := range []string{"gomod", "gosum", "gowork"} {
		inst.Register(alias, installer.InstallerConfig{
			BinaryName:  "gopls",
			InstallCmd:  "go",
			InstallArgs: []string{"install", "golang.org/x/tools/gopls@latest"},
		})
	}

	return inst
}

func createGenericManager(adapter multilsp.LanguageAdapter, adapters *multilsp.LanguageAdapterRegistry, root string, log *slog.Logger) (multilsp.Manager, error) {
	command, err := adapter.ServerCommand(context.Background(), multilsp.ResolvedLanguageScope{})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.Executable) == "" {
		return nil, errors.New("language adapter server command is empty")
	}
	return multilsp.NewManager(multilsp.Config{
		WorkspaceRoot:    root,
		LanguageAdapters: adapters,
		ClientFactory: multilsp.ClientFactoryWithEnvFunc(func(rootDir string, env []string, h protocol.NotificationHandler) (multilsp.Client, error) {
			// rootDir is supplied per-call from cfg.rootPath so the
			// language server subprocess Dir tracks the workspace being
			// initialised.
			// For example, this follows ctx _cwd from an agent in
			// another project. It falls back to the manager's startup
			// root only when the caller has not resolved a specific
			// workspace yet.
			dir := rootDir
			if strings.TrimSpace(dir) == "" {
				dir = root
			}
			return multilsp.NewClientWithOptions(multilsp.Options{
				Binary:              command.Executable,
				Args:                command.Args,
				Dir:                 dir,
				Env:                 env,
				InitOptions:         adapter.InitOptions(multilsp.ResolvedLanguageScope{}),
				NotificationHandler: h,
			})
		}),
		Logger: log,
	}), nil
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
