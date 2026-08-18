//go:build !linux && !darwin && !windows

package main

// trustedLauncherContractShells 为未登记平台保留最小 bash 契约。
func trustedLauncherContractShells() []string {
	return []string{"bash"}
}
