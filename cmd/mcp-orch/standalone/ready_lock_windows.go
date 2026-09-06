//go:build windows

package standalone

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryReadyLock(file *os.File) error {
	var position windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &position)
}

func unlockReady(file *os.File) error {
	var position windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &position)
}

func readyLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
