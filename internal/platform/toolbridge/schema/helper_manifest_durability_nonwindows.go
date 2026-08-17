//go:build !windows

package schema

import (
	"errors"
	"fmt"
	"os"
)

// wrapSchemaFilesystemError 在非 Windows 上保持原始错误链，避免引入 Windows ACL
// 语义；共享 helper 只负责调用该平台 seam，不选择系统行为。
func wrapSchemaFilesystemError(_ string, err error) error {
	return err
}

// syncFilesystemSnapshotDirectory 保留非 Windows 的目录 fsync 实现；共享写入协议不依赖平台 API。
func syncFilesystemSnapshotDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open schema snapshot directory for fsync: %w", err)
	}
	if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
		return fmt.Errorf("fsync schema snapshot directory: %w", err)
	}
	return nil
}
