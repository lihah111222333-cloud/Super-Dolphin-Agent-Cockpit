//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

type runtimeServerWindowsSwiftProvisionResolver func(string) (installer.WindowsRuntimeDependencyProvisionResult, error)

type runtimeServerWindowsSwiftShortPathResolver func(string, string) (string, error)

// runtimeServerWindowsSwiftLaunchArgs 仅为产品根内已锁定的 Swift sourcekit-lsp
// 解析同一 receipt 的 SDK/resource-dir，并在进程边界转换为安全短路径。
// 外部 sourcekit-lsp 不进入 resolver，也不改变其原始参数。
func runtimeServerWindowsSwiftLaunchArgs(serverBinary string, args []string) ([]string, error) {
	productRoot, owned, err := runtimeServerWindowsSwiftProductRoot(serverBinary)
	if err != nil || !owned {
		return append([]string(nil), args...), err
	}
	return runtimeServerWindowsSwiftLaunchArgsWithResolver(
		serverBinary,
		args,
		productRoot,
		runtimeServerWindowsSwiftResolveProvision,
		installer.WindowsShortProcessPathWithinRoot,
	)
}

// runtimeServerWindowsSwiftLaunchArgsWithResolver 供生产和 Windows 单测共享
// receipt、root containment、8.3 路径和 pinned SDK 参数守卫。
func runtimeServerWindowsSwiftLaunchArgsWithResolver(
	serverBinary string,
	args []string,
	productRoot string,
	resolve runtimeServerWindowsSwiftProvisionResolver,
	shortPath runtimeServerWindowsSwiftShortPathResolver,
) ([]string, error) {
	result, err := runtimeServerWindowsSwiftResolveAndValidate(serverBinary, productRoot, resolve, shortPath)
	if err != nil {
		return nil, err
	}
	launchArgs := append([]string(nil), result.Args...)
	if !runtimeServerWindowsSwiftHasFlag(launchArgs, "-resource-dir") {
		launchArgs = append(
			launchArgs,
			"-Xswiftc", "-resource-dir", "-Xswiftc", installer.WindowsSwiftSourceKitLSPToolchainResource(result.RootPath),
		)
	}
	launchArgs, err = runtimeServerWindowsSwiftShortenArgs(launchArgs, productRoot, shortPath)
	if err != nil {
		return nil, err
	}
	shortSDK, err := shortPath(productRoot, installer.WindowsSwiftSourceKitLSPPlatformSDK(result.RootPath))
	if err != nil {
		return nil, fmt.Errorf("resolve short Swift SDK path: %w", err)
	}
	shortResource, err := shortPath(productRoot, installer.WindowsSwiftSourceKitLSPToolchainResource(result.RootPath))
	if err != nil {
		return nil, fmt.Errorf("resolve short Swift resource path: %w", err)
	}
	if !runtimeServerWindowsSwiftHasSDKArgument(launchArgs, shortSDK) {
		return nil, fmt.Errorf("Swift sourcekit-lsp receipt args do not pin the cohort Windows SDK")
	}
	if !runtimeServerWindowsSwiftHasResourceArgument(launchArgs, shortResource) {
		return nil, fmt.Errorf("Swift sourcekit-lsp launch args do not pin the cohort resource-dir")
	}
	return launchArgs, nil
}

// runtimeServerWindowsSwiftEnvironment 仅向 owned Swift 注入 cohort root、toolchain
// bin、SOURCEKIT_TOOLCHAIN_PATH 和 SDKROOT；继承 PATH 只保留已验证的 VCLibs 与
// Windows System32 allowlist，避免机器级 Swift 污染，外部 sourcekit 行为不变。
func runtimeServerWindowsSwiftEnvironment(serverBinary string, env []string) ([]string, error) {
	productRoot, owned, err := runtimeServerWindowsSwiftProductRoot(serverBinary)
	if err != nil || !owned {
		return append([]string(nil), env...), err
	}
	return runtimeServerWindowsSwiftEnvironmentWithResolver(
		serverBinary,
		env,
		productRoot,
		runtimeServerWindowsSwiftResolveProvision,
		installer.WindowsShortProcessPathWithinRoot,
	)
}

// runtimeServerWindowsSwiftEnvironmentWithResolver 是环境注入的可测试边界。
// 任何 receipt 路径逃出 productRoot 或短路径转换失败都会 fail-fast，绝不放宽 ACL。
func runtimeServerWindowsSwiftEnvironmentWithResolver(
	serverBinary string,
	env []string,
	productRoot string,
	resolve runtimeServerWindowsSwiftProvisionResolver,
	shortPath runtimeServerWindowsSwiftShortPathResolver,
) ([]string, error) {
	result, err := runtimeServerWindowsSwiftResolveAndValidate(serverBinary, productRoot, resolve, shortPath)
	if err != nil {
		return nil, err
	}
	shortCohortRoot, err := shortPath(productRoot, result.RootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve short Swift cohort root: %w", err)
	}
	toolchainRoot := installer.WindowsSwiftSourceKitLSPToolchainRoot(result.RootPath)
	shortToolchainRoot, err := shortPath(productRoot, toolchainRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve short Swift toolchain root: %w", err)
	}
	toolchainBin := installer.WindowsSwiftSourceKitLSPToolchainBin(result.RootPath)
	shortToolchainBin, err := shortPath(productRoot, toolchainBin)
	if err != nil {
		return nil, fmt.Errorf("resolve short Swift toolchain bin: %w", err)
	}
	shortRuntimeRoot, err := shortPath(productRoot, installer.WindowsSwiftSourceKitLSPRuntimeRoot(result.RootPath))
	if err != nil {
		return nil, fmt.Errorf("resolve short Swift runtime root: %w", err)
	}
	shortSDK, err := shortPath(productRoot, installer.WindowsSwiftSourceKitLSPPlatformSDK(result.RootPath))
	if err != nil {
		return nil, fmt.Errorf("resolve short Swift SDK path: %w", err)
	}
	resultEnv := append([]string(nil), env...)
	for _, override := range result.Env {
		key, value, ok := strings.Cut(override, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("Swift receipt contains malformed environment override %q", override)
		}
		if strings.EqualFold(key, "SDKROOT") {
			if !runtimeServerWindowsSwiftPathWithinRoot(result.RootPath, value) {
				return nil, fmt.Errorf("Swift SDKROOT escapes receipt root: %q", value)
			}
			value, err = shortPath(productRoot, value)
			if err != nil {
				return nil, fmt.Errorf("resolve short Swift SDKROOT: %w", err)
			}
		}
		resultEnv = replaceRuntimeServerWindowsEnvironment(resultEnv, key, value)
	}
	resultEnv = replaceRuntimeServerWindowsEnvironment(resultEnv, "SDKROOT", shortSDK)
	resultEnv = replaceRuntimeServerWindowsEnvironment(resultEnv, "SOURCEKIT_TOOLCHAIN_PATH", shortToolchainRoot)
	pathValue := shortRuntimeRoot + string(os.PathListSeparator) + shortToolchainBin + string(os.PathListSeparator) + shortCohortRoot
	allowedInherited := runtimeServerWindowsSwiftAllowedInheritedPATH(resultEnv)
	if inherited := runtimeServerWindowsSwiftFilterInheritedPATH(runtimeServerWindowsEnvironmentValue(resultEnv, "PATH"), allowedInherited); inherited != "" {
		pathValue += string(os.PathListSeparator) + inherited
	} else if inherited := runtimeServerWindowsSwiftFilterInheritedPATH(os.Getenv("PATH"), allowedInherited); inherited != "" {
		pathValue += string(os.PathListSeparator) + inherited
	}
	return replaceRuntimeServerWindowsEnvironment(resultEnv, "PATH", pathValue), nil
}

// runtimeServerWindowsSwiftAllowedInheritedPATH 只返回 owned Swift 路径前缀
// 后仍需继承的 Windows 目录：已验证的 app-local VCLibs 和 Windows System32。
// allowlist 从子进程环境派生，不信任任意父 PATH 条目。
func runtimeServerWindowsSwiftAllowedInheritedPATH(env []string) []string {
	allowed := make([]string, 0, 2)
	if vclibs := runtimeServerWindowsEnvironmentValue(env, runtimeServerWindowsMSVCRuntimeDirEnv); vclibs != "" {
		allowed = append(allowed, vclibs)
	}
	systemRoot := runtimeServerWindowsEnvironmentValue(env, "SystemRoot")
	if systemRoot == "" {
		systemRoot = os.Getenv("SystemRoot")
	}
	if systemRoot != "" {
		allowed = append(allowed, filepath.Join(systemRoot, "System32"))
	}
	return allowed
}

// runtimeServerWindowsSwiftFilterInheritedPATH 丢弃 allowlist 之外的所有继承
// PATH。SourceKit-LSP 可能在 SOURCEKIT_TOOLCHAIN_PATH 后继续扫描 PATH；保留
// 任意父目录会让机器注入的 swift.exe/sourcekit-lsp.exe 绕过目录名检查。owned
// toolchain/runtime/cohort 根由调用方显式前缀注入，不从此继承通道接收。
func runtimeServerWindowsSwiftFilterInheritedPATH(value string, allowed []string) string {
	entries := strings.Split(value, string(os.PathListSeparator))
	kept := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" || !runtimeServerWindowsSwiftAllowedPATHEntry(entry, allowed) {
			continue
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, string(os.PathListSeparator))
}

func runtimeServerWindowsSwiftAllowedPATHEntry(entry string, allowed []string) bool {
	entry = filepath.Clean(strings.TrimSpace(entry))
	if entry == "." || !filepath.IsAbs(entry) {
		return false
	}
	for _, candidate := range allowed {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if candidate == "." || !filepath.IsAbs(candidate) {
			continue
		}
		if runtimeServerWindowsSwiftSamePath(entry, candidate) {
			return true
		}
	}
	return false
}

func runtimeServerWindowsSwiftProductRoot(serverBinary string) (string, bool, error) {
	if !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "sourcekit-lsp.exe") {
		return "", false, nil
	}
	return runtimeServerWindowsOwnedProductRoot(serverBinary)
}

func runtimeServerWindowsSwiftResolveProvision(productRoot string) (installer.WindowsRuntimeDependencyProvisionResult, error) {
	result, err := installer.ResolveWindowsRuntimeDependency(
		installer.WindowsRuntimeDependencyProductSwiftSourceKitLS,
		windowsRuntimeDependencyCacheRoot(productRoot),
	)
	if err != nil {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("resolve locked Swift sourcekit-lsp receipt: %w", err)
	}
	return result, nil
}

func runtimeServerWindowsSwiftResolveAndValidate(
	serverBinary, productRoot string,
	resolve runtimeServerWindowsSwiftProvisionResolver,
	shortPath runtimeServerWindowsSwiftShortPathResolver,
) (installer.WindowsRuntimeDependencyProvisionResult, error) {
	if resolve == nil {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("Swift receipt resolver is nil")
	}
	if shortPath == nil {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("Swift short-path resolver is nil")
	}
	result, err := resolve(productRoot)
	if err != nil {
		return installer.WindowsRuntimeDependencyProvisionResult{}, err
	}
	if result.Product != installer.WindowsRuntimeDependencyProductSwiftSourceKitLS {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("Swift resolver returned product %q", result.Product)
	}
	if !runtimeServerWindowsSwiftPathWithinRoot(productRoot, result.RootPath) {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("Swift receipt root escapes product root: %q", result.RootPath)
	}
	if !runtimeServerWindowsSwiftPathWithinRoot(result.RootPath, result.ServerPath) {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("Swift receipt server escapes cohort root: %q", result.ServerPath)
	}
	if !runtimeServerWindowsSwiftSamePath(result.ServerPath, serverBinary) {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("Swift receipt server identity differs from requested binary")
	}
	if strings.TrimSpace(result.RootPath) == "" || strings.TrimSpace(result.ServerPath) == "" {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("Swift receipt has empty root or server path")
	}
	if _, err := shortPath(productRoot, result.RootPath); err != nil {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate Swift cohort root identity: %w", err)
	}
	if _, err := shortPath(productRoot, result.ServerPath); err != nil {
		return installer.WindowsRuntimeDependencyProvisionResult{}, fmt.Errorf("validate Swift server identity: %w", err)
	}
	return result, nil
}

func runtimeServerWindowsSwiftShortenArgs(args []string, productRoot string, shortPath runtimeServerWindowsSwiftShortPathResolver) ([]string, error) {
	result := append([]string(nil), args...)
	for index, arg := range result {
		if !filepath.IsAbs(arg) {
			continue
		}
		if !runtimeServerWindowsSwiftPathWithinRoot(productRoot, arg) {
			return nil, fmt.Errorf("Swift launch argument path escapes product root: %q", arg)
		}
		short, err := shortPath(productRoot, arg)
		if err != nil {
			return nil, fmt.Errorf("resolve short Swift launch argument: %w", err)
		}
		result[index] = short
	}
	return result, nil
}

func runtimeServerWindowsSwiftHasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, flag) {
			return true
		}
	}
	return false
}

func runtimeServerWindowsSwiftHasSDKArgument(args []string, sdk string) bool {
	return runtimeServerWindowsSwiftHasTriplet(args, "-sdk", sdk)
}

func runtimeServerWindowsSwiftHasResourceArgument(args []string, resource string) bool {
	return runtimeServerWindowsSwiftHasTriplet(args, "-resource-dir", resource)
}

func runtimeServerWindowsSwiftHasTriplet(args []string, flag, value string) bool {
	for index := 0; index+3 < len(args); index++ {
		if !strings.EqualFold(args[index], "-Xswiftc") || !strings.EqualFold(args[index+1], flag) || !strings.EqualFold(args[index+2], "-Xswiftc") {
			continue
		}
		if runtimeServerWindowsSwiftSamePath(args[index+3], value) {
			return true
		}
	}
	return false
}

func runtimeServerWindowsSwiftSamePath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func runtimeServerWindowsSwiftPathWithinRoot(root, candidate string) bool {
	root = filepath.Clean(strings.TrimSpace(root))
	candidate = filepath.Clean(strings.TrimSpace(candidate))
	if root == "." || candidate == "." || !filepath.IsAbs(root) || !filepath.IsAbs(candidate) {
		return false
	}
	if runtimeServerWindowsSwiftSamePath(root, candidate) {
		return true
	}
	prefix := strings.TrimRight(root, string(filepath.Separator)) + string(filepath.Separator)
	return strings.HasPrefix(strings.ToLower(candidate), strings.ToLower(prefix))
}
