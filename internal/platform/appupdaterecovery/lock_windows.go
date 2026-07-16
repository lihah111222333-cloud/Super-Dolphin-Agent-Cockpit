//go:build windows

package appupdaterecovery

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

const transactionLockBytes uint32 = 1

func tryAcquireTransactionFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		transactionLockBytes,
		0,
		&overlapped,
	)
}

func releaseTransactionFileLock(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, transactionLockBytes, 0, &overlapped)
}

func isTransactionFileLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
