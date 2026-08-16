//go:build !linux

package tools

// normalizePlatformWorkDir 保持非 Linux 平台的原生路径语义。
func normalizePlatformWorkDir(workDir string) string {
	return workDir
}
