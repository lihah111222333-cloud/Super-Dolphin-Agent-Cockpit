//go:build linux

package pidregistry

import (
	"errors"
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

func renameCooperativeEndpointNoReplace(source, target string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
}

func cooperativePeerPID(fd int) (int, error) {
	credentials, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	if credentials == nil || credentials.Pid <= 1 {
		return 0, errors.New("pidregistry: cooperative peer PID is invalid")
	}
	return int(credentials.Pid), nil
}

// cooperativeEndpointCreationTime 读取 statx birth time 并与 Lstat 身份交叉校验。
func cooperativeEndpointCreationTime(endpoint string, stat *syscall.Stat_t) (int64, int64, error) {
	var statx unix.Statx_t
	if err := unix.Statx(
		unix.AT_FDCWD, endpoint, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BASIC_STATS|unix.STATX_BTIME, &statx,
	); err != nil {
		return 0, 0, fmt.Errorf("pidregistry: read cooperative termination endpoint birth time: %w", err)
	}
	requiredMask := uint32(unix.STATX_BTIME | unix.STATX_INO | unix.STATX_MODE | unix.STATX_UID)
	if statx.Mask&requiredMask != requiredMask {
		return 0, 0, errors.New("pidregistry: cooperative termination endpoint statx identity is incomplete")
	}
	if statx.Ino != uint64(stat.Ino) || unix.Mkdev(statx.Dev_major, statx.Dev_minor) != uint64(stat.Dev) ||
		statx.Uid != stat.Uid || uint32(statx.Mode) != uint32(stat.Mode) {
		return 0, 0, ErrCooperativeEndpointIdentityMismatch
	}
	return statx.Btime.Sec, int64(statx.Btime.Nsec), nil
}
