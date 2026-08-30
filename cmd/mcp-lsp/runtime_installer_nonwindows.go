//go:build !windows

package main

import (
	"runtime"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"
)

// runtimeNPMCommand 返回非 Windows 构建的历史 npm 启动文件名；平台选择由本文件的 build tag 固定。
func runtimeNPMCommand() string {
	return runtimeNPMCommandForPlatform("nonwindows")
}

// runtimeNPMExecutableName 返回非 Windows 构建的历史 npm bin shim 文件名；不读取 runtime.GOOS。
func runtimeNPMExecutableName(binaryName string) string {
	return runtimeNPMExecutableNameForPlatform("nonwindows", binaryName)
}

// registerNPMInstallers 注册非 Windows 的历史 PATH npm 安装器；Windows locked cohort 在 Windows companion 中选择。
func registerNPMInstallers(inst *installer.Provider) {
	registerInstallerSpecs(inst, runtimeNPMInstallerSpecsForPlatform("nonwindows"))
}

// registerShellAndSQLInstallers 保留非 Windows 的 PATH shell/npm 与 SQL 注册行为。
func registerShellAndSQLInstallers(inst *installer.Provider) {
	inst.Register("shellscript", runtimeNonWindowsShellInstallerConfig())
	// Linux amd64 already has the self-contained, checksum-pinned sqruff
	// artifact registered by registerPlatformNativeArtifactInstallers. Do not
	// replace it with the legacy cargo PATH installer.
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		registerSQLInstaller(inst)
	}
}

// runtimeShellNPMInstallerConfigForPlatform 保留非 Windows 测试的显式平台映射入口；参数只为矩阵测试保留。
func runtimeShellNPMInstallerConfigForPlatform(_ string) installer.InstallerConfig {
	return runtimeNonWindowsShellInstallerConfig()
}

// registerPlatformProductionInstallers 保留 Linux/macOS 的历史 PATH、brew、go、
// cargo 和 npm 注册及其原有 timeout/lifecycle；Windows catalog/runtime bridge
// 不会在非 Windows 编译或调用路径中出现。
func registerPlatformProductionInstallers(inst *installer.Provider) {
	registerNPMInstallers(inst)
	// Linux amd64 uses the managed official-archive installers registered by
	// registerPlatformNativeArtifactInstallers. The legacy brew/rustup map has
	// overlapping language IDs and would silently overwrite those installers.
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		registerNativeToolInstallers(inst)
	}
	registerGoInstallers(inst)
	registerShellAndSQLInstallers(inst)
}
