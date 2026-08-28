//go:build !windows && !linux

package tools

// normalizePlatformWorkDir 在非 Windows、非 Linux 平台保持主机路径原值。
func normalizePlatformWorkDir(workDir string) string {
	return workDir
}
