//go:build e2e && windows

package main

import (
	"errors"

	"golang.org/x/sys/windows"
)

// processAliveForE2E 通过查询受限句柄检查 Windows 测试目标进程是否存活。
func processAliveForE2E(pid int) (alive bool, retErr error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() {
		retErr = errors.Join(retErr, windows.CloseHandle(handle))
	}()
	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return false, err
	}
	return exitCode == windowsStillActive, nil
}
