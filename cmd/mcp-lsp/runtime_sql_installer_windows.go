//go:build windows

package main

import (
	"context"
	"fmt"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

const runtimeWindowsSQLSInstallLockKey = "windows-runtime-dependency-go-sqls"

// registerWindowsSQLInstaller 注册仅由 Windows 产品私有缓存提供的 SQL 语言服务安装器。
func registerWindowsSQLInstaller(inst *installer.Provider, productRoot string, productRootErr error) {
	inst.Register("sql", windowsSQLInstallerConfig(productRoot, productRootErr))
}

// windowsSQLInstallerConfig 构造 Windows 产品自有 GoSQLS 的安装、只读解析和就绪校验契约。
func windowsSQLInstallerConfig(productRoot string, productRootErr error) installer.InstallerConfig {
	cfg := installer.InstallerConfig{
		BinaryName:                  installer.WindowsGoSQLSBinaryName,
		AllowInstallCommand:         true,
		InstallTimeout:              windowsProductionInstallTimeout,
		InstallLockKey:              runtimeWindowsSQLSInstallLockKey,
		InstallAction:               windowsSQLSInstallAction(productRoot, productRootErr),
		InstalledBinaryPathResolver: windowsSQLSBinaryPathResolver(productRoot, productRootErr),
		InstalledReadinessValidator: windowsSQLSReadinessValidator(productRoot, productRootErr),
	}
	if productRootErr != nil {
		cfg.UnsupportedPlatform = productRootErr
		return cfg
	}
	if _, err := installer.DetectWindowsHostPlatform(); err != nil {
		cfg.UnsupportedPlatform = err
	}
	return cfg
}

// windowsSQLSInstallAction 在受控 InstallAction 生命周期内准备 VCLibs 和产品自有 GoSQLS。
func windowsSQLSInstallAction(productRoot string, productRootErr error) installer.InstallAction {
	return func(ctx context.Context) (installer.InstallResult, error) {
		if productRootErr != nil {
			return installer.InstallResult{}, productRootErr
		}
		result, err := installer.ProvisionWindowsRuntimeDependency(ctx, installer.WindowsRuntimeDependencyProductGoSQLS, windowsRuntimeDependencyCacheRoot(productRoot))
		if err != nil {
			return installer.InstallResult{}, fmt.Errorf("install product-owned GoSQLS: %w", err)
		}
		if result.ServerPath == "" {
			return installer.InstallResult{}, fmt.Errorf("product-owned GoSQLS returned an empty server path")
		}
		return installer.InstallResult{Path: result.ServerPath}, nil
	}
}

// windowsSqruffInstallAction 保留 E2E 测试 seam 名称；Windows SQL 产品已统一走 GoSQLS。
func windowsSqruffInstallAction(productRoot string, productRootErr error) installer.InstallAction {
	return windowsSQLSInstallAction(productRoot, productRootErr)
}

// windowsSQLSBinaryPathResolver 只读解析产品私有 GoSQLS，不查询宿主 PATH。
func windowsSQLSBinaryPathResolver(productRoot string, productRootErr error) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return "", err
		}
		if productRootErr != nil {
			return "", productRootErr
		}
		result, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductGoSQLS, windowsRuntimeDependencyCacheRoot(productRoot))
		if err != nil {
			return "", err
		}
		return result.ServerPath, nil
	}
}

// windowsSQLSReadinessValidator 只读复验产品私有 GoSQLS 已经可用。
func windowsSQLSReadinessValidator(productRoot string, productRootErr error) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := installer.WindowsNodeRuntimeResolverContextCheck(ctx); err != nil {
			return err
		}
		if productRootErr != nil {
			return productRootErr
		}
		_, err := installer.ResolveWindowsRuntimeDependency(installer.WindowsRuntimeDependencyProductGoSQLS, windowsRuntimeDependencyCacheRoot(productRoot))
		return err
	}
}
