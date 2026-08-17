//go:build !windows

package pidregistry

import (
	"os"
	"syscall"
)

func registryFileOwnedByCurrentUser(_ string, info os.FileInfo) bool {
	if info.Mode().Perm()&0o022 != 0 {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(stat.Uid) == os.Getuid()
}

