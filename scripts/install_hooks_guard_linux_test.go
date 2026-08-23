//go:build linux

package main

// trustedLauncherContractShells 仅返回 Linux 发布镜像实际提供的 shell。
func trustedLauncherContractShells() []string {
	return []string{"bash"}
}
