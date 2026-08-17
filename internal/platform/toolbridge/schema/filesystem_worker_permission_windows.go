//go:build windows

package schema

import (
	"errors"
	"syscall"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// filesystemWorkerPermissionMetadata 只从 securefs typed 错误生成 wire 字段；普通
// syscall.Errno（包括旧只读目录句柄失败）不会被猜测为 ACL 授权请求。
func filesystemWorkerPermissionMetadata(cause error) (uint32, string) {
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(cause, &permissionErr) || permissionErr == nil {
		return 0, ""
	}
	switch permissionErr.Win32Code() {
	case filesystemWorkerWindowsAccessDeniedCode:
		return filesystemWorkerWindowsAccessDeniedCode, filesystemWorkerWindowsAccessDeniedKind
	case filesystemWorkerWindowsPrivilegeNotHeldCode:
		return filesystemWorkerWindowsPrivilegeNotHeldCode, filesystemWorkerWindowsPrivilegeNotHeldKind
	default:
		return 0, ""
	}
}

// filesystemWorkerPermissionCause 在 parent 端重建最小 typed 错误；wire 不传路径，
// 因而重建错误只包含稳定 operation 与 Win32 code，保留 errors.As 而不泄露机器信息。
func filesystemWorkerPermissionCause(code uint32, kind string) (error, error) {
	if code == 0 && kind == "" {
		return nil, nil
	}
	if err := validateFilesystemWorkerPermissionFields(&filesystemWorkerError{
		WindowsErrorCode: code, WindowsPermissionKind: kind,
	}); err != nil {
		return nil, err
	}
	return securefs.NewWindowsPermissionError("schema filesystem worker", "", syscall.Errno(code)), nil
}
