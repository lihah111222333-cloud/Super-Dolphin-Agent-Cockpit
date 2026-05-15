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

	registry := manager.NewRegistry(inst)
	backgroundRunners := make([]platformrunner.Runner, 0, 6)
	registerLang := func(langIDs []string, mgr multilsp.Manager) {
		scopedResolver := multilsp.NewRegistryScopedResolver(mgr)
		for _, langID := range langIDs {
			registry.Register(langID, mgr, scopedResolver)
		}
		if r := mgr.BackgroundRunner(); r != nil {
			backgroundRunners = append(backgroundRunners, r)
		}
	}

	// Register Go manager
	registerLang([]string{"go", "gomod", "gosum", "gowork"}, createGenericManager("gopls", nil, root, log))
	// Register JS/TS manager
	registerLang([]string{"javascript", "typescript"}, createGenericManager("typescript-language-server", []string{"--stdio"}, root, log))
	// Register Python manager
	registerLang([]string{"python"}, createGenericManager("pyright-langserver", []string{"--stdio"}, root, log))
	// Register CSS manager
	registerLang([]string{"css"}, createGenericManager("vscode-css-language-server", []string{"--stdio"}, root, log))
	// Register Rust manager
	registerLang([]string{"rust"}, createGenericManager("rust-analyzer", nil, root, log))
	// Register Java manager
	registerLang([]string{"java"}, createGenericManager("jdtls", nil, root, log, jdtlsInitOptions()))

	return &Manager{registry: registry, root: root, backgroundRunners: backgroundRunners}, nil
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

func createGenericManager(executable string, args []string, root string, log *slog.Logger, initOpts ...map[string]any) multilsp.Manager {
	var opts map[string]any
	if len(initOpts) > 0 {
		opts = initOpts[0]
	}
	return multilsp.NewManager(multilsp.Config{
		WorkspaceRoot: root,
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
				Binary:              executable,
				Args:                args,
				Dir:                 dir,
				Env:                 env,
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
