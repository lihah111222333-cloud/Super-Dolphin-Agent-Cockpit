//go:build windows

package main

import (
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// sidecarFileLoggerCanDeferPermissionError 只允许 typed Win32 5/1314 进入带 CallID 的
// tools/call；宿主审批本身不提升权限，也不会改变 owner-only DACL。
func sidecarFileLoggerCanDeferPermissionError(err error) bool {
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) || permissionErr == nil {
		return false
	}
	return permissionErr.Win32Code() == 5 || permissionErr.Win32Code() == 1314
}
