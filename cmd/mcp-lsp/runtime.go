// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

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

const (
	lspDiagnosticsColdStartMaxWait = 8 * time.Second
	jstsTSServerFallbackPath       = "tsserver"
)

// Manager 汇总 mcp-lsp 进程内的语言 registry、后台 runner 和 scope 释放器。
// 它是 MCP 工具层进入 LSP runtime 的本地边界，不直接暴露具体 ManagerPool 实现。
type Manager struct {
	registry          manager.Registry
	root              string
	backgroundRunners []platformrunner.Runner
	releaseScopes     []multilsp.ScopeReleaser
}

// BackgroundRunners 返回需要交给根 runner 聚合器托管的 LSP 后台任务。
// 当前主要是各语言 ManagerPool 的 recycler；nil Manager 返回 nil，便于上层统一过滤。
func (m *Manager) BackgroundRunners() []platformrunner.Runner {
	if m == nil {
		return nil
	}
	return m.backgroundRunners
}

// newManager 根据平台配置组装语言适配器、安装器、registry 和后台 runner。
// 配置缺失、runtime 根目录缺失或语言适配器不完整都会立即返回错误，避免启动半可用 LSP 服务。
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

// runtimeWorkspaceRootsFromEnv 从环境变量读取 sidecar 可信工作区根。
// 显式配置为空会返回 configured=true，调用方据此 fail-fast 而不是退回进程 cwd。
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

// normalizeRuntimeWorkspaceRoots 规范化运行时工作区根目录。
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
	return []string{"go", "javascript", "python", "css", "rust", "java", "markdown", "shellscript", "sql"}
}

// runtimePrimaryLanguageIDsForBundle 为包体处理运行时primary语言ids。
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

// registerRuntimeAdapter 注册运行时适配器。
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

// bundledAdapterServer 为一个语言适配器选择打包内置的 LSP server。
// 同一适配器声明的多个语言必须映射到同一二进制，否则返回错误防止启动错配 server。
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

// setupInstaller 注册按需安装各语言 LSP server 的命令。
// 仅在未使用打包 LSP bundle 时启用，避免运行时覆盖应用随包携带的二进制。
func setupInstaller() *installer.Provider {
	inst := installer.NewProvider()

	inst.Register("javascript", installer.InstallerConfig{
		BinaryName:          "typescript-language-server",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "typescript-language-server", "typescript"},
		AllowInstallCommand: true,
	})
	inst.Register("javascriptreact", installer.InstallerConfig{
		BinaryName:          "typescript-language-server",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "typescript-language-server", "typescript"},
		AllowInstallCommand: true,
	})
	inst.Register("typescript", installer.InstallerConfig{
		BinaryName:          "typescript-language-server",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "typescript-language-server", "typescript"},
		AllowInstallCommand: true,
	})
	inst.Register("typescriptreact", installer.InstallerConfig{
		BinaryName:          "typescript-language-server",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "typescript-language-server", "typescript"},
		AllowInstallCommand: true,
	})
	inst.Register("python", installer.InstallerConfig{
		BinaryName:          "pyright-langserver",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "pyright"},
		AllowInstallCommand: true,
	})
	inst.Register("css", installer.InstallerConfig{
		BinaryName:          "vscode-css-language-server",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "vscode-langservers-extracted"},
		AllowInstallCommand: true,
	})
	inst.Register("rust", installer.InstallerConfig{
		BinaryName:          "rust-analyzer",
		InstallCmd:          "rustup",
		InstallArgs:         []string{"component", "add", "rust-analyzer"},
		AllowInstallCommand: true,
	})
	inst.Register("java", installer.InstallerConfig{
		BinaryName:          "jdtls",
		InstallCmd:          "brew",
		InstallArgs:         []string{"install", "jdtls"},
		AllowInstallCommand: true,
	})
	inst.Register("go", installer.InstallerConfig{
		BinaryName:          "gopls",
		InstallCmd:          "go",
		InstallArgs:         []string{"install", "golang.org/x/tools/gopls@latest"},
		AllowInstallCommand: true,
	})
	registerShellAndSQLInstallers(inst)
	for _, alias := range []string{"gomod", "gosum", "gowork"} {
		inst.Register(alias, installer.InstallerConfig{
			BinaryName:          "gopls",
			InstallCmd:          "go",
			InstallArgs:         []string{"install", "golang.org/x/tools/gopls@latest"},
			AllowInstallCommand: true,
		})
	}

	return inst
}

func registerShellAndSQLInstallers(inst *installer.Provider) {
	inst.Register("shellscript", installer.InstallerConfig{
		BinaryName:          "bash-language-server",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "bash-language-server", "shellcheck"},
		AllowInstallCommand: true,
		RequiredBinaries: []installer.RequiredBinary{
			{Name: "shellcheck", CheckArgs: []string{"--version"}},
		},
	})
	inst.Register("sql", installer.InstallerConfig{
		BinaryName:          "sql-language-server",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "sql-language-server"},
		AllowInstallCommand: true,
	})
}

func createFallbackManager(adapters *multilsp.LanguageAdapterRegistry, root string, log *slog.Logger) multilsp.Manager {
	return multilsp.NewManager(multilsp.Config{
		WorkspaceRoot:                    root,
		LanguageAdapters:                 adapters,
		Logger:                           log,
		DisableInitialWorkspaceBootstrap: true,
	})
}

// createGenericManagerWithBinary 创建可热更新二进制路径的通用 multilsp manager。
// ClientFactory 按每次解析出的 workspace root 设置子进程 Dir，避免不同项目共享同一启动目录。
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
		DiagnosticsMaxWait:               runtimeAdapterDiagnosticsMaxWait(adapter),
		DisableInitialWorkspaceBootstrap: true,
		ClientFactory: multilsp.ClientFactoryWithEnvFunc(func(rootDir string, env []string, h protocol.NotificationHandler) (multilsp.Client, error) {
			// rootDir 来自本次 workspace 解析结果，让语言服务器子进程跟随调用方项目。
			// 只有调用方尚未解析到具体 workspace 时，才退回 manager 启动根目录。
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

func runtimeAdapterDiagnosticsMaxWait(adapter multilsp.LanguageAdapter) time.Duration {
	if adapter.CapabilityPolicy().RequiresLSPClient {
		return lspDiagnosticsColdStartMaxWait
	}
	return 0
}

const packagedPyrightNoSystemPythonPath = "/__super_dolphin_no_system_python__/python"

// runtimeAdapterInitOptions 复制适配器 init options 并补充打包 LSP 的运行约束。
// 打包 Pyright 使用哨兵 pythonPath，防止它隐式探测系统 Python 造成跨环境差异。
func runtimeAdapterInitOptions(adapter multilsp.LanguageAdapter, packagedLSP bool) map[string]any {
	initOptions := adapter.InitOptions(multilsp.ResolvedLanguageScope{})
	initOptions = runtimeJSTSInitOptions(adapter, initOptions)
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

// runtimeJSTSInitOptions 为 JS/TS language server 注入 tsserver 后备路径。
// typescript-language-server 只有在工作区 TypeScript 查找失败后才会使用该后备路径。
func runtimeJSTSInitOptions(adapter multilsp.LanguageAdapter, initOptions map[string]any) map[string]any {
	if !runtimeAdapterUsesJSTS(adapter) {
		return initOptions
	}
	if initOptions == nil {
		initOptions = map[string]any{}
	}
	tsserver, ok := initOptions["tsserver"].(map[string]any)
	if !ok {
		tsserver = map[string]any{}
		initOptions["tsserver"] = tsserver
	}
	if runtimeStringOption(tsserver["path"]) == "" && runtimeStringOption(tsserver["fallbackPath"]) == "" {
		tsserver["fallbackPath"] = jstsTSServerFallbackPath
	}
	return initOptions
}

func runtimeAdapterUsesJSTS(adapter multilsp.LanguageAdapter) bool {
	for _, languageID := range []string{"javascript", "javascriptreact", "typescript", "typescriptreact"} {
		if adapterSupportsLanguage(adapter, languageID) {
			return true
		}
	}
	return false
}

func runtimeStringOption(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func adapterSupportsLanguage(adapter multilsp.LanguageAdapter, languageID string) bool {
	return slices.Contains(adapter.LanguageIDs(), languageID)
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

// Set 在 installer 或 bundle 解析到新二进制后更新后续 client 使用的路径。
// 空路径会被忽略，避免把已验证的可执行文件覆盖成不可启动状态。
func (b *runtimeBinaryOverride) Set(path string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if trimmed := strings.TrimSpace(path); trimmed != "" {
		b.value = trimmed
	}
}

// Get 返回当前语言 server 二进制路径。
// 读锁保证并发创建 client 时能看到一致的路径字符串。
func (b *runtimeBinaryOverride) Get() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.value
}

type runtimeBinaryManager struct {
	multilsp.Manager
	binary *runtimeBinaryOverride
}

// RegistryScopedResolver 暴露 runtime manager 的按工具作用域解析能力。
// nil receiver 返回 nil，registry 会继续使用非 scoped 路径而不是解引用 panic。
func (m *runtimeBinaryManager) RegistryScopedResolver() manager.ScopedManagerResolver {
	if m == nil {
		return nil
	}
	return multilsp.NewRegistryScopedResolver(m.Manager)
}

// ReleaseScope 将 runtimeBinaryManager 的释放请求转交给底层 ManagerPool。
// 底层不支持 ScopeReleaser 时返回零值结果，保持非池实现的兼容边界。
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

// SetBinaryPath 设置二进制路径。
func (m *runtimeBinaryManager) SetBinaryPath(path string) {
	if m != nil && m.binary != nil {
		m.binary.Set(path)
	}
}

// Close 关闭 LSP 管理器资源。
func (m *Manager) Close() error {
	if m.registry != nil {
		return m.registry.Close()
	}
	return nil
}

// ReleaseScope 将 MCP DTO 转换为 multilsp release 请求并广播给所有语言池。
// 每个语言池独立统计命中/关闭/忙碌租约，首个关闭错误会随聚合结果返回给调用方。
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

// Run 启动LSP后台流程。
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
