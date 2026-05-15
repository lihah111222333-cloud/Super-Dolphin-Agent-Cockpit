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
	root, err := runtimeRoot()
	if err != nil {
		return nil, err
	}
	log := pkglogger.Get()
	inst := setupInstaller()
	adapters := multilsp.NewDefaultLanguageAdapterRegistry()

	registry := manager.NewRegistry(inst)
	backgroundRunners := make([]platformrunner.Runner, 0, len(runtimePrimaryLanguageIDs()))
	for _, primaryLanguageID := range runtimePrimaryLanguageIDs() {
		adapter, ok := adapters.AdapterForLanguage(primaryLanguageID)
		if !ok {
			return nil, errors.New("missing LSP language adapter: " + primaryLanguageID)
		}
		runner, err := registerRuntimeAdapter(registry, adapter, adapters, root, log)
		if err != nil {
			return nil, err
		}
		backgroundRunners = appendBackgroundRunner(backgroundRunners, runner)
	}

	return &Manager{registry: registry, root: root, backgroundRunners: backgroundRunners}, nil
}

func runtimeRoot() (string, error) {
	root := os.Getenv("GO_AGENT_LSP_ROOT")
	if root != "" {
		return root, nil
	}
	return os.Getwd()
}

func runtimePrimaryLanguageIDs() []string {
	return []string{"go", "javascript", "python", "css", "rust", "java", "markdown"}
}

func appendBackgroundRunner(runners []platformrunner.Runner, runner platformrunner.Runner) []platformrunner.Runner {
	if runner == nil {
		return runners
	}
	return append(runners, runner)
}

func registerRuntimeAdapter(
	registry interface {
		Register(string, manager.Manager, ...manager.ScopedManagerResolver)
		RegisterNoInstall(string, manager.Manager, ...manager.ScopedManagerResolver)
	},
	adapter multilsp.LanguageAdapter,
	adapters *multilsp.LanguageAdapterRegistry,
	root string,
	log *slog.Logger,
) (platformrunner.Runner, error) {
	if !adapter.CapabilityPolicy().RequiresLSPClient {
		mgr := createFallbackManager(adapters, root, log)
		registerAdapterLanguagesNoInstall(registry, adapter, mgr)
		return nil, nil
	}
	mgr, err := createGenericManager(adapter, adapters, root, log)
	if err != nil {
		return nil, err
	}
	registerAdapterLanguages(registry, adapter, mgr)
	return mgr.BackgroundRunner(), nil
}

func registerAdapterLanguages(
	registry interface {
		Register(string, manager.Manager, ...manager.ScopedManagerResolver)
	},
	adapter multilsp.LanguageAdapter,
	mgr multilsp.Manager,
) {
	scopedResolver := multilsp.NewRegistryScopedResolver(mgr)
	for _, langID := range adapter.LanguageIDs() {
		registry.Register(langID, mgr, scopedResolver)
	}
}

func registerAdapterLanguagesNoInstall(
	registry interface {
		RegisterNoInstall(string, manager.Manager, ...manager.ScopedManagerResolver)
	},
	adapter multilsp.LanguageAdapter,
	mgr multilsp.Manager,
) {
	scopedResolver := multilsp.NewRegistryScopedResolver(mgr)
	for _, langID := range adapter.LanguageIDs() {
		registry.RegisterNoInstall(langID, mgr, scopedResolver)
	}
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

func createFallbackManager(adapters *multilsp.LanguageAdapterRegistry, root string, log *slog.Logger) multilsp.Manager {
	return multilsp.NewManager(multilsp.Config{
		WorkspaceRoot:    root,
		LanguageAdapters: adapters,
		Logger:           log,
	})
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
