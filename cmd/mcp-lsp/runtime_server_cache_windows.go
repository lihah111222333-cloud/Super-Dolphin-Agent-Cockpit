//go:build windows

package main

import (
	"errors"
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
	"golang.org/x/sys/windows"
)

// runtimeServerHardenPrivateDirectory 设置仅当前用户与 LocalSystem 可访问的受保护 DACL。
func runtimeServerHardenPrivateDirectory(path string) error {
	return securefs.RestrictPrivateOwnerOnly(path, 0o700)
}

func runtimeServerValidatePrivateDirectoryPlatform(path string, info os.FileInfo) error {
	return securefs.CheckPrivateOwnerOnly(path, info)
}

func runtimeServerTryLockResourceLease(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
}

func runtimeServerUnlockResourceLease(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func runtimeServerResourceLeaseLockBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
