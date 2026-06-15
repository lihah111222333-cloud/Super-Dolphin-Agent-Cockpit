package ownerperms

import (
	"errors"
	"os"
)

// ValidateOwnerOnlyFilePath 校验owneronly文件路径。
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
