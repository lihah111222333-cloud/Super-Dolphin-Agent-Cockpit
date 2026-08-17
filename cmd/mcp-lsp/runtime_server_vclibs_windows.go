//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
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
func runtimeServerPlatformDependencyEnvironment(serverBinary string, env []string) ([]string, error) {
	result, err := runtimeServerWindowsVCLibsEnvironmentWithResolver(serverBinary, env, resolveRuntimeWindowsVCLibsProcessPath)
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
	return runtimeServerWindowsRustEnvironment(serverBinary, result)
}

// runtimeServerWindowsRustEnvironment 仅为产品根内锁定的 rust-analyzer 注入同 cohort 的 rustfmt/cargo-fmt bin；外部 binary 原样保留。
func runtimeServerWindowsRustEnvironment(serverBinary string, env []string) ([]string, error) {
	productRoot, owned, err := runtimeServerWindowsOwnedProductRoot(serverBinary)
	if err != nil || !owned || !strings.EqualFold(filepath.Base(filepath.Clean(serverBinary)), "rust-analyzer.exe") {
		return append([]string(nil), env...), err
	}
	rustfmtPath, err := installer.ResolveWindowsRustfmtPath(productRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve product-owned Rustfmt for rust-analyzer: %w", err)
	}
	rustfmtDir := installer.WindowsRustfmtBinDir(rustfmtPath)
	pathValue := runtimeServerWindowsEnvironmentValue(env, "PATH")
	if pathValue == "" {
		pathValue = os.Getenv("PATH")
	}
	if pathValue != "" {
		rustfmtDir += string(os.PathListSeparator) + pathValue
	}
	return replaceRuntimeServerWindowsEnvironment(env, "PATH", rustfmtDir), nil
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
// 同一文件身份的 8.3 路径；缓存、资源 cohort 和产品根推导仍使用完整 SHA 路径。
func runtimeServerPlatformProcessBinary(serverBinary string) (string, error) {
	if kotlinBinary, handled, err := runtimeServerWindowsKotlinProcessBinary(serverBinary); err != nil {
		return "", err
	} else if handled {
		return kotlinBinary, nil
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
