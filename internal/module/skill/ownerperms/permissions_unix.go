//go:build !windows

// Package ownerperms 的 Unix 实现，通过检查文件 mode 位确保权限为 0600。
package ownerperms

import (
	"fmt"
	"os"
)

// ValidateOwnerIdentitySaltPermissions 校验 owner identity salt 文件不为空且权限为 0600。
func ValidateOwnerIdentitySaltPermissions(_ string, info os.FileInfo) error {
	if info.Size() == 0 {
		return fmt.Errorf("owner identity salt is empty")
	}
	return ValidateOwnerOnlyFilePermissions("", info, "owner identity salt")
}

// SecureOwnerIdentitySaltPermissions 将 owner identity salt 文件权限加固为 0600。
func SecureOwnerIdentitySaltPermissions(path string) error {
	return SecureOwnerOnlyFilePermissions(path)
}

// ValidateOwnerOnlyFilePermissions 校验文件为普通文件且权限为 0600，label 用于错误信息。
func ValidateOwnerOnlyFilePermissions(_ string, info os.FileInfo, label string) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", label)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%s permissions must be 0600, got %04o", label, info.Mode().Perm())
	}
	return nil
}

// SecureOwnerOnlyFilePermissions 将文件权限设置为 0600（仅 owner 可读写）。
func SecureOwnerOnlyFilePermissions(path string) error {
	return os.Chmod(path, 0o600)
}
