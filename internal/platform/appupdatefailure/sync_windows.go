//go:build windows

package appupdatefailure

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// syncDirectory 使用目录句柄刷新 Windows StageDir 目录项。
func syncDirectory(stageDir string) error {
	path, err := windows.UTF16PtrFromString(stageDir)
	if err != nil {
		return fmt.Errorf("encode app update stage dir for sync: %w", err)
	}
	handle, err := windows.CreateFile(path, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fmt.Errorf("open app update stage dir for sync: %w", err)
	}
	defer windows.CloseHandle(handle)
	if err := windows.FlushFileBuffers(handle); err != nil {
		return fmt.Errorf("sync app update stage dir: %w", err)
	}
	return nil
}
