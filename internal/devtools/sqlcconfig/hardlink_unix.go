//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package sqlcconfig

import (
	"errors"
	"os"
	"syscall"
)

func hardLinkCount(_ string, info os.FileInfo) (uint64, error) {
	if info == nil {
		return 0, errors.New("output link metadata is missing")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("output link metadata has an unknown platform type")
	}
	if stat.Nlink == 0 {
		return 0, errors.New("output link count is unavailable")
	}
	return uint64(stat.Nlink), nil
}
