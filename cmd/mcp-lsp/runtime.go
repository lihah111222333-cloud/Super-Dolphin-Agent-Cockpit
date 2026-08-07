// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/protocol"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	lspDiagnosticsColdStartMaxWait         = 8 * time.Second
	jstsTSServerFallbackPath               = "tsserver"
	sqruffInstallVersion                   = "0.38.0"
	typeScriptLanguageServerInstallVersion = "5.3.0"
	typeScriptInstallVersion               = "5.9.3"
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
	resolvedLSPConfig, err := requireResolvedLSPConfig(cfg.LSP)
	if err != nil {
		return nil, err
	}
	if err := multilsp.ValidateResourceLimitEnvironment(); err != nil {
		return nil, err
	}
	root, err := runtimeRoot()
	if err != nil {
		return nil, err
	}
	log := pkglogger.Get()
	adapters := multilsp.NewLanguageAdapterRegistryFromConfig(resolvedLSPConfig)
	lspBundle, packagedLSP, err := runtimeenv.LoadLSPBundleFromEnv()
	if err != nil {
		return nil, err
	}
	inst := runtimeInstaller(packagedLSP)
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
		runner, releaser, err := registerRuntimeAdapter(registry, adapter, adapters, root, log, resolvedLSPConfig.IdleTimeout, lspBundle, packagedLSP)
		if err != nil {
			return nil, err
		}
		backgroundRunners = appendBackgroundRunner(backgroundRunners, runner)
		releaseScopes = appendReleaseScopeReleaser(releaseScopes, releaser)
	}

	return &Manager{registry: registry, root: root, backgroundRunners: backgroundRunners, releaseScopes: releaseScopes}, nil
}

func requireResolvedLSPConfig(cfg contract.LSPConfig) (contract.LSPConfig, error) {
	if cfg.IdleTimeout <= 0 {
		return contract.LSPConfig{}, errors.New("resolved LSP idle timeout is required")
	}
	return cfg, nil
}

func runtimeInstaller(packagedLSP bool) *installer.Provider {
	if packagedLSP {
		return nil
	}
	return setupInstaller()
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
	return []string{
		"go", "javascript", "python", "css", "html", "json", "yaml", "markdown",
		"vue", "svelte", "c", "swift", "csharp", "php", "ruby", "kotlin", "dart",
		"lua", "dockerfile", "terraform", "graphql", "prisma", "rust", "java",
		"shellscript", "sql",
	}
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
	idleTimeout time.Duration,
	lspBundle runtimeenv.LSPBundle,
	packagedLSP bool,
) (platformrunner.Runner, multilsp.ScopeReleaser, error) {
	if !adapter.CapabilityPolicy().RequiresLSPClient {
		mgr, err := createFallbackManager(adapters, root, log, idleTimeout)
		if err != nil {
			return nil, nil, err
		}
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
	mgr, err := createGenericManagerWithBinary(adapter, adapters, root, log, idleTimeout, binaryOverride, packagedLSP)
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
	registerNPMInstallers(inst)
	registerNativeToolInstallers(inst)
	registerGoInstallers(inst)
	registerShellAndSQLInstallers(inst)

	return inst
}

type runtimeInstallerSpec struct {
	languages  []string
	binaryName string
	installCmd string
	args       []string
}

func registerInstallerSpecs(inst *installer.Provider, specs []runtimeInstallerSpec) {
	for _, spec := range specs {
		cfg := installer.InstallerConfig{
			BinaryName:          spec.binaryName,
			InstallCmd:          spec.installCmd,
			InstallArgs:         spec.args,
			AllowInstallCommand: true,
		}
		for _, languageID := range spec.languages {
			inst.Register(languageID, cfg)
		}
	}
}

func registerNPMInstallers(inst *installer.Provider) {
	extractedArgs := []string{
		"install", "-g", "vscode-langservers-extracted",
		"vscode-markdown-languageservice@0.5.0-alpha.11",
	}
	registerInstallerSpecs(inst, []runtimeInstallerSpec{
		{[]string{"javascript", "javascriptreact", "typescript", "typescriptreact"}, "typescript-language-server", "npm", []string{
			"install", "-g",
			"typescript-language-server@" + typeScriptLanguageServerInstallVersion,
			"typescript@" + typeScriptInstallVersion,
		}},
		{[]string{"python"}, "pyright-langserver", "npm", []string{"install", "-g", "pyright"}},
		{[]string{"css"}, "vscode-css-language-server", "npm", extractedArgs},
		{[]string{"html"}, "vscode-html-language-server", "npm", extractedArgs},
		{[]string{"json"}, "vscode-json-language-server", "npm", extractedArgs},
		{[]string{"yaml"}, "yaml-language-server", "npm", []string{"install", "-g", "yaml-language-server"}},
		{[]string{"markdown"}, "vscode-markdown-language-server", "npm", extractedArgs},
		{[]string{"vue"}, "vue-language-server", "npm", []string{"install", "-g", "@vue/language-server"}},
		{[]string{"svelte"}, "svelteserver", "npm", []string{"install", "-g", "svelte-language-server"}},
		{[]string{"php"}, "intelephense", "npm", []string{"install", "-g", "intelephense"}},
		{[]string{"dockerfile"}, "docker-langserver", "npm", []string{"install", "-g", "dockerfile-language-server-nodejs"}},
		{[]string{"graphql"}, "graphql-lsp", "npm", []string{"install", "-g", "graphql-language-service-cli"}},
		{[]string{"prisma"}, "prisma-language-server", "npm", []string{"install", "-g", "@prisma/language-server"}},
	})
}

func registerNativeToolInstallers(inst *installer.Provider) {
	registerInstallerSpecs(inst, []runtimeInstallerSpec{
		{[]string{"c", "cpp", "objective-c", "objective-cpp"}, "clangd", "brew", []string{"install", "llvm"}},
		{[]string{"swift"}, "sourcekit-lsp", "brew", []string{"install", "swift"}},
		{[]string{"csharp"}, "csharp-ls", "dotnet", []string{"tool", "install", "--global", "csharp-ls"}},
		{[]string{"ruby"}, "solargraph", "brew", []string{"install", "solargraph"}},
		{[]string{"kotlin"}, "kotlin-language-server", "brew", []string{"install", "kotlin-language-server"}},
		{[]string{"dart"}, "dart", "brew", []string{"install", "dart-sdk"}},
		{[]string{"lua"}, "lua-language-server", "brew", []string{"install", "lua-language-server"}},
		{[]string{"terraform"}, "terraform-ls", "brew", []string{"install", "hashicorp/tap/terraform-ls"}},
		{[]string{"rust"}, "rust-analyzer", "rustup", []string{"component", "add", "rust-analyzer"}},
		{[]string{"java"}, "jdtls", "brew", []string{"install", "jdtls"}},
	})
}

func registerGoInstallers(inst *installer.Provider) {
	cfg := installer.InstallerConfig{
		BinaryName:          "gopls",
		InstallCmd:          "go",
		InstallArgs:         []string{"install", "golang.org/x/tools/gopls@latest"},
		AllowInstallCommand: true,
	}
	for _, languageID := range []string{"go", "gomod", "gosum", "gowork"} {
		inst.Register(languageID, cfg)
	}
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
		BinaryName:          "sqruff",
		BinaryCheckArgs:     []string{"--version"},
		InstallCmd:          "cargo",
		InstallArgs:         []string{"install", "sqruff", "--version", sqruffInstallVersion, "--locked"},
		AllowInstallCommand: true,
	})
}

func createFallbackManager(adapters *multilsp.LanguageAdapterRegistry, root string, log *slog.Logger, idleTimeout time.Duration) (multilsp.Manager, error) {
	return multilsp.NewManagerWithError(multilsp.Config{
		WorkspaceRoot:                    root,
		LanguageAdapters:                 adapters,
		IdleTimeout:                      idleTimeout,
		Logger:                           log,
		DisableInitialWorkspaceBootstrap: true,
	})
}

// createGenericManagerWithBinary 创建可热更新二进制路径的通用 multilsp manager。
// ClientFactory 按每次解析出的 workspace root 设置子进程 Dir，避免不同项目共享同一启动目录。
func createGenericManagerWithBinary(adapter multilsp.LanguageAdapter, adapters *multilsp.LanguageAdapterRegistry, root string, log *slog.Logger, idleTimeout time.Duration, binaryOverride string, packagedLSP bool) (multilsp.Manager, error) {
	command, err := adapter.ServerCommand(context.Background(), multilsp.ResolvedLanguageScope{})
	if err != nil {
		return nil, err
	}
	initialBinary := runtimeServerBinary(command.Executable, binaryOverride)
	if strings.TrimSpace(initialBinary) == "" {
		return nil, errors.New("language adapter server command is empty")
	}
	binary := &runtimeBinaryOverride{value: initialBinary}
	var goplsRootController multilsp.GoplsRootCohortController
	if runtime.GOOS != "windows" && runtimeServerUsesSharedGoplsDaemon(command) {
		goplsRootController, err = runtimeServerNewDurableGoplsRootCohortController()
		if err != nil {
			return nil, err
		}
	}
	mgr, err := multilsp.NewManagerWithError(multilsp.Config{
		WorkspaceRoot:                    root,
		LanguageAdapters:                 adapters,
		IdleTimeout:                      idleTimeout,
		DiagnosticsMaxWait:               runtimeAdapterDiagnosticsMaxWait(adapter),
		DisableInitialWorkspaceBootstrap: true,
		ClientFactory: multilsp.ClientFactoryWithEnvFunc(func(rootDir string, env []string, h protocol.NotificationHandler) (multilsp.Client, error) {
			return createRuntimeLSPClient(
				adapter,
				command,
				root,
				packagedLSP,
				binary,
				rootDir,
				env,
				goplsRootController,
				h,
			)
		}),
		Logger: log,
	})
	if err != nil {
		return nil, err
	}
	return &runtimeBinaryManager{Manager: mgr, binary: binary, goplsRootController: goplsRootController}, nil
}

func runtimeAdapterDiagnosticsMaxWait(adapter multilsp.LanguageAdapter) time.Duration {
	if adapter.CapabilityPolicy().RequiresLSPClient {
		return lspDiagnosticsColdStartMaxWait
	}
	return 0
}

const packagedPyrightNoSystemPythonPath = "/__super_dolphin_no_system_python__/python"

const (
	runtimeJSTSMaxMemoryMB     = runtimeNodeMaxOldSpaceMB
	runtimeJSTSUseSyntaxServer = "never"
)

// runtimeAdapterUsesNode 只为已知 Node 驱动的 adapter 开启 Node 专属堆与编译缓存策略。
func runtimeAdapterUsesNode(adapter multilsp.LanguageAdapter) bool {
	if adapter == nil {
		return false
	}
	for _, languageID := range adapter.LanguageIDs() {
		if runtimeLanguageUsesNode(languageID) {
			return true
		}
	}
	return false
}

// runtimeLanguageUsesNode 判断标准化后的语言标识是否由 Node 驱动的 LSP 处理。
func runtimeLanguageUsesNode(languageID string) bool {
	switch strings.ToLower(strings.TrimSpace(languageID)) {
	case "css", "dockerfile", "graphql", "html",
		"javascript", "javascriptreact", "json", "markdown",
		"php", "prisma", "python", "shellscript",
		"svelte", "typescript", "typescriptreact", "vue", "yaml":
		return true
	default:
		return false
	}
}

// runtimeAdapterInitOptions 复制适配器 init options 并补充打包 LSP 的运行约束。
// 打包 Pyright 使用哨兵 pythonPath，防止它隐式探测系统 Python 造成跨环境差异。
func runtimeAdapterInitOptions(adapter multilsp.LanguageAdapter, packagedLSP bool) map[string]any {
	return runtimeAdapterInitOptionsWithBinary(adapter, packagedLSP, "")
}

// runtimeAdapterInitOptionsWithBinary 在客户端创建时使用安装器最终解析出的服务路径补齐运行选项。
func runtimeAdapterInitOptionsWithBinary(adapter multilsp.LanguageAdapter, packagedLSP bool, serverBinary string) map[string]any {
	initOptions := adapter.InitOptions(multilsp.ResolvedLanguageScope{})
	initOptions = runtimeJSTSInitOptions(adapter, initOptions, serverBinary)
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

// runtimeJSTSInitOptions 为 JS/TS language server 注入受管 tsserver 运行策略和后备路径。
// 每个 worktree 保留独立语义服务，但禁用额外 syntax server，并由跨进程 cohort RSS 总账约束总内存。
func runtimeJSTSInitOptions(adapter multilsp.LanguageAdapter, initOptions map[string]any, serverBinary string) map[string]any {
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
	initOptions["maxTsServerMemory"] = runtimeJSTSMaxMemoryMB
	tsserver["useSyntaxServer"] = runtimeJSTSUseSyntaxServer
	configuredPath := runtimeStringOption(tsserver["path"])
	fallbackPath := runtimeStringOption(tsserver["fallbackPath"])
	if configuredPath == "" && (fallbackPath == "" || fallbackPath == jstsTSServerFallbackPath) {
		fallbackPath = jstsTSServerFallbackPath
		if resolved := runtimeTypeScriptModuleRoot(serverBinary); resolved != "" {
			fallbackPath = resolved
		}
		tsserver["fallbackPath"] = fallbackPath
	}
	return initOptions
}

// runtimeTypeScriptModuleRoot 从 npm 安装的语言服务器或 tsserver 入口定位 TypeScript 包根。
// typescript-language-server 的 fallbackPath 需要包含 lib/tsserver.js 的包目录，而不是可执行文件。
func runtimeTypeScriptModuleRoot(serverBinary string) string {
	binaryPaths := []string{strings.TrimSpace(serverBinary)}
	for _, binaryName := range []string{"typescript-language-server", jstsTSServerFallbackPath} {
		if binaryPath, err := exec.LookPath(binaryName); err == nil {
			binaryPaths = append(binaryPaths, binaryPath)
		}
	}
	for _, binaryPath := range binaryPaths {
		if binaryPath == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(binaryPath)
		if err != nil {
			resolved = binaryPath
		}
		if target, ok := installer.CommandShimTarget(resolved); ok {
			resolved = target
		}
		if root := typeScriptModuleRootFromBinary(resolved); root != "" {
			return root
		}
	}
	return ""
}

// typeScriptModuleRootFromBinary 沿 npm 包路径向上寻找 node_modules/typescript。
func typeScriptModuleRootFromBinary(binaryPath string) string {
	prefix := filepath.Dir(filepath.Dir(filepath.Clean(binaryPath)))
	for _, candidate := range []string{
		filepath.Join(prefix, "node_modules", "typescript"),
		filepath.Join(prefix, "lib", "node_modules", "typescript"),
	} {
		if isTypeScriptModuleRoot(candidate) {
			return candidate
		}
	}
	for dir := filepath.Dir(filepath.Clean(binaryPath)); ; dir = filepath.Dir(dir) {
		if filepath.Base(dir) == "node_modules" {
			candidate := filepath.Join(dir, "typescript")
			if isTypeScriptModuleRoot(candidate) {
				return candidate
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
	}
}

func isTypeScriptModuleRoot(candidate string) bool {
	info, err := os.Stat(filepath.Join(candidate, "lib", "tsserver.js"))
	return err == nil && info.Mode().IsRegular()
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
