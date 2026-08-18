//go:build darwin

package main

// trustedLauncherContractShells 覆盖 macOS 实际提供的 bash 与 zsh。
func trustedLauncherContractShells() []string {
	return []string{"bash", "zsh"}
}
