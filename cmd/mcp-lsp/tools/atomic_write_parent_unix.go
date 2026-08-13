//go:build !windows

package tools

import "fmt"

// syncParentDirectory 在支持目录 fsync 的平台持久化 rename 后的目录项。
func syncParentDirectory(dir string, writer fileWriter) error {
	dirFile, err := writer.Open(dir)
	if err != nil {
		return fmt.Errorf("open parent directory %s: %w", dir, err)
	}
	if err := dirFile.Sync(); err != nil {
		_ = dirFile.Close()
		return fmt.Errorf("sync parent directory %s: %w", dir, err)
	}
	if err := dirFile.Close(); err != nil {
		return fmt.Errorf("close parent directory %s: %w", dir, err)
	}
	return nil
}
