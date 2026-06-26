//go:build !windows

package securefs

import (
	"fmt"
	"os"
)

// CheckExistingOwnerOnly 拒绝 group/world 可写路径，避免 SQLite 等本地状态被其他用户改写。
func CheckExistingOwnerOnly(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("path is group/world-writable: %s", RedactPath(path))
	}
	return nil
}

// RestrictOwnerOnly 设置目标权限后重新检查 owner-only 约束。
func RestrictOwnerOnly(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("restrict path permissions %s: %s", RedactPath(path), SafeError(err))
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect restricted path %s: %s", RedactPath(path), SafeError(err))
	}
	return CheckExistingOwnerOnly(path, info)
}
