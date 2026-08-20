//go:build !windows

package pidregistry

import (
	"os"
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

func registryFileOwnedByCurrentUser(path string, info os.FileInfo) bool {
	if err := securefs.CheckPrivateOwnerOnly(path, info); err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return int(stat.Uid) == os.Getuid()
}
