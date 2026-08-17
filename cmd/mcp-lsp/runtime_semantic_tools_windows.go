//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/multilsp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

const windowsProductionDependencyProfile = "production"

// runtimePlatformSemanticLSPToolsAvailable 只在 Windows production profile 中检查
// 产品自动安装器。它只读安装配置，不触发下载或写盘；NativeArch、Windows build
// 和 unsupported 判定仍由 Windows tagged installer 注册层负责，禁止跨架构回退。
func runtimePlatformSemanticLSPToolsAvailable(context.Context) (bool, error) {
	profile := strings.TrimSpace(os.Getenv("SUPER_DOLPHIN_DEPENDENCY_PROFILE"))
	if profile != windowsProductionDependencyProfile {
		pkglogger.Get().Debug("mcp-lsp semantic tools unavailable from Windows production auto-installer",
			"dependency_profile", profile,
			"reason", "production_profile_required",
		)
		return false, nil
	}

	// Windows production 的 semantic tools/list 必须先验证产品根；安装器可能把根目录
	// 错误延迟到 InstallAction，若此处继续暴露工具会把不可安装配置误报为语义能力。
	// 该预检只读取解析结果，不触发安装或写盘；非 Windows 仍由对应 no-op 实现处理。
	if _, err := runtimeenv.ResolveWindowsLSPProductRoot(); err != nil {
		return false, fmt.Errorf("resolve Windows production LSP product root: %w", err)
	}

	inst, err := setupInstallerWithError()
	if err != nil {
		return false, err
	}
	adapters := multilsp.NewDefaultLanguageAdapterRegistry()
	for _, languageID := range runtimePrimaryLanguageIDs() {
		adapter, ok := adapters.AdapterForLanguage(languageID)
		if !ok {
			return false, errors.New("missing LSP language adapter: " + languageID)
		}
		if !adapter.CapabilityPolicy().RequiresLSPClient {
			continue
		}
		cfg, ok := inst.ConfigForLanguage(languageID)
		if !ok || cfg.UnsupportedPlatform != nil || !windowsSemanticInstallerCanProvision(cfg) {
			continue
		}
		pkglogger.Get().Info("mcp-lsp semantic tool visibility enabled by Windows production auto-installer",
			"source", "windows_production_auto_installer",
			"language_id", languageID,
			"binary_name", cfg.BinaryName,
		)
		return true, nil
	}

	pkglogger.Get().Warn("mcp-lsp semantic tools unavailable from Windows production auto-installer",
		"reason", "no_supported_semantic_installer",
	)
	return false, errors.New("Windows production profile has no supported semantic LSP auto-installer")
}

// windowsSemanticInstallerCanProvision 判断 Windows 配置是否声明了真实的按需安装
// 动作；仅有 resolver 或二进制名称不能让 tools/list 虚假暴露语义工具。
func windowsSemanticInstallerCanProvision(cfg installer.InstallerConfig) bool {
	if !cfg.AllowInstallCommand {
		return false
	}
	return cfg.InstallAction != nil || cfg.ManagedInstall != nil ||
		cfg.InstallCommandResolver != nil || strings.TrimSpace(cfg.InstallCmd) != ""
}
