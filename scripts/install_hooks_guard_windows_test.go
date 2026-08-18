//go:build windows

package main

// trustedLauncherContractShells 仅验证 Windows Git Bash 执行面。
func trustedLauncherContractShells() []string {
	return []string{"bash"}
}
