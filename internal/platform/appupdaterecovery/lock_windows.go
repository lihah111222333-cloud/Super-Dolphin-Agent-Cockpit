//go:build windows

package appupdaterecovery

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

const (
	transactionLockBytes uint32 = 1
	openReparsePoint     uint32 = 0x00200000
)

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

// captureDiscardRootIdentity 通过不跟随 reparse point 的句柄读取 discard 根实例身份。
func captureDiscardRootIdentity(path string) (identity discardRootIdentity, err error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return discardRootIdentity{}, fmt.Errorf("encode backup discard root path: %w", err)
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|openReparsePoint,
		0,
	)
	if err != nil {
		return discardRootIdentity{}, fmt.Errorf("open backup discard root identity: %w", err)
	}
	defer func() {
		if closeErr := windows.CloseHandle(handle); err == nil && closeErr != nil {
			err = fmt.Errorf("close backup discard root: %w", closeErr)
		}
	}()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil {
		return discardRootIdentity{}, fmt.Errorf("inspect backup discard root identity: %w", err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return discardRootIdentity{}, errors.New("backup discard root must not be a reparse point")
	}
	kind := discardRootKindFile
	if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		kind = discardRootKindDirectory
	}
	fileID := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	return discardRootIdentity{VolumeID: uint64(info.VolumeSerialNumber), FileID: fileID, Kind: kind}, nil
}
