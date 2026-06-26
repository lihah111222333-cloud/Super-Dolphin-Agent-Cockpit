//go:build windows

package memory

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const memoryRootLockBytes uint32 = 1

// tryAcquireMemoryFileLock 在 Windows 平台锁住文件首字节，模拟记忆根的跨进程排他写边界。
func tryAcquireMemoryFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		memoryRootLockBytes,
		0,
		&overlapped,
	)
}

// releaseMemoryFileLock 释放 Windows 文件锁；失败时保留原始系统错误，便于定位锁状态异常。
func releaseMemoryFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, memoryRootLockBytes, 0, &overlapped)
}

// isMemoryFileLockBusy 将 Windows 锁冲突错误识别为记忆根忙碌，供上层返回稳定的并发写错误。
func isMemoryFileLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
