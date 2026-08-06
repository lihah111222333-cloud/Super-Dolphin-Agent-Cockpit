//go:build windows

package similarity

import (
	"os"

	"golang.org/x/sys/windows"
)

const ignoredLockBytes uint32 = 1

// lockIgnoredFile 获取 ignored set 的跨进程排他锁，并在持锁者结束后继续执行。
func lockIgnoredFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		ignoredLockBytes,
		0,
		&overlapped,
	)
}

// unlockIgnoredFile 释放 ignored set 的跨进程排他锁。
func unlockIgnoredFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, ignoredLockBytes, 0, &overlapped)
}
