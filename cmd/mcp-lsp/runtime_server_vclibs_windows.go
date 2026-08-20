//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const (
	runtimeServerWindowsMSVCRuntimeDirEnv = "SUPER_DOLPHIN_MSVC_RUNTIME_DIR"
	runtimeServerWindowsLocalAppDataEnv   = "LOCALAPPDATA"
	runtimeServerWindowsAppDataEnv        = "APPDATA"
)

// runtimeServerWindowsMkdirAll 是产品私有用户态目录创建的窄测试 seam；生产值固定为 os.MkdirAll。
var runtimeServerWindowsMkdirAll = os.MkdirAll

// runtimeServerPlatformDependencyEnvironment 为产品私有 Windows LSP 子进程注入
// 已安装且复验过的 VC++ Desktop 8.3 目录。外部语言服务器保持调用方环境；产品
// cache 缺少依赖时则 fail-fast，禁止查询系统 PATH 或在只读启动阶段联网修复。
func runtimeServerPlatformDependencyEnvironment(serverBinary, workspaceRoot string, env []string) ([]string, error) {
	result, err := runtimeServerWindowsVCLibsEnvironmentWithResolver(serverBinary, env, resolveRuntimeWindowsVCLibsProcessPath)
	if err != nil {
		return nil, err
	}
	result, err = runtimeServerWindowsNodeCohortEnvironmentWithResolver(serverBinary, result, installer.ResolveWindowsNodeRuntimeExecutablePath)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(workspaceRoot) == "" {
		// Keep the compatibility seam live for callers that do not provide a workspace.
		result, err = runtimeServerWindowsDotnetEnvironment(serverBinary, result)
	} else {
		result, err = runtimeServerWindowsDotnetEnvironmentForWorkspace(serverBinary, workspaceRoot, result)
	}
	if err != nil {
		return nil, err
	}
	result, err = runtimeServerWindowsGoSQLSEnvironment(serverBinary, result)
	if err != nil {
		return nil, err
	}
	result, err = runtimeServerWindowsSwiftEnvironment(serverBinary, result)
	if err != nil {
		return nil, err
	}
	result, err = runtimeServerWindowsRubyLSPEnvironment(serverBinary, result)
	if err != nil {
		return nil, err
	}
	result, err = runtimeServerWindowsTerraformEnvironment(serverBinary, result)
	if err != nil {
		return nil, err
	}
	result, err = runtimeServerWindowsClangdEnvironment(serverBinary, result)
	if err != nil {
		return nil, err
	}
	return runtimeServerWindowsRustEnvironment(serverBinary, result)
}

// runtimeServerWindowsDotnetEnvironment 为无 workspace 的旧启动 seam 注入同 cohort 的 .NET。
// 需要项目 TargetFramework 校验的生产 C# workspace 走 workspace-aware seam。
func runtimeServerWindowsDotnetEnvironment(serverBinary string, env []string) ([]string, error) {
	return runtimeServerWindowsDotnetEnvironmentForWorkspace(serverBinary, "", env)
}

// runtimeServerWindowsDotnetEnvironmentForWorkspace 将受管 .NET cohort 绑定到 csharp-ls，
// 并在存在 C# 项目时校验 workspace 的 TargetFramework 与 cohort reference pack 一致。
func runtimeServerWindowsDotnetEnvironmentForWorkspace(serverBinary, workspaceRoot string, env []string) ([]string, error) {
	return runtimeServerWindowsDotnetEnvironmentWithResolverForWorkspace(serverBinary, workspaceRoot, env, func(productRoot string) (installer.WindowsRuntimeDependencyProvisionResult, error) {
		return installer.ResolveWindowsRuntimeDependency(
			installer.WindowsRuntimeDependencyProductDotnetCsharpLS,
			windowsRuntimeDependencyCacheRoot(productRoot),
		)
	})
}

// runtimeServerWindowsDotnetEnvironmentWithResolver 是受管 .NET 启动环境的只读测试
// seam。外部 csharp-ls 保持调用方环境，只有产品根内且身份一致的 server 才会覆盖变量。
func runtimeServerWindowsDotnetEnvironmentWithResolver(
	serverBinary string,
	env []string,
	resolver func(string) (installer.WindowsRuntimeDependencyProvisionResult, error),
) ([]string, error) {
	return runtimeServerWindowsDotnetEnvironmentWithResolverForWorkspace(serverBinary, "", env, resolver)
}

// runtimeServerWindowsDotnetEnvironmentWithResolverForWorkspace 是带 workspace TargetFramework
// 校验的受管 .NET 启动 seam；空 workspace 保持旧的环境绑定测试语义。
func runtimeServerWindowsDotnetEnvironmentWithResolverForWorkspace(
	serverBinary, workspaceRoot string,
	env []string,
	resolver func(string) (installer.WindowsRuntimeDependencyProvisionResult, error),
) ([]string, error) {
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return nil, err
	}
	if !owned || !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "csharp-ls.exe") {
		return append([]string(nil), env...), nil
	}
	log := pkglogger.Get()
	log.Info("C# managed runtime stage", "stage", "cohort_resolve_begin")
	if resolver == nil {
		return nil, errors.New("Windows managed .NET runtime resolver is nil")
	}
	resolved, err := resolver(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve product-owned .NET cohort for csharp-ls: %w", err)
	}
	log.Info("C# managed runtime stage", "stage", "cohort_resolve_ready")
	if resolved.Product != installer.WindowsRuntimeDependencyProductDotnetCsharpLS ||
		!strings.EqualFold(filepath.Clean(resolved.ServerPath), filepath.Clean(serverBinary)) {
		return nil, errors.New("product-owned csharp-ls server identity changed")
	}
	canonicalRoot := filepath.Clean(resolved.RootPath)
	if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, canonicalRoot); err != nil {
		return nil, fmt.Errorf("validate product-owned .NET root: %w", err)
	}
	processRoot, err := installer.MaterializeWindowsCSharpProcessRoot(productRoot, canonicalRoot)
	if err != nil {
		return nil, fmt.Errorf("materialize product-owned .NET process root: %w", err)
	}
	log.Info("C# managed runtime stage", "stage", "cohort_process_root_ready", "canonical_root", securefs.RedactPath(canonicalRoot), "process_root", securefs.RedactPath(processRoot), "process_root_materialized", !strings.EqualFold(canonicalRoot, filepath.Clean(processRoot)))
	// resolver 返回的隔离 NuGet/.NET 状态属于同一已复验 cohort。
	// 子进程环境中的 cohort 路径必须改指物理短根，不能把 canonical 长路径泄漏回去。
	env = appendRuntimeServerEnvironment(env, rebaseRuntimeServerWindowsEnvironmentPaths(resolved.Env, canonicalRoot, processRoot))
	// csharp-ls inherits the MCP process environment. Do not mix a host SDK10
	// MSBuildSDKsPath with net8 resolver/targeting-pack overrides: that
	// combination makes MSBuild lose the framework references. The managed
	// cohort remains discoverable through DOTNET_ROOT, PATH, and the validated
	// product layout below.
	env = removeRuntimeServerWindowsCSharpEnvironment(env,
		"MSBuildSDKsPath",
		"DOTNET_MSBUILD_SDK_RESOLVER_SDKS_DIR",
		"NetCoreTargetingPackRoot",
		"TargetFrameworkRootPath",
	)
	_, net8SDKsErr := os.Stat(filepath.Join(canonicalRoot, "sdk", "8.0.424", "Sdks"))
	net8ReferencePackPath := filepath.Join(canonicalRoot, "packs", "Microsoft.NETCore.App.Ref", "8.0.30")
	_, net8ReferencePackErr := os.Stat(net8ReferencePackPath)
	net10SDKPath := filepath.Join(canonicalRoot, "sdk", "10.0.400")
	_, net10SDKErr := os.Stat(net10SDKPath)
	log.Info("C# managed runtime stage", "stage", "msbuild_resolution_inputs_ready", "net8_sdk_present", net8SDKsErr == nil, "net8_reference_pack_present", net8ReferencePackErr == nil, "net10_host_sdk_present", net10SDKErr == nil, "msbuild_sdks_env_present", runtimeServerWindowsEnvironmentValue(env, "MSBuildSDKsPath") != "", "resolver_sdks_env_present", runtimeServerWindowsEnvironmentValue(env, "DOTNET_MSBUILD_SDK_RESOLVER_SDKS_DIR") != "", "targeting_pack_root_env_present", runtimeServerWindowsEnvironmentValue(env, "NetCoreTargetingPackRoot") != "", "framework_root_env_present", runtimeServerWindowsEnvironmentValue(env, "TargetFrameworkRootPath") != "")
	dotnetExecutable := filepath.Join(canonicalRoot, "dotnet.exe")
	if info, statErr := os.Stat(dotnetExecutable); statErr != nil {
		return nil, fmt.Errorf("validate product-owned dotnet.exe %s: %w", securefs.RedactPath(dotnetExecutable), statErr)
	} else if info.IsDir() {
		return nil, errors.New("product-owned dotnet.exe is not a file")
	}
	for _, path := range []string{
		filepath.Join(canonicalRoot, "shared", "Microsoft.NETCore.App"),
		filepath.Join(canonicalRoot, "sdk"),
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return nil, fmt.Errorf("validate product-owned .NET runtime directory %s: %w", securefs.RedactPath(path), statErr)
		}
		if !info.IsDir() {
			return nil, errors.New("product-owned .NET runtime path is not a directory")
		}
	}
	if strings.TrimSpace(workspaceRoot) != "" {
		log.Info("C# managed runtime stage", "stage", "target_framework_validation_begin")
		if err := installer.ValidateWindowsCSharpTargetFrameworkReferencePacks(processRoot, workspaceRoot); err != nil {
			return nil, fmt.Errorf("validate C# TargetFramework reference packs: %w", err)
		}
		log.Info("C# managed runtime stage", "stage", "target_framework_validation_ready")
	}
	architectureVariable := map[string]string{
		installer.WindowsHostArchARM64: "DOTNET_ROOT_ARM64",
		installer.WindowsHostArchX64:   "DOTNET_ROOT_X64",
		installer.WindowsHostArchX86:   "DOTNET_ROOT_X86",
	}[resolved.Architecture]
	if architectureVariable == "" {
		return nil, fmt.Errorf("product-owned .NET architecture is unsupported: %q", resolved.Architecture)
	}
	env = replaceRuntimeServerWindowsEnvironment(env, "DOTNET_ROOT", processRoot)
	env = replaceRuntimeServerWindowsEnvironment(env, architectureVariable, processRoot)
	pathValue := runtimeServerWindowsEnvironmentValue(env, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	managedPath := processRoot
	if pathValue != "" {
		managedPath += string(os.PathListSeparator) + pathValue
	}
	log.Info("C# managed runtime stage", "stage", "child_environment_ready")
	return replaceRuntimeServerWindowsEnvironment(env, "PATH", managedPath), nil
}

// runtimeServerWindowsNodeCohortEnvironmentWithResolver 为产品私有 npm .cmd shim
// 前置同一锁定 cohort 的 node.exe 目录；外部 shim 与原生 EXE 保持调用方环境。
func runtimeServerWindowsNodeCohortEnvironmentWithResolver(
	serverBinary string,
	env []string,
	resolver func(string) (string, error),
) ([]string, error) {
	if !strings.EqualFold(filepath.Ext(filepath.Clean(serverBinary)), ".cmd") {
		return append([]string(nil), env...), nil
	}
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return nil, err
	}
	if !owned {
		return append([]string(nil), env...), nil
	}
	if resolver == nil {
		return nil, errors.New("Windows managed Node executable resolver is nil")
	}
	nodePath, err := resolver(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve product-owned Node executable for npm language server: %w", err)
	}
	if !filepath.IsAbs(nodePath) || !strings.EqualFold(filepath.Base(nodePath), "node.exe") {
		return nil, fmt.Errorf("product-owned Node executable path is invalid: %s", securefs.RedactPath(nodePath))
	}
	nodeDir := filepath.Dir(nodePath)
	pathValue := runtimeServerWindowsEnvironmentValue(env, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	if pathValue != "" {
		nodeDir += string(os.PathListSeparator) + pathValue
	}
	return replaceRuntimeServerWindowsEnvironment(env, "PATH", nodeDir), nil
}

// runtimeServerWindowsRustEnvironment 仅为产品根内锁定的 rust-analyzer 注入同 cohort 的
// Cargo、Rustc 与 rustfmt/cargo-fmt；外部 binary 原样保留。
func runtimeServerWindowsRustEnvironment(serverBinary string, env []string) ([]string, error) {
	return runtimeServerWindowsRustEnvironmentWithResolvers(
		serverBinary,
		env,
		installer.ResolveWindowsRustfmtPath,
		installer.ResolveWindowsRustToolchain,
	)
}

// runtimeServerWindowsRustEnvironmentWithResolvers 是 Rust companion 环境的只读测试 seam。
// 两个 resolver 都必须命中同一产品根，禁止从机器 PATH 拼接未锁定的 Cargo/Rustc。
func runtimeServerWindowsRustEnvironmentWithResolvers(
	serverBinary string,
	env []string,
	rustfmtResolver func(string) (string, error),
	toolchainResolver func(string) (installer.WindowsRustToolchainPaths, error),
) ([]string, error) {
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil || !owned || !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "rust-analyzer.exe") {
		return append([]string(nil), env...), err
	}
	if rustfmtResolver == nil || toolchainResolver == nil {
		return nil, errors.New("Windows managed Rust companion resolver is nil")
	}
	rustfmtPath, err := rustfmtResolver(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve product-owned Rustfmt for rust-analyzer: %w", err)
	}
	toolchain, err := toolchainResolver(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve product-owned Rust toolchain for rust-analyzer: %w", err)
	}
	result := installer.WindowsRustToolchainEnvironment(env, toolchain)
	rustfmtDir := installer.WindowsRustfmtBinDir(rustfmtPath)
	pathValue := runtimeServerWindowsEnvironmentValue(result, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	if pathValue != "" {
		rustfmtDir += string(os.PathListSeparator) + pathValue
	}
	return replaceRuntimeServerWindowsEnvironment(result, "PATH", rustfmtDir), nil
}

// runtimeServerWindowsTerraformEnvironment 仅为产品根内已锁定的 terraform-ls 注入同 cohort 的
// Terraform CLI 目录；外部 terraform-ls 保持原环境，绝不从机器 PATH 选择或补装 CLI。
func runtimeServerWindowsTerraformEnvironment(serverBinary string, env []string) ([]string, error) {
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil || !owned || !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "terraform-ls.exe") {
		return append([]string(nil), env...), err
	}
	cliPath, err := installer.ResolveWindowsTerraformCLIPath(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve product-owned Terraform CLI for terraform-ls: %w", err)
	}
	cliDir := filepath.Dir(cliPath)
	pathValue := runtimeServerWindowsEnvironmentValue(env, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	if pathValue != "" {
		cliDir += string(os.PathListSeparator) + pathValue
	}
	return replaceRuntimeServerWindowsEnvironment(env, "PATH", cliDir), nil
}

// runtimeServerWindowsGoSQLSEnvironment 将 GoSQLS 的 APPDATA 绑定到已校验的产品 cohort。
// SQLS 使用 os.UserConfigDir 读取配置；不绑定时会依赖系统用户环境，甚至在 APPDATA 缺失时直接 panic。
// 只有生产 resolver 返回的同一 sqls.exe 才能进入该分支，外部或同名二进制保持原环境并拒绝伪装。
func runtimeServerWindowsGoSQLSEnvironment(serverBinary string, env []string) ([]string, error) {
	return runtimeServerWindowsGoSQLSEnvironmentWithResolver(serverBinary, env, func(productRoot string) (installer.WindowsRuntimeDependencyProvisionResult, error) {
		return installer.ResolveWindowsRuntimeDependency(
			installer.WindowsRuntimeDependencyProductGoSQLS,
			windowsRuntimeDependencyCacheRoot(productRoot),
		)
	})
}

// runtimeServerWindowsGoSQLSEnvironmentWithResolver 为 APPDATA 注入提供只读 resolver seam，避免测试下载或修改生产 cache。
func runtimeServerWindowsGoSQLSEnvironmentWithResolver(
	serverBinary string,
	env []string,
	resolver func(string) (installer.WindowsRuntimeDependencyProvisionResult, error),
) ([]string, error) {
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return nil, err
	}
	if !owned || !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), installer.WindowsGoSQLSBinaryName) {
		return append([]string(nil), env...), nil
	}
	if resolver == nil {
		return nil, fmt.Errorf("Windows Go SQLS runtime resolver is nil")
	}
	resolved, err := resolver(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve product-owned Windows Go SQLS cohort: %w", err)
	}
	if !strings.EqualFold(filepath.Clean(resolved.ServerPath), filepath.Clean(serverBinary)) {
		return nil, fmt.Errorf("product-owned Windows Go SQLS server identity changed: resolved=%s actual=%s", securefs.RedactPath(resolved.ServerPath), securefs.RedactPath(serverBinary))
	}
	configRoot := filepath.Join(resolved.RootPath, "config")
	if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, configRoot); err != nil {
		return nil, fmt.Errorf("product-owned Windows Go SQLS APPDATA escaped product root: %w", err)
	}
	configInfo, err := os.Lstat(configRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect product-owned Windows Go SQLS APPDATA: %w", securefs.WrapErrorForPath(err, configRoot))
	}
	if configInfo.IsDir() == false || configInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("product-owned Windows Go SQLS APPDATA is not a real directory")
	}
	return replaceRuntimeServerWindowsEnvironment(env, runtimeServerWindowsAppDataEnv, configRoot), nil
}

// runtimeServerWindowsOwnedProductRoot 只允许归属于 ResolveWindowsLSPProductRoot
// 的 binary 进入产品私有 VCLibs 和 8.3 进程边界；同名 marker 下的外部 binary
// 无法证明归属时保持调用方行为不变，避免误注入产品依赖或改写外部进程路径。
func runtimeServerWindowsOwnedProductRoot(serverBinary string) (string, bool, error) {
	candidateRoot, err := runtimeServerWindowsProductRootFromBinary(serverBinary)
	if err != nil {
		return "", false, nil
	}
	productRoot, err := runtimeenv.ResolveWindowsLSPProductRoot()
	if err != nil {
		return "", false, fmt.Errorf("resolve Windows LSP product root for server ownership: %w", err)
	}
	if !strings.EqualFold(filepath.Clean(candidateRoot), filepath.Clean(productRoot)) {
		return "", false, nil
	}
	return candidateRoot, true, nil
}

// runtimeServerWindowsVCLibsEnvironmentWithResolver 允许单测注入纯只读 resolver，
// 生产始终使用锁定 VCLibs cache resolver。
func runtimeServerWindowsVCLibsEnvironmentWithResolver(serverBinary string, env []string, resolver func(string) (string, error)) ([]string, error) {
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return nil, err
	}
	if !owned {
		return append([]string(nil), env...), nil
	}
	if resolver == nil {
		return nil, fmt.Errorf("Windows VCLibs process-directory resolver is nil")
	}
	runtimeRoot, err := resolver(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve app-local Windows VCLibs for LSP server: %w", err)
	}
	pathValue := runtimeRoot
	// multilsp 把这里返回的增量环境追加到 os.Environ 之后；当调用方未在增量
	// 环境中重复 PATH 时，后置的 VCLibs PATH 仍会覆盖父进程 PATH。必须显式
	// 折叠父进程 PATH，才能保留产品已锁定的 Node 等运行时目录。该值只用于
	// 子进程环境合成，不参与语言服务器或 VCLibs 的资产发现与解析。
	inherited := runtimeServerWindowsEnvironmentValue(env, "PATH")
	if inherited == "" {
		inherited = os.Getenv("PATH")
	}
	if inherited != "" {
		pathValue += string(os.PathListSeparator) + inherited
	}
	env = replaceRuntimeServerWindowsEnvironment(env, runtimeServerWindowsMSVCRuntimeDirEnv, runtimeRoot)
	env = replaceRuntimeServerWindowsEnvironment(env, "PATH", pathValue)
	return runtimeServerWindowsProductUserDataEnvironment(productRoot, env)
}

// runtimeServerWindowsProductUserDataEnvironment 把所有受管 Windows LSP 子进程的
// 用户态目录限制在同一 product root 内。TypeScript typingsInstaller 默认读取
// LOCALAPPDATA，npm/Node 及其他语言服务也可能读取 APPDATA；只约束 Vue 会令直接启动的
// JavaScript/TypeScript server 逃到系统用户目录。调用方已经验证 server 归属于产品根，
// 外部 binary 会在进入本函数前原样返回，因此这里必须统一覆盖而不能按语言猜测。
func runtimeServerWindowsProductUserDataEnvironment(productRoot string, env []string) ([]string, error) {
	userDataRoot := filepath.Join(productRoot, "runtime-state")
	localAppData := filepath.Join(userDataRoot, "localappdata")
	appData := filepath.Join(userDataRoot, "appdata")
	for _, item := range []struct {
		label string
		path  string
	}{
		{label: "product-owned LOCALAPPDATA", path: localAppData},
		{label: "product-owned APPDATA", path: appData},
	} {
		if err := runtimeServerWindowsMkdirAll(item.path, 0o700); err != nil {
			return nil, fmt.Errorf("create %s under Windows product root %s: %w", item.label, securefs.RedactPath(productRoot), securefs.WrapErrorForPath(err, item.path))
		}
		if _, err := installer.WindowsShortProcessPathWithinRoot(productRoot, item.path); err != nil {
			return nil, fmt.Errorf("%s escaped Windows product root %s: %w", item.label, securefs.RedactPath(productRoot), securefs.WrapErrorForPath(err, item.path))
		}
	}
	env = replaceRuntimeServerWindowsEnvironment(env, runtimeServerWindowsLocalAppDataEnv, localAppData)
	env = replaceRuntimeServerWindowsEnvironment(env, runtimeServerWindowsAppDataEnv, appData)
	return env, nil
}

// runtimeServerPlatformProcessBinary 把产品私有 Windows server 的进程边界改为
// 产品根内的物理短布局；缓存、资源 cohort 和身份校验仍使用完整路径。
func runtimeServerWindowsCSharpProcessBinary(serverBinary string) (string, bool, error) {
	if !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "csharp-ls.exe") {
		return serverBinary, false, nil
	}
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return "", true, err
	}
	if !owned {
		return serverBinary, false, nil
	}
	return runtimeServerWindowsCSharpProcessBinaryWithResolver(serverBinary, productRoot, func(root string) (installer.WindowsRuntimeDependencyProvisionResult, error) {
		return installer.ResolveWindowsRuntimeDependency(
			installer.WindowsRuntimeDependencyProductDotnetCsharpLS,
			windowsRuntimeDependencyCacheRoot(root),
		)
	})
}

func runtimeServerWindowsCSharpProcessBinaryWithResolver(
	serverBinary, productRoot string,
	resolver func(string) (installer.WindowsRuntimeDependencyProvisionResult, error),
) (string, bool, error) {
	if resolver == nil {
		return "", true, errors.New("Windows managed .NET process resolver is nil")
	}
	resolved, err := resolver(productRoot)
	if err != nil {
		return "", true, fmt.Errorf("resolve product-owned .NET cohort for C# process root: %w", err)
	}
	if resolved.Product != installer.WindowsRuntimeDependencyProductDotnetCsharpLS ||
		!strings.EqualFold(filepath.Clean(resolved.ServerPath), filepath.Clean(serverBinary)) {
		return "", true, errors.New("product-owned csharp-ls server identity changed")
	}
	canonicalRoot := filepath.Clean(resolved.RootPath)
	processRoot, err := installer.MaterializeWindowsCSharpProcessRoot(productRoot, canonicalRoot)
	if err != nil {
		return "", true, fmt.Errorf("materialize product-owned C# process root: %w", err)
	}
	relative, err := filepath.Rel(canonicalRoot, filepath.Clean(serverBinary))
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", true, errors.New("product-owned csharp-ls server escaped materialized cohort")
	}
	processBinary := filepath.Join(processRoot, relative)
	if info, statErr := os.Stat(processBinary); statErr != nil {
		return "", true, fmt.Errorf("validate materialized csharp-ls server: %w", statErr)
	} else if info.IsDir() {
		return "", true, errors.New("materialized csharp-ls server is a directory")
	}
	return processBinary, true, nil
}

func runtimeServerPlatformProcessBinary(serverBinary string) (string, error) {
	if kotlinBinary, handled, err := runtimeServerWindowsKotlinProcessBinary(serverBinary); err != nil {
		return "", err
	} else if handled {
		return kotlinBinary, nil
	}
	if csharpBinary, handled, err := runtimeServerWindowsCSharpProcessBinary(serverBinary); err != nil {
		return "", err
	} else if handled {
		return csharpBinary, nil
	}
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil {
		return "", err
	}
	if !owned {
		return serverBinary, nil
	}
	processBinary, err := installer.WindowsShortProcessPathWithinRoot(productRoot, serverBinary)
	if err != nil {
		return "", fmt.Errorf("resolve Windows LSP server process path: %w", err)
	}
	return processBinary, nil
}

// runtimeServerWindowsEnvironmentValue 按 Windows 环境变量大小写不敏感语义取最后值。
func runtimeServerWindowsEnvironmentValue(env []string, key string) string {
	value := ""
	for _, entry := range env {
		entryKey, candidate, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(entryKey, key) {
			value = candidate
		}
	}
	return value
}

// replaceRuntimeServerWindowsEnvironment 按大小写不敏感语义替换单个子进程变量。
func replaceRuntimeServerWindowsEnvironment(env []string, key, value string) []string {
	result := make([]string, 0, len(env)+1)
	for _, entry := range env {
		entryKey, _, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(entryKey, key) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, key+"="+value)
}

// removeRuntimeServerWindowsCSharpEnvironment removes all case-insensitive matches
// for variables that must not leak from the MCP process into a managed child.
func removeRuntimeServerWindowsCSharpEnvironment(env []string, keys ...string) []string {
	blocked := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		blocked[strings.ToLower(key)] = struct{}{}
	}
	result := make([]string, 0, len(env))
	for _, entry := range env {
		entryKey, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, found := blocked[strings.ToLower(entryKey)]; found {
				continue
			}
		}
		result = append(result, entry)
	}
	return result
}

// rebaseRuntimeServerWindowsEnvironmentPaths keeps product-owned path values
// inside the physical short cohort used by the managed child.
func rebaseRuntimeServerWindowsEnvironmentPaths(env []string, sourceRoot, targetRoot string) []string {
	sourceRoot = filepath.Clean(sourceRoot)
	targetRoot = filepath.Clean(targetRoot)
	sourcePrefix := strings.ToLower(sourceRoot + string(filepath.Separator))
	result := make([]string, 0, len(env))
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || strings.TrimSpace(value) == "" {
			result = append(result, entry)
			continue
		}
		candidate := filepath.Clean(value)
		candidateLower := strings.ToLower(candidate)
		if strings.EqualFold(candidate, sourceRoot) || strings.HasPrefix(candidateLower, sourcePrefix) {
			if relative, err := filepath.Rel(sourceRoot, candidate); err == nil &&
				relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
				entry = key + "=" + filepath.Join(targetRoot, relative)
			}
		}
		result = append(result, entry)
	}
	return result
}
