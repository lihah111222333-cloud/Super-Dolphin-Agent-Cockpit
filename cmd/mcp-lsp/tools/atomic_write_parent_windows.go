//go:build windows

package tools

// syncParentDirectory 避免在 Windows 上对目录句柄调用不受支持的 os.File.Sync。
func syncParentDirectory(string, fileWriter) error {
	return nil
}
