//go:build !linux && !darwin && !windows

package main

// trustedLauncherContractShells 为未登记平台保留最小 bash 契约。
func trustedLauncherContractShells() []string {
	return []string{"bash"}
}

// trustedLauncherContractOSHomeResolver 为未登记平台保留 getent 账户查询夹具。
func trustedLauncherContractOSHomeResolver(home string) (string, string) {
	return "getent", "#!/bin/sh\nprintf 'fixture:x:0:0::" + home + ":/bin/sh\\n'\n"
}
