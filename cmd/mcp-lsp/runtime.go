package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/installer"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/multilsp"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/protocol"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// Manager is the local runtime hook point for LSP and exec resources.
type Manager struct {
	registry          manager.Registry
	root              string
	backgroundRunners []platformrunner.Runner
	releaseScopes     []multilsp.ScopeReleaser
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

func newManager(cfg *platformconfig.Config) (*Manager, error) {
	if cfg == nil {
		return nil, errors.New("platform config is required")
	}
	root, err := runtimeRoot()
	if err != nil {
		return nil, err
	}
	log := pkglogger.Get()
	adapters := multilsp.NewLanguageAdapterRegistryFromConfig(cfg.LSP)
	lspBundle, packagedLSP, err := runtimeenv.LoadLSPBundleFromEnv()
	if err != nil {
		return nil, err
	}
	var inst *installer.Provider
	if !packagedLSP {
		inst = setupInstaller()
	}
	languageIDs, err := runtimePrimaryLanguageIDsForBundle(adapters, lspBundle, packagedLSP)
	if err != nil {
		return nil, err
	}

	registry := manager.NewRegistry(inst)
	backgroundRunners := make([]platformrunner.Runner, 0, len(languageIDs))
	releaseScopes := make([]multilsp.ScopeReleaser, 0, len(languageIDs))
	for _, primaryLanguageID := range languageIDs {
		adapter, ok := adapters.AdapterForLanguage(primaryLanguageID)
		if !ok {
			return nil, errors.New("missing LSP language adapter: " + primaryLanguageID)
		}
		runner, releaser, err := registerRuntimeAdapter(registry, adapter, adapters, root, log, lspBundle, packagedLSP)
		if err != nil {
			return nil, err
		}
		backgroundRunners = appendBackgroundRunner(backgroundRunners, runner)
		releaseScopes = appendReleaseScopeReleaser(releaseScopes, releaser)
	}

	return &Manager{registry: registry, root: root, backgroundRunners: backgroundRunners, releaseScopes: releaseScopes}, nil
}

func runtimeRoot() (string, error) {
	roots, err := runtimeWorkspaceRoots()
	if err != nil {
		return "", err
	}
	if len(roots) == 0 {
		return "", errors.New("runtime workspace root is empty")
	}
	return roots[0], nil
}

func runtimeWorkspaceRoots() ([]string, error) {
	roots, configured, err := runtimeWorkspaceRootsFromEnv()
	if err != nil {
		return nil, err
	}
	if len(roots) > 0 {
		return roots, nil
	}
	if configured {
		return nil, errors.New("runtime workspace roots env is explicitly configured but empty")
	}
	return nil, errors.New("runtime workspace roots env is required")
}

func runtimeWorkspaceRootsFromEnv() ([]string, bool, error) {
	if rawRoots, ok := os.LookupEnv("GO_AGENT_LSP_ROOTS"); ok {
		rawRoots = strings.TrimSpace(rawRoots)
		var decoded []string
		if rawRoots != "" {
			if err := json.Unmarshal([]byte(rawRoots), &decoded); err != nil {
				return nil, true, err
			}
		}
		normalized, err := normalizeRuntimeWorkspaceRoots(decoded)
		if err != nil {
			return nil, true, err
		}
		return normalized, true, nil
	}
	if primary, ok := os.LookupEnv("GO_AGENT_LSP_ROOT"); ok {
		normalized, err := normalizeRuntimeWorkspaceRoots([]string{primary})
		if err != nil {
			return nil, true, err
		}
		return normalized, true, nil
	}
	return nil, false, nil
}

func normalizeRuntimeWorkspaceRoots(roots []string) ([]string, error) {
	if len(roots) == 0 {
		return nil, nil
	}
	base, err := normalizeRuntimeWorkspaceRoot("", roots[0])
	if err != nil {
		return nil, err
	}
	if base == "" {
		return nil, errors.New("runtime workspace roots primary root is required")
	}
	out := make([]string, 0, len(roots))
	seen := map[string]struct{}{base: {}}
	out = append(out, base)
	for _, root := range roots[1:] {
		normalized, err := normalizeRuntimeWorkspaceRoot(base, root)
		if err != nil {
			return nil, err
		}
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizeRuntimeWorkspaceRoot(base, root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", nil
	}
	if strings.TrimSpace(base) != "" && !filepath.IsAbs(root) {
		root = filepath.Join(base, root)
	}
	if filepath.IsAbs(root) {
		return filepath.Clean(root), nil
	}
	return "", errors.New("runtime workspace root must be absolute")
}

func runtimePrimaryLanguageIDs() []string {
	return []string{"go", "javascript", "python", "css", "rust", "java", "markdown", "shellscript"}
}

func runtimePrimaryLanguageIDsForBundle(adapters *multilsp.LanguageAdapterRegistry, bundle runtimeenv.LSPBundle, packaged bool) ([]string, error) {
	if !packaged {
		return runtimePrimaryLanguageIDs(), nil
	}
	ids := make([]string, 0, len(runtimePrimaryLanguageIDs()))
	for _, primaryLanguageID := range runtimePrimaryLanguageIDs() {
		adapter, ok := adapters.AdapterForLanguage(primaryLanguageID)
		if !ok {
			return nil, errors.New("missing LSP language adapter: " + primaryLanguageID)
		}
		if !adapter.CapabilityPolicy().RequiresLSPClient || bundledAdapterLanguageIDs(adapter, bundle) != nil {
			ids = append(ids, primaryLanguageID)
		}
	}
	return ids, nil
}

func bundledAdapterLanguageIDs(adapter multilsp.LanguageAdapter, bundle runtimeenv.LSPBundle) []string {
	if adapter == nil {
		return nil
	}
	var ids []string
	for _, languageID := range adapter.LanguageIDs() {
		if _, ok := bundle.ServerForLanguage(languageID); ok {
			ids = append(ids, strings.ToLower(strings.TrimSpace(languageID)))
		}
	}
	return ids
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
	lspBundle runtimeenv.LSPBundle,
	packagedLSP bool,
) (platformrunner.Runner, multilsp.ScopeReleaser, error) {
	if !adapter.CapabilityPolicy().RequiresLSPClient {
		mgr := createFallbackManager(adapters, root, log)
		registerAdapterLanguagesNoInstall(registry, adapter, mgr)
		return nil, scopeReleaserFromManager(mgr), nil
	}
	languageIDs := bundledAdapterLanguageIDs(adapter, lspBundle)
	binaryOverride := ""
	if packagedLSP {
		server, err := bundledAdapterServer(adapter, lspBundle)
		if err != nil {
			return nil, nil, err
		}
		binaryOverride = server.Path
	}
	mgr, err := createGenericManagerWithBinary(adapter, adapters, root, log, binaryOverride, packagedLSP)
	if err != nil {
		return nil, nil, err
	}
	if packagedLSP {
		registerAdapterLanguagesNoInstall(registry, adapter, mgr, languageIDs...)
		return mgr.BackgroundRunner(), scopeReleaserFromManager(mgr), nil
	}
	registerAdapterLanguages(registry, adapter, mgr)
	return mgr.BackgroundRunner(), scopeReleaserFromManager(mgr), nil
}

func bundledAdapterServer(adapter multilsp.LanguageAdapter, bundle runtimeenv.LSPBundle) (runtimeenv.LSPServer, error) {
	var selected runtimeenv.LSPServer
	for _, languageID := range adapter.LanguageIDs() {
		server, ok := bundle.ServerForLanguage(languageID)
		if !ok {
			continue
		}
		if selected.Path == "" {
			selected = server
			continue
		}
		if selected.Path != server.Path {
			return runtimeenv.LSPServer{}, errors.New("bundled LSP adapter maps to multiple server binaries")
		}
	}
	if selected.Path == "" {
		return runtimeenv.LSPServer{}, errors.New("missing bundled LSP server for adapter")
	}
	return selected, nil
}

func appendReleaseScopeReleaser(releasers []multilsp.ScopeReleaser, releaser multilsp.ScopeReleaser) []multilsp.ScopeReleaser {
	if releaser == nil {
		return releasers
	}
	return append(releasers, releaser)
}

func scopeReleaserFromManager(mgr multilsp.Manager) multilsp.ScopeReleaser {
	releaser, _ := mgr.(multilsp.ScopeReleaser)
	return releaser
}

func registerAdapterLanguages(
	registry interface {
		Register(string, manager.Manager, ...manager.ScopedManagerResolver)
	},
	adapter multilsp.LanguageAdapter,
	mgr multilsp.Manager,
	allowed ...string,
) {
	scopedResolver := runtimeScopedResolver(mgr)
	allowedSet := runtimeAllowedLanguageSet(allowed)
	for _, langID := range adapter.LanguageIDs() {
		if len(allowedSet) > 0 && !allowedSet[strings.ToLower(strings.TrimSpace(langID))] {
			continue
		}
		registry.Register(langID, mgr, scopedResolver)
	}
}

func registerAdapterLanguagesNoInstall(
	registry interface {
		RegisterNoInstall(string, manager.Manager, ...manager.ScopedManagerResolver)
	},
	adapter multilsp.LanguageAdapter,
	mgr multilsp.Manager,
	allowed ...string,
) {
	scopedResolver := runtimeScopedResolver(mgr)
	allowedSet := runtimeAllowedLanguageSet(allowed)
	for _, langID := range adapter.LanguageIDs() {
		if len(allowedSet) > 0 && !allowedSet[strings.ToLower(strings.TrimSpace(langID))] {
			continue
		}
		registry.RegisterNoInstall(langID, mgr, scopedResolver)
	}
}

func runtimeAllowedLanguageSet(allowed []string) map[string]bool {
	if len(allowed) == 0 {
		return nil
	}
	set := make(map[string]bool, len(allowed))
	for _, langID := range allowed {
		langID = strings.ToLower(strings.TrimSpace(langID))
		if langID != "" {
			set[langID] = true
		}
	}
	return set
}

type runtimeScopedResolverProvider interface {
	RegistryScopedResolver() manager.ScopedManagerResolver
}

func runtimeScopedResolver(mgr multilsp.Manager) manager.ScopedManagerResolver {
	if provider, ok := mgr.(runtimeScopedResolverProvider); ok {
		if resolver := provider.RegistryScopedResolver(); resolver != nil {
			return resolver
		}
	}
	return multilsp.NewRegistryScopedResolver(mgr)
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
	inst.Register("shellscript", installer.InstallerConfig{
		BinaryName:  "bash-language-server",
		InstallCmd:  "npm",
		InstallArgs: []string{"install", "-g", "bash-language-server", "shellcheck"},
		RequiredBinaries: []installer.RequiredBinary{
			{Name: "shellcheck", CheckArgs: []string{"--version"}},
		},
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
		WorkspaceRoot:                    root,
		LanguageAdapters:                 adapters,
		Logger:                           log,
		DisableInitialWorkspaceBootstrap: true,
	})
}

func createGenericManagerWithBinary(adapter multilsp.LanguageAdapter, adapters *multilsp.LanguageAdapterRegistry, root string, log *slog.Logger, binaryOverride string, packagedLSP bool) (multilsp.Manager, error) {
	command, err := adapter.ServerCommand(context.Background(), multilsp.ResolvedLanguageScope{})
	if err != nil {
		return nil, err
	}
	initialBinary := runtimeServerBinary(command.Executable, binaryOverride)
	if strings.TrimSpace(initialBinary) == "" {
		return nil, errors.New("language adapter server command is empty")
	}
	binary := &runtimeBinaryOverride{value: initialBinary}
	mgr := multilsp.NewManager(multilsp.Config{
		WorkspaceRoot:                    root,
		LanguageAdapters:                 adapters,
		DisableInitialWorkspaceBootstrap: true,
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
				Binary:              binary.Get(),
				Args:                command.Args,
				Dir:                 dir,
				Env:                 env,
				InitOptions:         runtimeAdapterInitOptions(adapter, packagedLSP),
				NotificationHandler: h,
			})
		}),
		Logger: log,
	})
	return &runtimeBinaryManager{Manager: mgr, binary: binary}, nil
}

const packagedPyrightNoSystemPythonPath = "/__super_dolphin_no_system_python__/python"

func runtimeAdapterInitOptions(adapter multilsp.LanguageAdapter, packagedLSP bool) map[string]any {
	initOptions := adapter.InitOptions(multilsp.ResolvedLanguageScope{})
	if !packagedLSP || !adapterSupportsLanguage(adapter, "python") {
		return initOptions
	}
	if initOptions == nil {
		initOptions = map[string]any{}
	}
	settings, ok := initOptions["settings"].(map[string]any)
	if !ok {
		settings = map[string]any{}
		initOptions["settings"] = settings
	}
	python, ok := settings["python"].(map[string]any)
	if !ok {
		python = map[string]any{}
		settings["python"] = python
	}
	python["pythonPath"] = packagedPyrightNoSystemPythonPath
	return initOptions
}

func adapterSupportsLanguage(adapter multilsp.LanguageAdapter, languageID string) bool {
	for _, adapterLanguageID := range adapter.LanguageIDs() {
		if adapterLanguageID == languageID {
			return true
		}
	}
	return false
}

func runtimeServerBinary(commandExecutable, binaryOverride string) string {
	if trimmed := strings.TrimSpace(binaryOverride); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(commandExecutable)
}

type runtimeBinaryOverride struct {
	mu    sync.RWMutex
	value string
}

func (b *runtimeBinaryOverride) Set(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		b.value = trimmed
	}
}

func (b *runtimeBinaryOverride) Get() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.value
}

type runtimeBinaryManager struct {
	multilsp.Manager
	binary *runtimeBinaryOverride
}

func (m *runtimeBinaryManager) RegistryScopedResolver() manager.ScopedManagerResolver {
	if m == nil {
		return nil
	}
	return multilsp.NewRegistryScopedResolver(m.Manager)
}

func (m *runtimeBinaryManager) ReleaseScope(req multilsp.ReleaseScopeRequest) (multilsp.ReleaseScopeResult, error) {
	if m == nil {
		return multilsp.ReleaseScopeResult{}, nil
	}
	releaser, ok := m.Manager.(multilsp.ScopeReleaser)
	if !ok {
		return multilsp.ReleaseScopeResult{}, nil
	}
	return releaser.ReleaseScope(req)
}

func (m *runtimeBinaryManager) SetBinaryPath(path string) {
	if m != nil && m.binary != nil {
		m.binary.Set(path)
	}
}

func (m *Manager) Close() error {
	if m.registry != nil {
		return m.registry.Close()
	}
	return nil
}

func (m *Manager) ReleaseScope(req mcpdto.LSPReleaseScopeRequest) (mcpdto.LSPReleaseScopeResult, error) {
	if m == nil {
		return mcpdto.LSPReleaseScopeResult{}, nil
	}
	translated := multilsp.ReleaseScopeRequest{
		ScopeKind:  req.ScopeKind,
		AgentID:    req.AgentID,
		ThreadID:   req.ThreadID,
		ManagerKey: req.ManagerKey,
		Drain:      req.Drain,
		Reason:     req.Reason,
	}
	var combined mcpdto.LSPReleaseScopeResult
	var firstErr error
	for _, releaser := range m.releaseScopes {
		if releaser == nil {
			continue
		}
		result, err := releaser.ReleaseScope(translated)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		combined.MatchedManagers += result.MatchedManagers
		combined.ClosedManagers += result.ClosedManagers
		combined.BusyLeases += result.BusyLeases
		combined.Drained = combined.Drained || result.Drained
		combined.ScopeKeys = appendRuntimeUnique(combined.ScopeKeys, result.ScopeKeys...)
		combined.ManagerKeys = appendRuntimeUnique(combined.ManagerKeys, result.ManagerKeys...)
	}
	return combined, firstErr
}

func appendRuntimeUnique(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
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
