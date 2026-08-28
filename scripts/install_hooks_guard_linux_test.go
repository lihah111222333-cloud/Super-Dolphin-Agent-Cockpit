//go:build linux

package main

// trustedLauncherContractShells 仅返回 Linux 发布镜像实际提供的 shell。
func trustedLauncherContractShells() []string {
	return []string{"bash"}
}

// trustedLauncherContractOSHomeResolver 返回 Linux 系统账户 home 查询夹具。
func trustedLauncherContractOSHomeResolver(home string) (string, string) {
	return "getent", "#!/bin/sh\nprintf 'fixture:x:0:0::" + home + ":/bin/sh\\n'\n"
}
