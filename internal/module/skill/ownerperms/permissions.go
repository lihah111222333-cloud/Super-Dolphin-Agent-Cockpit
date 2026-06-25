// Package ownerperms 提供技能文件权限校验与加固工具，确保敏感文件仅对当前用户可读写。
package ownerperms

import (
	"errors"
	"os"
)

// ValidateOwnerOnlyFilePath 检查 path 对应文件的权限是否符合仅 owner 可读写要求。
// 文件不存在时视为合法，直接返回 nil。
func ValidateOwnerOnlyFilePath(path, label string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return ValidateOwnerOnlyFilePermissions(path, info, label)
}
