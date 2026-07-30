//go:build windows

package gateprivate

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const exclusiveFileLockBytes uint32 = 1

func tryAcquireExclusiveFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		exclusiveFileLockBytes,
		0,
		&overlapped,
	)
}

func releaseExclusiveFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, exclusiveFileLockBytes, 0, &overlapped)
}

func isExclusiveFileLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
