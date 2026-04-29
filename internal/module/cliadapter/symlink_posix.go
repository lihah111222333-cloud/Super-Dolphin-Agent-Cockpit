//go:build !windows

package cliadapter

import (
	"fmt"
	"os"
)

// platformLink 在 POSIX 上用 os.Symlink。
func platformLink(target, source string) error {
	if err := os.Symlink(source, target); err != nil {
		return fmt.Errorf("cliadapter: symlink: %w", err)
	}
	return nil
}
