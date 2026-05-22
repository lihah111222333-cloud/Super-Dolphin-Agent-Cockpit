//go:build !windows

package ownerperms

import (
	"fmt"
	"os"
)

func ValidateOwnerIdentitySaltPermissions(_ string, info os.FileInfo) error {
	if info.Size() == 0 {
		return fmt.Errorf("owner identity salt is empty")
	}
	return ValidateOwnerOnlyFilePermissions("", info, "owner identity salt")
}

func SecureOwnerIdentitySaltPermissions(path string) error {
	return SecureOwnerOnlyFilePermissions(path)
}

func ValidateOwnerOnlyFilePermissions(_ string, info os.FileInfo, label string) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", label)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s permissions must be 0600, got %04o", label, info.Mode().Perm())
	}
	return nil
}

func SecureOwnerOnlyFilePermissions(path string) error {
	return os.Chmod(path, 0o600)
}
