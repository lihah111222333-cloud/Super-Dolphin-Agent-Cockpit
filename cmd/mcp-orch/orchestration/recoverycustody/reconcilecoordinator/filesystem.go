package reconcilecoordinator

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// validatePrivateFilesystem 要求 coordinator 自有 SQLite 的父目录和所有已存在文件
// 均为当前用户专用；数据库及 WAL/SHM 只接受普通非符号链接文件。
// allowMissingDatabase 仅供 Bootstrap 使用，允许 SQLite 创建缺失的主文件及父目录。
func validatePrivateFilesystem(path string, allowMissingDatabase bool) error {
	if err := validateCoordinatorParent(filepath.Dir(path), allowMissingDatabase); err != nil {
		return err
	}
	return validateCoordinatorArtifacts(path, allowMissingDatabase)
}

func validateCoordinatorParent(parent string, allowMissingDatabase bool) error {
	parentInfo, err := os.Lstat(parent)
	if errors.Is(err, os.ErrNotExist) {
		if allowMissingDatabase {
			return nil
		}
		return fmt.Errorf("%w: coordinator parent directory is missing", ErrSchemaMismatch)
	}
	if err != nil {
		return fmt.Errorf("%w: inspect coordinator parent directory", ErrSchemaMismatch)
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return fmt.Errorf("%w: coordinator parent directory must be a private directory", ErrSchemaMismatch)
	}
	if err := securefs.CheckPrivateOwnerOnly(parent, parentInfo); err != nil {
		return fmt.Errorf("%w: coordinator parent directory is not private", ErrSchemaMismatch)
	}
	return nil
}

func validateCoordinatorArtifacts(path string, allowMissingDatabase bool) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		info, statErr := os.Lstat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			if candidate == path && !allowMissingDatabase {
				return fmt.Errorf("%w: coordinator database was not bootstrapped", ErrSchemaMismatch)
			}
			continue
		}
		if statErr != nil {
			return fmt.Errorf("%w: inspect coordinator SQLite path", ErrSchemaMismatch)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: coordinator SQLite path must be a regular non-symlink file", ErrSchemaMismatch)
		}
		if err := securefs.CheckPrivateOwnerOnly(candidate, info); err != nil {
			return fmt.Errorf("%w: coordinator SQLite path is not private", ErrSchemaMismatch)
		}
	}
	return nil
}
