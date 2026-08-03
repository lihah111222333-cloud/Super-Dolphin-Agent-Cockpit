//go:build aix || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package remoteci

import (
	"errors"
	"os"
	"syscall"
)

func sourceSnapshotFileHasSingleLink(_ string, info os.FileInfo) (bool, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, errors.New("source snapshot file does not expose link count")
	}
	return stat.Nlink == 1, nil
}
