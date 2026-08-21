//go:build windows

package main

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// syncUpdaterDirectory 用 backup-semantics directory handle 刷新 Windows recovery capsule 目录。
func syncUpdaterDirectory(path string) error {
	path16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("encode recovery capsule directory %s: %w", path, err)
	}
	handle, err := windows.CreateFile(path16, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("open recovery capsule directory for flush %s: %w", path, err)
	}
	flushErr := windows.FlushFileBuffers(handle)
	closeErr := windows.CloseHandle(handle)
	if flushErr != nil {
		flushErr = fmt.Errorf("flush recovery capsule directory %s: %w", path, flushErr)
	}
	return errors.Join(flushErr, closeErr)
}
