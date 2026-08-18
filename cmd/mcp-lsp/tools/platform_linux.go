//go:build linux

package tools

// normalizePlatformWorkDir 在 Linux 上把 Windows 主机路径转换为 WSL 挂载路径。
func normalizePlatformWorkDir(workDir string) string {
	return normalizeWSLWorkDir(workDir)
}
