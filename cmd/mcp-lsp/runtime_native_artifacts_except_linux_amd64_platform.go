//go:build !linux || !amd64

package main

import "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/installer"

// registerPlatformNativeArtifactInstallers 保留非 Linux 平台既有的系统包管理器安装行为。
func registerPlatformNativeArtifactInstallers(inst *installer.Provider) error {
	registerInstallerSpecs(inst, []runtimeInstallerSpec{
		{
			languages:  []string{"proto"},
			binaryName: "buf",
			installCmd: "brew",
			args:       []string{"install", "buf"},
		},
		{
			languages:  []string{"lua"},
			binaryName: "lua-language-server",
			installCmd: "brew",
			args:       []string{"install", "lua-language-server"},
		},
		{
			languages:  []string{"terraform"},
			binaryName: "terraform-ls",
			installCmd: "brew",
			args:       []string{"install", "hashicorp/tap/terraform-ls"},
		},
	})
	inst.Register("sql", installer.InstallerConfig{
		BinaryName:          "sqruff",
		BinaryCheckArgs:     []string{"--version"},
		InstallCmd:          "cargo",
		InstallArgs:         []string{"install", "sqruff", "--version", sqruffInstallVersion, "--locked"},
		AllowInstallCommand: true,
	})
	return nil
}
