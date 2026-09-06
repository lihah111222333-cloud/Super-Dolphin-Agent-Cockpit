//go:build windows

package securefs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

type pinnedDirectoryPlatformLock struct {
	overlapped windows.Overlapped
}

type pinnedDirectoryWindowsOps struct {
	lock   func(windows.Handle, *windows.Overlapped) error
	unlock func(windows.Handle, *windows.Overlapped) error
}

const windowsFileFlagOpenReparsePoint uint32 = 0x00200000

// ensurePinnedDirectoryPlatformSupported 确认当前 Windows 平台提供 no-delete-share 与 LockFileEx。
func ensurePinnedDirectoryPlatformSupported() error {
	return nil
}

// acquirePinnedDirectoryPlatform 用禁止 delete-share 的句柄固定哨兵，再非阻塞锁定首字节。
func acquirePinnedDirectoryPlatform(
	rootPath string,
	_ *os.Root,
	_ *os.File,
	sentinelName string,
) (pinnedDirectoryPlatformLock, *os.File, error) {
	sentinelPath := filepath.Join(rootPath, sentinelName)
	handle, err := openPinnedWindowsSentinel(sentinelPath)
	if err != nil {
		if errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			return pinnedDirectoryPlatformLock{}, nil, fmt.Errorf("open pinned sentinel: %w", ErrLockBusy)
		}
		return pinnedDirectoryPlatformLock{}, nil, WrapErrorForPath(
			fmt.Errorf("open pinned sentinel %s: %w", RedactPath(sentinelName), err),
			sentinelPath,
		)
	}
	platform := pinnedDirectoryPlatformLock{}
	if err := lockPinnedWindowsSentinel(handle, &platform.overlapped, defaultPinnedDirectoryWindowsOps()); err != nil {
		_ = windows.CloseHandle(handle)
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return pinnedDirectoryPlatformLock{}, nil, fmt.Errorf("lock pinned sentinel: %w", ErrLockBusy)
		}
		return pinnedDirectoryPlatformLock{}, nil, fmt.Errorf("lock pinned sentinel: %w", err)
	}
	sentinel := os.NewFile(uintptr(handle), sentinelPath)
	if sentinel == nil {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, &platform.overlapped)
		_ = windows.CloseHandle(handle)
		return pinnedDirectoryPlatformLock{}, nil, errors.New("convert pinned sentinel handle")
	}
	return platform, sentinel, nil
}

// closePinnedDirectoryPlatform 先释放 LockFileEx，再关闭禁止删除共享的哨兵句柄。
func closePinnedDirectoryPlatform(platform pinnedDirectoryPlatformLock, sentinel *os.File) error {
	handle := windows.Handle(sentinel.Fd())
	unlockErr := unlockPinnedWindowsSentinel(handle, &platform.overlapped, defaultPinnedDirectoryWindowsOps())
	closeErr := sentinel.Close()
	return errors.Join(unlockErr, closeErr)
}

// openPinnedWindowsSentinel 以 read/write share 且无 delete-share 的方式打开或创建哨兵。
func openPinnedWindowsSentinel(path string) (windows.Handle, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("encode pinned sentinel path: %w", err)
	}
	return windows.CreateFile(
		pathPointer,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_NORMAL|windowsFileFlagOpenReparsePoint,
		0,
	)
}

// lockPinnedWindowsSentinel 通过可替换 ops 暴露 LockFileEx 的单元测试边界。
func lockPinnedWindowsSentinel(
	handle windows.Handle,
	overlapped *windows.Overlapped,
	ops pinnedDirectoryWindowsOps,
) error {
	if ops.lock == nil {
		return errors.New("pinned directory Windows lock operation is missing")
	}
	return ops.lock(handle, overlapped)
}

// unlockPinnedWindowsSentinel 通过可替换 ops 暴露 UnlockFileEx 的单元测试边界。
func unlockPinnedWindowsSentinel(
	handle windows.Handle,
	overlapped *windows.Overlapped,
	ops pinnedDirectoryWindowsOps,
) error {
	if ops.unlock == nil {
		return errors.New("pinned directory Windows unlock operation is missing")
	}
	return ops.unlock(handle, overlapped)
}

// defaultPinnedDirectoryWindowsOps 构造真实的非阻塞独占 LockFileEx 操作。
func defaultPinnedDirectoryWindowsOps() pinnedDirectoryWindowsOps {
	return pinnedDirectoryWindowsOps{
		lock: func(handle windows.Handle, overlapped *windows.Overlapped) error {
			return windows.LockFileEx(
				handle,
				windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
				0,
				1,
				0,
				overlapped,
			)
		},
		unlock: func(handle windows.Handle, overlapped *windows.Overlapped) error {
			return windows.UnlockFileEx(handle, 0, 1, 0, overlapped)
		},
	}
}
