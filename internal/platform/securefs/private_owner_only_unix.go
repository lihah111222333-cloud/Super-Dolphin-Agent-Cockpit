//go:build !windows

package securefs

import (
	"fmt"
	"os"
)

// CheckPrivateOwnerOnly 校验路径没有向 group 或 world 暴露任何权限。
func CheckPrivateOwnerOnly(path string, info os.FileInfo) error {
	if info == nil {
		var err error
		info, err = os.Lstat(path)
		if err != nil {
			return fmt.Errorf("inspect private path %s: %s", RedactPath(path), SafeError(err))
		}
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf(
			"private path grants group/world access: %s mode=%#o",
			RedactPath(path),
			info.Mode().Perm(),
		)
	}
	return nil
}

// RestrictPrivateOwnerOnly 设置严格 owner-only 权限并重新校验。
func RestrictPrivateOwnerOnly(path string, mode os.FileMode) error {
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("restrict private path %s: %s", RedactPath(path), SafeError(err))
	}
	return CheckPrivateOwnerOnly(path, nil)
}
