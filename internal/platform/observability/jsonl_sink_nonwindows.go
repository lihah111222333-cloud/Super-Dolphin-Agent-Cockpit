//go:build !windows

package observability

import (
	"fmt"
	"os"
)

// chmodOwnerOnly 在非 Windows 平台保留既有 chmod 行为，失败时继续 fail-fast。
func chmodOwnerOnly(path string, perm os.FileMode) error {
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("set owner-only permissions on %s: %w", path, err)
	}
	return nil
}
