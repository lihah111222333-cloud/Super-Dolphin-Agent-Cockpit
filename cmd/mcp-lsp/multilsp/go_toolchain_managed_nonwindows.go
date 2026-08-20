//go:build !windows

package multilsp

// managedGoToolchainCandidates 在非 Windows 平台保持 PATH-only 选择，避免改变既有平台的安装边界。
func managedGoToolchainCandidates(_ []string) []goToolchainCandidate {
	return nil
}
