package schema

import (
	"fmt"
)

const (
	filesystemWorkerWindowsAccessDeniedKind            = "access_denied"
	filesystemWorkerWindowsPrivilegeNotHeldKind        = "privilege_not_held"
	filesystemWorkerWindowsAccessDeniedCode     uint32 = 5
	filesystemWorkerWindowsPrivilegeNotHeldCode uint32 = 1314
)

// validateFilesystemWorkerPermissionFields 是 worker wire 的共享字段守卫；字段本身
// 跨平台可解码，但只有 Windows producer/consumer 才生成或恢复 typed 权限错误。
func validateFilesystemWorkerPermissionFields(workerErr *filesystemWorkerError) error {
	if workerErr == nil {
		return nil
	}
	// kind 必须按 wire 字面值匹配；不替换空白或大小写，避免把非规范字段
	// 放行到 Windows typed 权限恢复与审批边界。
	kind := workerErr.WindowsPermissionKind
	switch workerErr.WindowsErrorCode {
	case 0:
		if kind != "" {
			return fmt.Errorf("schema filesystem worker permission kind requires an error code")
		}
	case filesystemWorkerWindowsAccessDeniedCode:
		if kind != filesystemWorkerWindowsAccessDeniedKind {
			return fmt.Errorf("schema filesystem worker permission kind does not match error code 5")
		}
	case filesystemWorkerWindowsPrivilegeNotHeldCode:
		if kind != filesystemWorkerWindowsPrivilegeNotHeldKind {
			return fmt.Errorf("schema filesystem worker permission kind does not match error code 1314")
		}
	default:
		return fmt.Errorf("schema filesystem worker permission error code is unsupported")
	}
	return nil
}
