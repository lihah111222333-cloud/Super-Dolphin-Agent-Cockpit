//go:build darwin

package main

// trustedLauncherContractShells 覆盖 macOS 实际提供的 bash 与 zsh。
func trustedLauncherContractShells() []string {
	return []string{"bash", "zsh"}
}

// trustedLauncherContractOSHomeResolver 返回 macOS 系统账户 home 查询夹具。
func trustedLauncherContractOSHomeResolver(home string) (string, string) {
	return "dscl", "#!/bin/sh\nprintf 'NFSHomeDirectory: " + home + "\\n'\n"
}
