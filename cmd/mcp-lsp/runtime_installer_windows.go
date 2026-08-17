//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
)

const windowsProductionInstallTimeout = 45 * time.Minute

// runtimeNPMCommand 返回 Windows 的 npm.cmd 启动文件名；Windows 选择由本文件的 build tag 固定。
func runtimeNPMCommand() string {
	return runtimeNPMCommandForPlatform("windows")
}

// runtimeNPMExecutableName 返回 Windows npm bin shim 文件名；不读取 runtime.GOOS。
func runtimeNPMExecutableName(binaryName string) string {
	return runtimeNPMExecutableNameForPlatform("windows", binaryName)
}

// registerNPMInstallers 为 Windows 早期注册阶段提供锁定 cohort 的静态规格；最终生产配置由
// registerWindowsNodeRuntimeInstallers 绑定产品根、InstallAction 和 readiness resolver。
func registerNPMInstallers(inst *installer.Provider) {
	registerInstallerSpecs(inst, runtimeNPMInstallerSpecsForPlatform("windows"))
}

// registerShellAndSQLInstallers 只保留 SQL 的公共初始注册；Windows shell 在后续
// registerWindowsShellRuntimeInstaller 中绑定 NativeArch、锁定 cohort 和生命周期钩子。
func registerShellAndSQLInstallers(inst *installer.Provider) {
	registerSQLInstaller(inst)
}

// runtimeShellNPMInstallerConfigForProduction 是 Windows 专用生产 selector：它读取
// DetectWindowsHostPlatform 的 NativeArch，禁止用 runtime.GOARCH 或跨架构 fallback。
func runtimeShellNPMInstallerConfigForProduction(goos string) (installer.InstallerConfig, error) {
	if !strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return runtimeNonWindowsShellInstallerConfig(), nil
	}
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		return installer.InstallerConfig{}, err
	}
	if !strings.EqualFold(host.OS, "windows") || strings.TrimSpace(host.NativeArch) == "" {
		return installer.InstallerConfig{}, errors.New("detected host platform is not a supported native Windows platform")
	}
	cfg := runtimeShellNPMInstallerConfigForTarget(goos, host.NativeArch)
	if !runtimeShellcheckNPMAvailableForTarget(goos, host.NativeArch) {
		cfg.OptionalUnsupportedPlatform = &installer.UnsupportedPlatformError{
			Feature:    "shellcheck npm companion",
			OS:         host.OS,
			NativeArch: host.NativeArch,
		}
	}
	return cfg, nil
}

// registerPlatformProductionInstallers 注册 Windows 原生 catalog、Windows runtime
// dependency 和锁定 Node/npm cohort。所有联网/写盘动作都只放进 InstallAction；
// setupInstaller 本身只解析宿主事实和可变产品根目录，不走 PATH 或兼容架构。
func registerPlatformProductionInstallers(inst *installer.Provider) {
	// 这些公共规格只在 Windows tagged boundary 中进入生产注册；后续 Windows
	// runtime/native registration 会按锁定产品根和 NativeArch 覆盖对应语言。
	registerNPMInstallers(inst)
	registerNativeToolInstallers(inst)
	registerGoInstallers(inst)
	registerShellAndSQLInstallers(inst)
	productRoot, productRootErr := runtimeenv.ResolveWindowsLSPProductRoot()
	registerWindowsNodeRuntimeInstallers(inst, productRoot, productRootErr)
	registerWindowsNativeCatalogInstallers(inst, productRoot, productRootErr)
	registerWindowsRuntimeDependencyInstallers(inst, productRoot, productRootErr)
	registerWindowsShellRuntimeInstaller(inst, productRoot, productRootErr)
	registerWindowsASTGrepRuntimeInstaller(inst, productRoot, productRootErr)
}

const runtimeASTGrepLanguageID = "__mcp_lsp_ast_grep__"

func registerWindowsASTGrepRuntimeInstaller(inst *installer.Provider, productRoot string, productRootErr error) {
	if inst == nil {
		return
	}
	platform, platformErr := installer.DetectWindowsHostPlatform()
	spec := runtimeInstallerSpec{
		binaryName: "ast-grep.exe",
	}
	cfg := installer.InstallerConfig{
		BinaryName:                  spec.binaryName,
		AllowInstallCommand:         true,
		InstallTimeout:              windowsProductionInstallTimeout,
		InstallLockKey:              runtimeWindowsASTGrepInstallLockKey,
		InstallAction:               windowsASTGrepInstallAction(productRoot, productRootErr),
		InstalledBinaryPathResolver: windowsASTGrepBinaryPathResolver(productRoot, productRootErr),
		InstalledReadinessValidator: windowsASTGrepReadinessValidator(productRoot, productRootErr),
	}
	if platformErr != nil {
		cfg.UnsupportedPlatform = platformErr
	} else if _, err := installer.WindowsASTGrepAssetForPlatform(platform); err != nil {
		cfg.UnsupportedPlatform = &installer.UnsupportedPlatformError{
			Feature:    "ast-grep",
			OS:         platform.OS,
			NativeArch: platform.NativeArch,
		}
	}
	inst.Register(runtimeASTGrepLanguageID, cfg)
}

const runtimeWindowsASTGrepInstallLockKey = "windows-ast-grep-native"

func windowsASTGrepInstallAction(productRoot string, productRootErr error) installer.InstallAction {
	return func(ctx context.Context) (installer.InstallResult, error) {
		if productRootErr != nil {
			return installer.InstallResult{}, productRootErr
		}
		path, err := installer.EnsureWindowsASTGrepNativeExecutable(ctx, productRoot, nil)
		if err != nil {
			return installer.InstallResult{}, err
		}
		return installer.InstallResult{Path: path}, nil
	}
}

func windowsASTGrepBinaryPathResolver(productRoot string, productRootErr error) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return "", err
		}
		if productRootErr != nil {
			return "", productRootErr
		}
		platform, err := installer.DetectWindowsHostPlatform()
		if err != nil {
			return "", err
		}
		path, err := installer.WindowsASTGrepNativeExecutablePath(productRoot, platform.NativeArch)
		if err != nil {
			return "", err
		}
		if err := installer.ValidateWindowsASTGrepExecutable(path, platform.NativeArch); err != nil {
			return "", err
		}
		return path, nil
	}
}

func windowsASTGrepReadinessValidator(productRoot string, productRootErr error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return err
		}
		if productRootErr != nil {
			return productRootErr
		}
		platform, err := installer.DetectWindowsHostPlatform()
		if err != nil {
			return err
		}
		asset, err := installer.WindowsASTGrepAssetForPlatform(platform)
		if err != nil {
			return err
		}
		nativePath, err := installer.WindowsASTGrepNativeExecutablePath(productRoot, asset.Architecture)
		if err != nil {
			return err
		}
		if err := installer.ValidateWindowsASTGrepExecutable(nativePath, asset.Architecture); err != nil {
			return err
		}
		return nil
	}
}

func registerWindowsNodeRuntimeInstallers(inst *installer.Provider, productRoot string, productRootErr error) {
	for _, spec := range runtimeNPMInstallerSpecsForPlatform("windows") {
		packages, packagesErr := runtimeNPMExactPackages(spec.args)
		packagesCopy := append([]string(nil), packages...)
		cfg := windowsNodeRuntimeInstallerConfig(productRoot, productRootErr, spec, packagesCopy, packagesErr)
		for _, languageID := range spec.languages {
			inst.Register(languageID, cfg)
		}
	}
}

func windowsNodeRuntimeInstallerConfig(productRoot string, productRootErr error, spec runtimeInstallerSpec, packages []string, packagesErr error) installer.InstallerConfig {
	binaryName := spec.binaryName
	return installer.InstallerConfig{
		BinaryName:                  binaryName,
		AllowInstallCommand:         true,
		InstallTimeout:              windowsProductionInstallTimeout,
		InstallLockKey:              runtimeWindowsNodeInstallLockKey,
		InstallAction:               windowsNodeInstallAction(productRoot, productRootErr, binaryName, packages, packagesErr),
		InstalledBinaryPathResolver: windowsNodeBinaryPathResolver(productRoot, productRootErr, binaryName),
		InstalledReadinessValidator: windowsNodeReadinessValidator(productRoot, productRootErr, packages, packagesErr),
	}
}

func windowsNodeInstallAction(productRoot string, productRootErr error, binaryName string, packages []string, packagesErr error) installer.InstallAction {
	return func(ctx context.Context) (installer.InstallResult, error) {
		if productRootErr != nil {
			return installer.InstallResult{}, productRootErr
		}
		if packagesErr != nil {
			return installer.InstallResult{}, packagesErr
		}
		if err := ensureWindowsVCLibsForInstall(ctx, productRoot); err != nil {
			return installer.InstallResult{}, err
		}
		nodeRuntime, err := installer.NewWindowsNodeRuntime(productRoot, nil)
		if err != nil {
			return installer.InstallResult{}, err
		}
		paths, err := nodeRuntime.InstallWindowsNodeRuntimeExactPackages(ctx, packages)
		if err != nil {
			return installer.InstallResult{}, err
		}
		binaryPath, err := nodeRuntime.BinaryPath(ctx, binaryName)
		if err != nil {
			return installer.InstallResult{}, err
		}
		if filepath.Clean(binaryPath) == filepath.Clean(paths.NPMPath) {
			return installer.InstallResult{}, errors.New("Windows Node npm install returned its npm executable as the LSP binary")
		}
		return installer.InstallResult{Path: binaryPath}, nil
	}
}

func windowsNodeBinaryPathResolver(productRoot string, productRootErr error, binaryName string) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return "", err
		}
		if productRootErr != nil {
			return "", productRootErr
		}
		path, err := installer.ResolveWindowsNodeRuntimeBinaryPath(productRoot, binaryName)
		if err != nil {
			return "", err
		}
		if err := validateWindowsVCLibsForResolver(productRoot); err != nil {
			return "", err
		}
		return path, nil
	}
}

func windowsNodeReadinessValidator(productRoot string, productRootErr error, packages []string, packagesErr error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return err
		}
		if productRootErr != nil {
			return productRootErr
		}
		if packagesErr != nil {
			return packagesErr
		}
		return installer.ValidateWindowsNodeRuntimeExactPackages(productRoot, packages)
	}
}

func registerWindowsNativeCatalogInstallers(inst *installer.Provider, productRoot string, productRootErr error) {
	nativeSpecs := []windowsNativeCatalogInstallerSpec{
		{product: installer.WindowsLSPProductClangd, languages: contract.ClangdLanguageIDs(), binary: "clangd.exe"},
		{product: installer.WindowsLSPProductBuf, languages: []string{"protobuf", "proto", "proto3"}, binary: "buf.exe"},
		{product: installer.WindowsLSPProductKotlin, languages: []string{"kotlin"}, binary: "intellij-server.exe"},
		{product: installer.WindowsLSPProductDart, languages: []string{"dart"}, binary: "dart.exe"},
		{product: installer.WindowsLSPProductTerraform, languages: []string{"terraform"}, binary: "terraform-ls.exe"},
		{product: installer.WindowsLSPProductRustAnalyzer, languages: []string{"rust"}, binary: "rust-analyzer.exe"},
	}
	for _, spec := range nativeSpecs {
		cfg := windowsNativeCatalogInstallerConfig(productRoot, productRootErr, spec)
		for _, languageID := range spec.languages {
			inst.Register(languageID, cfg)
		}
	}
	inst.Register("lua", windowsLuaInstallerConfig(productRoot, productRootErr))
}

func windowsLuaInstallerConfig(productRoot string, productRootErr error) installer.InstallerConfig {
	luaLSCfg := windowsNativeCatalogInstallerConfig(productRoot, productRootErr, windowsNativeCatalogInstallerSpec{
		product: installer.WindowsLSPProductLuaLanguageLS,
		binary:  "lua-language-server.exe",
	})
	host, err := installer.DetectWindowsHostPlatform()
	if err != nil {
		luaLSCfg.UnsupportedPlatform = err
		return luaLSCfg
	}
	binaryName, err := windowsLuaBinaryForArchitecture(host.NativeArch)
	if err != nil {
		luaLSCfg.UnsupportedPlatform = err
		return luaLSCfg
	}
	if binaryName == installer.WindowsEmmyLuaBinaryName {
		cfg := windowsEmmyLuaInstallerConfig(productRoot, productRootErr)
		if _, err := installer.WindowsEmmyLuaAssetForPlatform(host); err != nil {
			cfg.UnsupportedPlatform = err
		}
		return cfg
	}
	return luaLSCfg
}

func windowsLuaBinaryForArchitecture(architecture string) (string, error) {
	normalized, err := installer.NormalizeWindowsArchitectureAlias(architecture)
	if err != nil {
		return "", err
	}
	if normalized == installer.WindowsHostArchARM64 {
		return installer.WindowsEmmyLuaBinaryName, nil
	}
	return "lua-language-server.exe", nil
}

func windowsEmmyLuaInstallerConfig(productRoot string, productRootErr error) installer.InstallerConfig {
	return installer.InstallerConfig{
		BinaryName:                  installer.WindowsEmmyLuaBinaryName,
		AllowInstallCommand:         true,
		InstallTimeout:              windowsProductionInstallTimeout,
		InstallAction:               windowsEmmyLuaInstallAction(productRoot, productRootErr),
		InstalledBinaryPathResolver: windowsEmmyLuaBinaryPathResolver(productRoot, productRootErr),
		InstalledReadinessValidator: windowsEmmyLuaReadinessValidator(productRoot, productRootErr),
		InstallLockKey:              "windows-native-" + string(installer.WindowsLSPProductEmmyLua),
	}
}

func windowsEmmyLuaInstallAction(productRoot string, productRootErr error) installer.InstallAction {
	return func(ctx context.Context) (installer.InstallResult, error) {
		if productRootErr != nil {
			return installer.InstallResult{}, productRootErr
		}
		if err := ensureWindowsVCLibsForInstall(ctx, productRoot); err != nil {
			return installer.InstallResult{}, err
		}
		result, err := installer.ProvisionWindowsEmmyLua(ctx, productRoot, nil)
		if err != nil {
			return installer.InstallResult{}, err
		}
		return installer.InstallResult{Path: result.Executable}, nil
	}
}

func windowsEmmyLuaBinaryPathResolver(productRoot string, productRootErr error) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return "", err
		}
		if productRootErr != nil {
			return "", productRootErr
		}
		path, err := installer.ResolveWindowsEmmyLuaAssetPath(productRoot)
		if err != nil {
			return "", err
		}
		if err := validateWindowsVCLibsForResolver(productRoot); err != nil {
			return "", err
		}
		return path, nil
	}
}

func windowsEmmyLuaReadinessValidator(productRoot string, productRootErr error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return err
		}
		if productRootErr != nil {
			return productRootErr
		}
		path, err := installer.ResolveWindowsEmmyLuaAssetPath(productRoot)
		if err != nil {
			return err
		}
		return installer.ValidateWindowsEmmyLuaExecutable(path)
	}
}

type windowsNativeCatalogInstallerSpec struct {
	product   installer.WindowsLSPProduct
	languages []string
	binary    string
}

func windowsNativeCatalogInstallerConfig(productRoot string, productRootErr error, spec windowsNativeCatalogInstallerSpec) installer.InstallerConfig {
	cfg := installer.InstallerConfig{
		BinaryName:                  spec.binary,
		AllowInstallCommand:         true,
		InstallTimeout:              windowsProductionInstallTimeout,
		InstallAction:               windowsNativeInstallAction(productRoot, productRootErr, spec.product),
		InstalledBinaryPathResolver: windowsNativeBinaryPathResolver(productRoot, productRootErr, spec.product),
		InstallLockKey:              "windows-native-" + string(spec.product),
	}
	if spec.product == installer.WindowsLSPProductTerraform {
		cfg.InstalledReadinessValidator = func(ctx context.Context) error {
			if productRootErr != nil {
				return productRootErr
			}
			if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
				return err
			}
			if _, err := installer.ResolveWindowsTerraformCLIPath(productRoot); err != nil {
				return fmt.Errorf("validate product-owned Terraform CLI companion: %w", err)
			}
			return nil
		}
	}
	if spec.product == installer.WindowsLSPProductRustAnalyzer {
		cfg.InstalledReadinessValidator = func(ctx context.Context) error {
			if productRootErr != nil {
				return productRootErr
			}
			if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
				return err
			}
			if _, err := installer.ResolveWindowsRustfmtPath(productRoot); err != nil {
				return fmt.Errorf("validate product-owned Rustfmt companion: %w", err)
			}
			return nil
		}
	}
	if productRootErr == nil {
		if host, err := installer.DetectWindowsHostPlatform(); err != nil {
			cfg.UnsupportedPlatform = err
		} else if _, err := installer.WindowsLSPAssetForPlatform(spec.product, host); err != nil {
			cfg.UnsupportedPlatform = err
		}
	}
	return cfg
}

func windowsNativeInstallAction(productRoot string, productRootErr error, product installer.WindowsLSPProduct) installer.InstallAction {
	return func(ctx context.Context) (installer.InstallResult, error) {
		if productRootErr != nil {
			return installer.InstallResult{}, productRootErr
		}
		if err := ensureWindowsVCLibsForInstall(ctx, productRoot); err != nil {
			return installer.InstallResult{}, err
		}
		cache, err := installer.NewWindowsLSPAssetCache(productRoot, nil)
		if err != nil {
			return installer.InstallResult{}, err
		}
		result, err := installer.WindowsProvision(ctx, installer.WindowsProvisionRequest{Product: product, Cache: cache})
		if err != nil {
			return installer.InstallResult{}, err
		}
		if product == installer.WindowsLSPProductTerraform {
			if _, err := installer.EnsureWindowsTerraformCLI(ctx, productRoot, nil); err != nil {
				return installer.InstallResult{}, fmt.Errorf("ensure product-owned Terraform CLI companion: %w", err)
			}
		}
		if product == installer.WindowsLSPProductRustAnalyzer {
			if _, err := installer.EnsureWindowsRustfmt(ctx, productRoot, nil); err != nil {
				return installer.InstallResult{}, fmt.Errorf("ensure product-owned Rustfmt companion: %w", err)
			}
		}
		return installer.InstallResult{Path: result.Executable}, nil
	}
}

func windowsNativeBinaryPathResolver(productRoot string, productRootErr error, product installer.WindowsLSPProduct) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return "", err
		}
		if productRootErr != nil {
			return "", productRootErr
		}
		path, err := installer.ResolveWindowsLSPAssetPath(productRoot, product)
		if err != nil {
			return "", err
		}
		if err := validateWindowsVCLibsForResolver(productRoot); err != nil {
			return "", err
		}
		if product == installer.WindowsLSPProductTerraform {
			if _, err := installer.ResolveWindowsTerraformCLIPath(productRoot); err != nil {
				return "", fmt.Errorf("resolve product-owned Terraform CLI companion: %w", err)
			}
		}
		if product == installer.WindowsLSPProductRustAnalyzer {
			if _, err := installer.ResolveWindowsRustfmtPath(productRoot); err != nil {
				return "", fmt.Errorf("resolve product-owned Rustfmt companion: %w", err)
			}
		}
		return path, nil
	}
}

func registerWindowsRuntimeDependencyInstallers(inst *installer.Provider, productRoot string, productRootErr error) {
	runtimeSpecs := []struct {
		product   installer.WindowsRuntimeDependencyProduct
		languages []string
		binary    string
	}{
		{product: installer.WindowsRuntimeDependencyProductGoGopls, languages: []string{"go", "gomod", "gosum", "gowork"}, binary: "gopls.exe"},
		{product: installer.WindowsRuntimeDependencyProductDotnetCsharpLS, languages: []string{"csharp"}, binary: "csharp-ls.exe"},
		{product: installer.WindowsRuntimeDependencyProductJDKJDTLS, languages: []string{"java"}, binary: "java.exe"},
		{product: installer.WindowsRuntimeDependencyProductRubyLSP, languages: []string{"ruby"}, binary: "ruby.exe"},
		{product: installer.WindowsRuntimeDependencyProductSwiftSourceKitLS, languages: []string{"swift"}, binary: "sourcekit-lsp.exe"},
		{product: installer.WindowsRuntimeDependencyProductGoSQLS, languages: []string{"sql"}, binary: installer.WindowsGoSQLSBinaryName},
	}
	for _, spec := range runtimeSpecs {
		cfg := installer.InstallerConfig{
			BinaryName:                  spec.binary,
			AllowInstallCommand:         true,
			InstallTimeout:              windowsProductionInstallTimeout,
			InstallAction:               windowsRuntimeDependencyInstallAction(productRoot, productRootErr, spec.product),
			InstalledBinaryPathResolver: windowsRuntimeDependencyBinaryPathResolver(productRoot, productRootErr, spec.product),
			InstallLockKey:              "windows-runtime-dependency-" + string(spec.product),
		}
		if productRootErr == nil {
			if host, err := installer.DetectWindowsHostPlatform(); err != nil {
				cfg.UnsupportedPlatform = err
			} else if _, err := installer.WindowsRuntimeDependencyPlanForArchitecture(spec.product, host.NativeArch); err != nil {
				cfg.UnsupportedPlatform = err
			}
		} else {
			cfg.InstalledBinaryPathResolver = windowsRuntimeDependencyBinaryPathResolver(productRoot, productRootErr, spec.product)
		}
		for _, languageID := range spec.languages {
			inst.Register(languageID, cfg)
		}
	}
}

func windowsRuntimeDependencyCacheRoot(productRoot string) string {
	return filepath.Join(productRoot, "cache", installer.WindowsLSPAssetCacheSubdir)
}

func windowsRuntimeDependencyBinaryPathResolver(productRoot string, productRootErr error, product installer.WindowsRuntimeDependencyProduct) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return "", err
		}
		if productRootErr != nil {
			return "", productRootErr
		}
		result, err := installer.ResolveWindowsRuntimeDependency(product, windowsRuntimeDependencyCacheRoot(productRoot))
		if err != nil {
			return "", err
		}
		if err := validateWindowsVCLibsForResolver(productRoot); err != nil {
			return "", err
		}
		if product == installer.WindowsRuntimeDependencyProductJDKJDTLS || product == installer.WindowsRuntimeDependencyProductRubyLSP {
			return result.ExecutablePath, nil
		}
		return result.ServerPath, nil
	}
}

func windowsRuntimeDependencyInstallAction(productRoot string, productRootErr error, product installer.WindowsRuntimeDependencyProduct) installer.InstallAction {
	return func(ctx context.Context) (installer.InstallResult, error) {
		if productRootErr != nil {
			return installer.InstallResult{}, productRootErr
		}
		if err := ensureWindowsVCLibsForInstall(ctx, productRoot); err != nil {
			return installer.InstallResult{}, err
		}
		result, err := installer.ProvisionWindowsRuntimeDependency(ctx, product, windowsRuntimeDependencyCacheRoot(productRoot))
		if err != nil {
			return installer.InstallResult{}, err
		}
		path := result.ServerPath
		if product == installer.WindowsRuntimeDependencyProductJDKJDTLS || product == installer.WindowsRuntimeDependencyProductRubyLSP {
			path = result.ExecutablePath
		}
		if strings.TrimSpace(path) == "" {
			return installer.InstallResult{}, fmt.Errorf("Windows runtime dependency %q returned an empty launch path", product)
		}
		return installer.InstallResult{Path: path}, nil
	}
}

// ensureWindowsVCLibsForInstall 在获得 InstallAction 能力后按真实 Windows
// version/build 与 NativeArch 下载、复验锁定 VC++ Appx，并仅把同目录身份的
// 8.3 路径发布给当前安装子进程；禁止系统安装、PATH 回退或 ACL 放宽。
func ensureWindowsVCLibsForInstall(ctx context.Context, productRoot string) error {
	runtimeRoot, err := installer.ProvisionWindowsVCLibsDesktopAppLocal(ctx, productRoot, nil)
	if err != nil {
		return fmt.Errorf("provision app-local Windows VCLibs for LSP install: %w", err)
	}
	processRoot, err := installer.WindowsShortProcessPathWithinRoot(productRoot, runtimeRoot)
	if err != nil {
		return fmt.Errorf("resolve app-local Windows VCLibs install path: %w", err)
	}
	if err := runtimeenv.PrependWindowsRuntimePathEntries(processRoot); err != nil {
		return fmt.Errorf("publish app-local Windows VCLibs install PATH: %w", err)
	}
	return nil
}

// validateWindowsVCLibsForResolver 只读复验 payload SHA、Appx 身份与 ready DLL；
// cache miss 交回 InstallAction 修复，resolver 本身不联网、不建目录、不改 PATH。
func validateWindowsVCLibsForResolver(productRoot string) error {
	_, err := resolveRuntimeWindowsVCLibsProcessPath(productRoot)
	return err
}

func registerWindowsShellRuntimeInstaller(inst *installer.Provider, productRoot string, productRootErr error) {
	cfg := windowsShellRuntimeInstallerConfig(productRoot, productRootErr)
	inst.Register("shellscript", cfg)
}

func windowsShellRuntimeInstallerConfig(productRoot string, productRootErr error) installer.InstallerConfig {
	cfg, err := runtimeShellNPMInstallerConfigForProduction("windows")
	if err != nil {
		cfg = runtimeShellNPMInstallerConfigForTarget("windows", "unsupported")
		cfg.UnsupportedPlatform = err
	}
	cfg.InstallTimeout = windowsProductionInstallTimeout
	if cfg.UnsupportedPlatform != nil {
		return cfg
	}
	packages, packagesErr := runtimeNPMExactPackages(cfg.InstallArgs)
	binaryName := cfg.BinaryName
	cfg.InstallCmd = ""
	cfg.InstallArgs = nil
	cfg.InstallCommandResolver = nil
	cfg.InstallArgsResolver = nil
	cfg.InstallAction = windowsNodeInstallAction(productRoot, productRootErr, binaryName, packages, packagesErr)
	cfg.InstalledBinaryPathResolver = windowsNodeBinaryPathResolver(productRoot, productRootErr, binaryName)
	cfg.InstalledReadinessValidator = windowsNodeReadinessValidator(productRoot, productRootErr, packages, packagesErr)
	cfg.InstallLockKey = runtimeWindowsNodeInstallLockKey
	for index := range cfg.RequiredBinaries {
		name := cfg.RequiredBinaries[index].Name
		cfg.RequiredBinaries[index].PathResolver = windowsNodeBinaryPathResolver(productRoot, productRootErr, name)
	}
	return cfg
}
