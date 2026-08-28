//go:build windows

package main

// trustedLauncherContractShells 仅验证 Windows Git Bash 执行面。
func trustedLauncherContractShells() []string {
	return []string{"bash"}
}

// trustedLauncherContractOSHomeResolver 保留 Git Bash 提供的账户查询夹具。
func trustedLauncherContractOSHomeResolver(home string) (string, string) {
	return "getent", "#!/bin/sh\nprintf 'fixture:x:0:0::" + home + ":/bin/sh\\n'\n"
}
