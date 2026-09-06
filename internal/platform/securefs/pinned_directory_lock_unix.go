//go:build darwin || linux

package securefs

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

type pinnedDirectoryPlatformLock struct {
	directoryFD int
}

// ensurePinnedDirectoryPlatformSupported 确认当前 Unix 平台提供目录 inode flock。
func ensurePinnedDirectoryPlatformSupported() error {
	return nil
}

// acquirePinnedDirectoryPlatform 在 Unix 上锁定目录 inode，再打开固定哨兵句柄。
func acquirePinnedDirectoryPlatform(
	rootPath string,
	root *os.Root,
	directory *os.File,
	sentinelName string,
) (pinnedDirectoryPlatformLock, *os.File, error) {
	if err := syscall.Flock(int(directory.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return pinnedDirectoryPlatformLock{}, nil, fmt.Errorf(
				"acquire pinned directory lock %s: %w",
				RedactPath(rootPath),
				ErrLockBusy,
			)
		}
		return pinnedDirectoryPlatformLock{}, nil, fmt.Errorf(
			"lock pinned directory %s: %s",
			RedactPath(rootPath),
			SafeError(err),
		)
	}
	sentinel, err := openPinnedSentinel(root, sentinelName)
	if err != nil {
		_ = syscall.Flock(int(directory.Fd()), syscall.LOCK_UN)
		return pinnedDirectoryPlatformLock{}, nil, err
	}
	return pinnedDirectoryPlatformLock{directoryFD: int(directory.Fd())}, sentinel, nil
}

// closePinnedDirectoryPlatform 在关闭目录句柄前显式释放 flock，并关闭哨兵句柄。
func closePinnedDirectoryPlatform(platform pinnedDirectoryPlatformLock, sentinel *os.File) error {
	unlockErr := syscall.Flock(platform.directoryFD, syscall.LOCK_UN)
	closeErr := sentinel.Close()
	return errors.Join(unlockErr, closeErr)
}

// openPinnedSentinel 在 os.Root 内创建或打开严格私有的普通哨兵文件。
func openPinnedSentinel(root *os.Root, sentinelName string) (*os.File, error) {
	_, err := root.Lstat(sentinelName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect pinned sentinel %s: %s", RedactPath(sentinelName), SafeError(err))
	}
	flags := os.O_RDWR
	if errors.Is(err, os.ErrNotExist) {
		flags |= os.O_CREATE | os.O_EXCL
	}
	sentinel, err := root.OpenFile(sentinelName, flags, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open pinned sentinel %s: %s", RedactPath(sentinelName), SafeError(err))
	}
	if err := validateOpenedPinnedSentinel(root, sentinelName, sentinel); err != nil {
		_ = sentinel.Close()
		return nil, err
	}
	return sentinel, nil
}

// validateOpenedPinnedSentinel 拒绝 symlink、非普通文件和打开期间发生的名称替换。
func validateOpenedPinnedSentinel(root *os.Root, sentinelName string, sentinel *os.File) error {
	entryInfo, err := root.Lstat(sentinelName)
	if err != nil {
		return fmt.Errorf("inspect opened pinned sentinel %s: %s", RedactPath(sentinelName), SafeError(err))
	}
	handleInfo, err := sentinel.Stat()
	if err != nil {
		return fmt.Errorf("inspect pinned sentinel handle %s: %s", RedactPath(sentinelName), SafeError(err))
	}
	if !entryInfo.Mode().IsRegular() || !handleInfo.Mode().IsRegular() || !os.SameFile(entryInfo, handleInfo) {
		return fmt.Errorf("pinned sentinel is not a stable regular file: %s", RedactPath(sentinelName))
	}
	if err := CheckPrivateOwnerOnly(sentinelName, entryInfo); err != nil {
		return fmt.Errorf("pinned sentinel is not private: %w", err)
	}
	return nil
}
