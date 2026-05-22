package ownerperms

import (
	"errors"
	"os"
)

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
