// Package main 提供 mcp-lsp sidecar 的工具注册、语言服务安装解析与进程生命周期编排。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
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
	lspDiagnosticsColdStartMaxWait              = 8 * time.Second
	jstsTSServerFallbackPath                    = "tsserver"
	sqruffInstallVersion                        = "0.38.0"
	typeScriptLanguageServerInstallVersion      = "5.3.0"
	typeScriptInstallVersion                    = "5.9.3"
	vscodeLangserversExtractedInstallVersion    = "4.10.0"
	vscodeMarkdownLanguageServiceInstallVersion = "0.5.0-alpha.11"
	pyrightInstallVersion                       = "1.1.412"
	yamlLanguageServerInstallVersion            = "1.24.0"
	vueLanguageServerInstallVersion             = "3.3.9"
	svelteLanguageServerInstallVersion          = "0.18.4"
	intelephenseInstallVersion                  = "1.18.5"
	dockerfileLanguageServerInstallVersion      = "0.15.0"
	graphqlLanguageServiceCLIInstallVersion     = "3.5.0"
	prismaLanguageServerInstallVersion          = "31.11.0"
	astGrepInstallVersion                       = "0.43.0"
	bashLanguageServerInstallVersion            = "5.6.0"
	shellcheckInstallVersion                    = "4.1.0"
	runtimeWindowsNodeInstallLockKey            = "windows-node-runtime-npm-cohort"
)

// Manager 聚合 MCP 工具注册表、工作区根目录以及由应用 runner 托管的 LSP 后台生命周期。
type Manager struct {
	registry          manager.Registry
	root              string
	astGrepEnsurer    func(context.Context) (string, error)
	backgroundRunners []platformrunner.Runner
	releaseScopes     []multilsp.ScopeReleaser
}

// BackgroundRunners 返回需要随应用 rungroup 启停的 LSP 后台 runner；nil Manager 返回 nil。
func (m *Manager) BackgroundRunners() []platformrunner.Runner {
	if m == nil {
		return nil
	}
	return m.backgroundRunners
}

// newManager 根据已解析配置建立全部语言工具，并把 manager pool 的释放职责绑定到 Manager。
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
	inst, err := runtimeInstaller(packagedLSP)
	if err != nil {
		return nil, err
	}
	astGrepEnsurer, err := runtimeASTGrepEnsurer(inst, packagedLSP)
	if err != nil {
		return nil, err
	}
	languageIDs, err := runtimePrimaryLanguageIDsForBundle(adapters, lspBundle, packagedLSP)
	if err != nil {
		return nil, err
	}

	registry := manager.NewRegistry(inst)
	backgroundRunners, releaseScopes, err := registerRuntimeLanguages(
		registry, adapters, languageIDs, root, log, resolvedLSPConfig.IdleTimeout, lspBundle, packagedLSP,
	)
	if err != nil {
		return nil, err
	}
	return &Manager{registry: registry, root: root, astGrepEnsurer: astGrepEnsurer, backgroundRunners: backgroundRunners, releaseScopes: releaseScopes}, nil
}

// registerRuntimeLanguages 注册全部主语言，并聚合后台 runner 与 scope 清理器。
func registerRuntimeLanguages(registry interface {
	Register(string, manager.Manager, ...manager.ScopedManagerResolver)
	RegisterNoInstall(string, manager.Manager, ...manager.ScopedManagerResolver)
}, adapters *multilsp.LanguageAdapterRegistry, languageIDs []string, root string, log *slog.Logger, idleTimeout time.Duration, lspBundle runtimeenv.LSPBundle, packagedLSP bool) ([]platformrunner.Runner, []multilsp.ScopeReleaser, error) {
	backgroundRunners := make([]platformrunner.Runner, 0, len(languageIDs))
	releaseScopes := make([]multilsp.ScopeReleaser, 0, len(languageIDs))
	for _, primaryLanguageID := range languageIDs {
		adapter, ok := adapters.AdapterForLanguage(primaryLanguageID)
		if !ok {
			return nil, nil, errors.New("missing LSP language adapter: " + primaryLanguageID)
		}
		runner, releaser, err := registerRuntimeAdapter(registry, adapter, adapters, root, log, idleTimeout, lspBundle, packagedLSP)
		if err != nil {
			return nil, nil, err
		}
		backgroundRunners = appendBackgroundRunner(backgroundRunners, runner)
		releaseScopes = appendReleaseScopeReleaser(releaseScopes, releaser)
	}

	return backgroundRunners, releaseScopes, nil
}

func requireResolvedLSPConfig(cfg contract.LSPConfig) (contract.LSPConfig, error) {
	if cfg.IdleTimeout <= 0 {
		return contract.LSPConfig{}, errors.New("resolved LSP idle timeout is required")
	}
	return cfg, nil
}

func runtimeInstaller(packagedLSP bool) (*installer.Provider, error) {
	if packagedLSP {
		return nil, nil
	}
	return setupInstallerWithError()
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

// runtimeWorkspaceRootsFromEnv 读取运行时工作区环境变量并返回去重后的绝对根目录。
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

// normalizeRuntimeWorkspaceRoots 清理、绝对化并去重工作区根目录，非法路径直接返回错误。
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
		"shellscript", "proto", "sql",
	}
}

// runtimePrimaryLanguageIDsForBundle 计算当前适配器与可选打包清单共同允许注册的主语言集合。
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

// registerRuntimeAdapter 为一个主语言建立 manager pool，并把七个 MCP 工具族绑定到注册表。
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

// bundledAdapterServer 从已校验的打包清单解析适配器对应的唯一语言服务，不允许猜测或 PATH 回退。
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

// setupInstallerWithError 注册按需安装各语言 LSP server，并把平台清单错误返回调用方。
func setupInstallerWithError() (*installer.Provider, error) {
	inst := installer.NewProvider()
	if err := registerPlatformNativeArtifactInstallers(inst); err != nil {
		return nil, err
	}
	if err := registerLinuxCSharpInstaller(inst); err != nil {
		return nil, err
	}
	// 平台专用注册函数由 runtime_installer_windows.go / runtime_installer_nonwindows.go
	// 的显式 build tag 选择；公共装配层只调用已编译的平台入口。
	registerPlatformProductionInstallers(inst)

	return inst, nil
}

// setupInstaller 构造生产安装器注册表，并由平台文件注入各自保持隔离的安装策略。
func setupInstaller() *installer.Provider {
	inst, err := setupInstallerWithError()
	if err != nil {
		panic(err)
	}
	return inst
}

type runtimeInstallerSpec struct {
	languages                   []string
	binaryName                  string
	installCmd                  string
	args                        []string
	installCommandResolver      func(context.Context) (string, error)
	installArgsResolver         func(context.Context) ([]string, error)
	installedBinaryPathResolver func(context.Context) (string, error)
	installedReadinessValidator func(context.Context) error
	installLockKey              string
}

// runtimeNPMCommandForPlatform 是纯映射：只按显式 goos 参数返回 npm 启动文件名，不读取系统状态、不执行 PATH 探测。
func runtimeNPMCommandForPlatform(goos string) string {
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return "npm.cmd"
	}
	return "npm"
}

// runtimeNPMExecutableNameForPlatform 是纯映射：只按显式 goos 与 binaryName 参数生成 npm bin shim 文件名，不执行系统调用。
func runtimeNPMExecutableNameForPlatform(goos, binaryName string) string {
	binaryName = strings.TrimSpace(binaryName)
	if binaryName == "" {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(goos), "windows") && filepath.Ext(binaryName) == "" {
		return binaryName + ".cmd"
	}
	return binaryName
}

// runtimeNPMInstallArgs 构造只接受精确版本包名的 npm 全局安装参数。
func runtimeNPMInstallArgs(packages ...string) []string {
	args := make([]string, 0, 2+len(packages))
	args = append(args, "install", "-g")
	return append(args, packages...)
}

func registerInstallerSpecs(inst *installer.Provider, specs []runtimeInstallerSpec) {
	for _, spec := range specs {
		cfg := installer.InstallerConfig{
			BinaryName:                  spec.binaryName,
			InstallCmd:                  spec.installCmd,
			InstallArgs:                 append([]string(nil), spec.args...),
			InstallCommandResolver:      spec.installCommandResolver,
			InstallArgsResolver:         spec.installArgsResolver,
			InstalledBinaryPathResolver: spec.installedBinaryPathResolver,
			InstalledReadinessValidator: spec.installedReadinessValidator,
			InstallLockKey:              spec.installLockKey,
			AllowInstallCommand:         true,
		}
		for _, languageID := range spec.languages {
			inst.Register(languageID, cfg)
		}
	}
}

func runtimeNPMExactPackages(args []string) ([]string, error) {
	if len(args) < 3 || args[0] != "install" || args[1] != "-g" {
		return nil, errors.New("npm installer must use the exact install -g argument shape")
	}
	packages := append([]string(nil), args[2:]...)
	if len(packages) == 0 {
		return nil, errors.New("npm installer has no exact package specifications")
	}
	return packages, nil
}

// runtimeNonWindowsNPMInstallerSpecs 保留 Linux/macOS 既有的 PATH npm 包参数；Windows 锁定安装不得改变此契约。
func runtimeNonWindowsNPMInstallerSpecs() []runtimeInstallerSpec {
	extractedArgs := []string{
		"install", "-g", "vscode-langservers-extracted",
		"vscode-markdown-languageservice@" + vscodeMarkdownLanguageServiceInstallVersion,
	}
	return []runtimeInstallerSpec{
		{languages: []string{"javascript", "javascriptreact", "typescript", "typescriptreact"}, binaryName: "typescript-language-server", installCmd: "npm", args: []string{
			"install", "-g",
			"typescript-language-server@" + typeScriptLanguageServerInstallVersion,
			"typescript@" + typeScriptInstallVersion,
		}},
		{languages: []string{"python"}, binaryName: "pyright-langserver", installCmd: "npm", args: []string{"install", "-g", "pyright"}},
		{languages: []string{"css"}, binaryName: "vscode-css-language-server", installCmd: "npm", args: append([]string(nil), extractedArgs...)},
		{languages: []string{"html"}, binaryName: "vscode-html-language-server", installCmd: "npm", args: append([]string(nil), extractedArgs...)},
		{languages: []string{"json"}, binaryName: "vscode-json-language-server", installCmd: "npm", args: append([]string(nil), extractedArgs...)},
		{languages: []string{"yaml"}, binaryName: "yaml-language-server", installCmd: "npm", args: []string{"install", "-g", "yaml-language-server"}},
		{languages: []string{"markdown"}, binaryName: "vscode-markdown-language-server", installCmd: "npm", args: append([]string(nil), extractedArgs...)},
		{languages: []string{"vue"}, binaryName: "vue-language-server", installCmd: "npm", args: []string{"install", "-g", "@vue/language-server"}},
		{languages: []string{"svelte"}, binaryName: "svelteserver", installCmd: "npm", args: []string{"install", "-g", "svelte-language-server"}},
		{languages: []string{"php"}, binaryName: "intelephense", installCmd: "npm", args: []string{"install", "-g", "intelephense"}},
		{languages: []string{"dockerfile"}, binaryName: "docker-langserver", installCmd: "npm", args: []string{"install", "-g", "dockerfile-language-server-nodejs"}},
		{languages: []string{"graphql"}, binaryName: "graphql-lsp", installCmd: "npm", args: []string{"install", "-g", "graphql-language-service-cli"}},
		{languages: []string{"prisma"}, binaryName: "prisma-language-server", installCmd: "npm", args: []string{"install", "-g", "@prisma/language-server"}},
	}
}

// runtimeNPMInstallerSpecsForPlatform 是纯映射：只按显式 goos 返回 Windows 锁定 cohort 或历史 PATH npm 参数；
// 不读取宿主 runtime、不联网、不执行安装。生产入口必须从带 build tag 的 companion 进入。
func runtimeNPMInstallerSpecsForPlatform(goos string) []runtimeInstallerSpec {
	if !strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return runtimeNonWindowsNPMInstallerSpecs()
	}
	npmCommand := runtimeNPMCommandForPlatform(goos)
	return []runtimeInstallerSpec{
		{
			languages:  []string{"javascript", "javascriptreact", "typescript", "typescriptreact"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "typescript-language-server"),
			installCmd: npmCommand,
			args: runtimeNPMInstallArgs(
				"typescript-language-server@"+typeScriptLanguageServerInstallVersion,
				"typescript@"+typeScriptInstallVersion,
			),
		},
		{
			languages:  []string{"python"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "pyright-langserver"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("pyright@" + pyrightInstallVersion),
		},
		{
			languages:  []string{"css"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "vscode-css-language-server"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("vscode-langservers-extracted@" + vscodeLangserversExtractedInstallVersion),
		},
		{
			languages:  []string{"html"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "vscode-html-language-server"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("vscode-langservers-extracted@" + vscodeLangserversExtractedInstallVersion),
		},
		{
			languages:  []string{"json"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "vscode-json-language-server"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("vscode-langservers-extracted@" + vscodeLangserversExtractedInstallVersion),
		},
		{
			languages:  []string{"markdown"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "vscode-markdown-language-server"),
			installCmd: npmCommand,
			args: runtimeNPMInstallArgs(
				"vscode-langservers-extracted@"+vscodeLangserversExtractedInstallVersion,
				"vscode-markdown-languageservice@"+vscodeMarkdownLanguageServiceInstallVersion,
				"markdown-it@"+runtimeMarkdownItInstallVersion,
			),
		},
		{
			languages:  []string{"yaml"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "yaml-language-server"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("yaml-language-server@" + yamlLanguageServerInstallVersion),
		},
		{
			languages:  []string{"vue"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "vue-language-server"),
			installCmd: npmCommand,
			args: runtimeNPMInstallArgs(
				"@vue/language-server@"+vueLanguageServerInstallVersion,
				"typescript-language-server@"+typeScriptLanguageServerInstallVersion,
				"typescript@"+typeScriptInstallVersion,
			),
		},
		{
			languages:  []string{"svelte"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "svelteserver"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("svelte-language-server@" + svelteLanguageServerInstallVersion),
		},
		{
			languages:  []string{"php"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "intelephense"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("intelephense@" + intelephenseInstallVersion),
		},
		{
			languages:  []string{"dockerfile"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "docker-langserver"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("dockerfile-language-server-nodejs@" + dockerfileLanguageServerInstallVersion),
		},
		{
			languages:  []string{"graphql"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "graphql-lsp"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("graphql-language-service-cli@" + graphqlLanguageServiceCLIInstallVersion),
		},
		{
			languages:  []string{"prisma"},
			binaryName: runtimeNPMExecutableNameForPlatform(goos, "prisma-language-server"),
			installCmd: npmCommand,
			args:       runtimeNPMInstallArgs("@prisma/language-server@" + prismaLanguageServerInstallVersion),
		},
	}
}

func registerNativeToolInstallers(inst *installer.Provider) {
	registerInstallerSpecs(inst, []runtimeInstallerSpec{
		{languages: contract.ClangdLanguageIDs(), binaryName: "clangd", installCmd: "brew", args: []string{"install", "llvm"}},
		{languages: []string{"swift"}, binaryName: "sourcekit-lsp", installCmd: "brew", args: []string{"install", "swift"}},
		{languages: []string{"proto"}, binaryName: "buf", installCmd: "brew", args: []string{"install", "buf"}},
		{languages: []string{"csharp"}, binaryName: "csharp-ls", installCmd: "dotnet", args: []string{"tool", "install", "--global", "csharp-ls"}},
		{languages: []string{"ruby"}, binaryName: "solargraph", installCmd: "brew", args: []string{"install", "solargraph"}},
		{languages: []string{"kotlin"}, binaryName: "kotlin-language-server", installCmd: "brew", args: []string{"install", "kotlin-language-server"}},
		{languages: []string{"dart"}, binaryName: "dart", installCmd: "brew", args: []string{"install", "dart-sdk"}},
		{languages: []string{"lua"}, binaryName: "lua-language-server", installCmd: "brew", args: []string{"install", "lua-language-server"}},
		{languages: []string{"terraform"}, binaryName: "terraform-ls", installCmd: "brew", args: []string{"install", "hashicorp/tap/terraform-ls"}},
		{languages: []string{"rust"}, binaryName: "rust-analyzer", installCmd: "rustup", args: []string{"component", "add", "rust-analyzer"}},
		{languages: []string{"java"}, binaryName: "jdtls", installCmd: "brew", args: []string{"install", "jdtls"}},
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

func registerSQLInstaller(inst *installer.Provider) {
	inst.Register("sql", installer.InstallerConfig{
		BinaryName:          "sqruff",
		BinaryCheckArgs:     []string{"--version"},
		InstallCmd:          "cargo",
		InstallArgs:         []string{"install", "sqruff", "--version", sqruffInstallVersion, "--locked"},
		AllowInstallCommand: true,
	})
}

// runtimeNonWindowsShellInstallerConfig 保留 Linux/macOS 原有的 PATH npm 与未加版本后缀的包参数。
func runtimeNonWindowsShellInstallerConfig() installer.InstallerConfig {
	return installer.InstallerConfig{
		BinaryName:          "bash-language-server",
		InstallCmd:          "npm",
		InstallArgs:         []string{"install", "-g", "bash-language-server", "shellcheck"},
		AllowInstallCommand: true,
		RequiredBinaries: []installer.RequiredBinary{
			{Name: "shellcheck", CheckArgs: []string{"--version"}},
		},
	}
}

// runtimeWindowsArchitecture 是纯映射：只按显式架构别名返回 Windows catalog 键，不读取宿主架构或执行系统调用。
func runtimeWindowsArchitecture(goarch string) string {
	switch strings.ToLower(strings.TrimSpace(goarch)) {
	case "arm64", "aarch64":
		return "arm64"
	case "amd64", "x64", "x86_64":
		return "x64"
	case "386", "x86", "i386", "i686":
		return "x86"
	default:
		return ""
	}
}

// runtimeShellcheckNPMAvailableForTarget 是纯映射：只按显式 goos/goarch 判断锁定 shellcheck 包是否有原生二进制。
func runtimeShellcheckNPMAvailableForTarget(goos, goarch string) bool {
	if !strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return true
	}
	return runtimeWindowsArchitecture(goarch) == "x64"
}

// runtimeShellNPMInstallerConfigForTarget 是纯映射：只按显式 goos/goarch 构造 shell 安装配置，不读取宿主事实或执行系统调用。
func runtimeShellNPMInstallerConfigForTarget(goos, goarch string) installer.InstallerConfig {
	if !strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return runtimeNonWindowsShellInstallerConfig()
	}
	packages := []string{"bash-language-server@" + bashLanguageServerInstallVersion}
	var requiredBinaries []installer.RequiredBinary
	if runtimeShellcheckNPMAvailableForTarget(goos, goarch) {
		packages = append(packages, "shellcheck@"+shellcheckInstallVersion)
		requiredBinaries = []installer.RequiredBinary{
			{Name: runtimeNPMExecutableNameForPlatform(goos, "shellcheck"), CheckArgs: []string{"--version"}},
		}
	}
	return installer.InstallerConfig{
		BinaryName:          runtimeNPMExecutableNameForPlatform(goos, "bash-language-server"),
		InstallCmd:          runtimeNPMCommandForPlatform(goos),
		InstallArgs:         runtimeNPMInstallArgs(packages...),
		AllowInstallCommand: true,
		RequiredBinaries:    requiredBinaries,
	}
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

// createGenericManagerWithBinary 用显式二进制覆盖建立通用 LSP manager，并保留适配器参数与生命周期策略。
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
	goplsRootController, err := runtimeServerNewPlatformGoplsRootCohortController(command)
	if err != nil {
		return nil, err
	}
	mgr, err := multilsp.NewManagerWithError(multilsp.Config{
		WorkspaceRoot:                    root,
		LanguageAdapters:                 adapters,
		IdleTimeout:                      idleTimeout,
		DiagnosticsMaxWait:               runtimeAdapterDiagnosticsMaxWait(adapter),
		DisableInitialWorkspaceBootstrap: true,
		ClientFactory: multilsp.ClientFactoryWithOptionsFunc(func(rootDir string, env []string, initOptions map[string]any, h protocol.NotificationHandler) (multilsp.Client, error) {
			return createRuntimeLSPClient(
				adapter,
				command,
				root,
				packagedLSP,
				binary,
				rootDir,
				env,
				initOptions,
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

// runtimeAdapterUsesNode 判断适配器的语言集合是否需要由受管 Node runtime 启动。
func runtimeAdapterUsesNode(adapter multilsp.LanguageAdapter) bool {
	if adapter == nil {
		return false
	}
	return slices.ContainsFunc(adapter.LanguageIDs(), runtimeLanguageUsesNode)
}

// runtimeLanguageUsesNode 判断规范 language ID 是否属于锁定 npm 语言服务集合。
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

// runtimeAdapterInitOptions 根据适配器命令推导初始化参数；无二进制覆盖时保留历史行为。
func runtimeAdapterInitOptions(adapter multilsp.LanguageAdapter, packagedLSP bool) map[string]any {
	return runtimeAdapterInitOptionsWithBinary(adapter, packagedLSP, "")
}

// runtimeAdapterInitOptionsWithBinary 为需要本地模块路径的 Node 适配器构造可复现初始化参数。
func runtimeAdapterInitOptionsWithBinary(adapter multilsp.LanguageAdapter, packagedLSP bool, serverBinary string) map[string]any {
	return runtimeResolvedAdapterInitOptionsWithBinary(adapter, nil, packagedLSP, serverBinary)
}

// runtimeResolvedAdapterInitOptionsWithBinary 合并解析后的初始化参数，并为受管 JS/TS 与打包 Python 注入稳定运行约束。
func runtimeResolvedAdapterInitOptionsWithBinary(adapter multilsp.LanguageAdapter, resolved map[string]any, packagedLSP bool, serverBinary string) map[string]any {
	initOptions := multilsp.CloneInitOptions(resolved)
	if len(initOptions) == 0 {
		initOptions = adapter.InitOptions(multilsp.ResolvedLanguageScope{})
	}
	initOptions = multilsp.CloneInitOptions(initOptions)
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

// runtimeJSTSInitOptions 为 JavaScript/TypeScript 服务解析工作区 SDK 与受管 TypeScript 模块路径。
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

// runtimeTypeScriptModuleRoot 优先使用工作区 TypeScript，否则从受管语言服务二进制反推同 cohort 模块目录。
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

// typeScriptModuleRootFromBinary 只接受 node_modules/.bin 内的显式 shim，并返回同一安装前缀下的 TypeScript 包目录。
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
