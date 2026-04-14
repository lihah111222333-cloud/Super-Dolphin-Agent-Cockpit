//go:build windows

package memory

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const memoryRootLockBytes uint32 = 1

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

func releaseMemoryFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, memoryRootLockBytes, 0, &overlapped)
}

func isMemoryFileLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
