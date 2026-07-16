//go:build unix

package localci

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

func currentSchedulerOwnerUID() (int, error) {
	return os.Geteuid(), nil
}

func schedulerFileOwnerUID(info os.FileInfo) (int, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, errors.New("scheduler path owner is unavailable")
	}
	return int(stat.Uid), nil
}

// openSchedulerFileNoFollow 使用 O_NOFOLLOW/O_EXCL 阻断链接和创建竞争。
func openSchedulerFileNoFollow(targetPath string, ownerUID int, exists bool) (*os.File, bool, error) {
	flags := unix.O_RDWR | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if !exists {
		flags |= unix.O_CREAT | unix.O_EXCL
	}
	fd, err := unix.Open(targetPath, flags, privateSchedulerFileMode)
	if errors.Is(err, unix.EEXIST) && !exists {
		exists, err = validateCurrentUIDPrivatePath(targetPath, ownerUID)
		if err != nil {
			return nil, false, err
		}
		if !exists {
			return nil, false, errors.New("scheduler file appeared without a valid owner")
		}
		fd, err = unix.Open(targetPath, unix.O_RDWR|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return nil, false, err
	}
	file := os.NewFile(uintptr(fd), targetPath)
	if file == nil {
		return nil, false, errors.Join(errors.New("create scheduler file handle"), unix.Close(fd))
	}
	return file, !exists, nil
}
