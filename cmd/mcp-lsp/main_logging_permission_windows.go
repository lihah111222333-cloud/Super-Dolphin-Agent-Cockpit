//go:build windows

package main

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"

// wrapSidecarLogPathError 在 Windows 日志路径边界保留 Win32 5/1314 类型和脱敏路径，
// 让上层能够区分需要授权的 ACL 失败；它不修改 ACL，也不把授权误当成令牌提升。
func wrapSidecarLogPathError(operation, path string, err error) error {
	return securefs.NewWindowsPermissionError(operation, path, err)
}
