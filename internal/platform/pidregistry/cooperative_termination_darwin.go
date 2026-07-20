//go:build darwin

package pidregistry

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func renameCooperativeEndpointNoReplace(source, target string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_EXCL)
}

func cooperativePeerPID(fd int) (int, error) {
	return unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
}

func cooperativeEndpointCreationTime(_ string, stat *syscall.Stat_t) (int64, int64, error) {
	return stat.Birthtimespec.Sec, stat.Birthtimespec.Nsec, nil
}
