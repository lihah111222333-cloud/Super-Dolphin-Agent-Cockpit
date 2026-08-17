//go:build !windows

package schema

import "errors"

// filesystemWorkerPermissionMetadata 在非 Windows 上严格 no-op，避免把同数值 Unix
// errno 当成 Windows 授权分类。
func filesystemWorkerPermissionMetadata(error) (uint32, string) {
	return 0, ""
}

// filesystemWorkerPermissionCause 拒绝非 Windows wire 中的 Windows 权限字段；正常
// non-Windows producer 永远不会写入这些字段。
func filesystemWorkerPermissionCause(code uint32, kind string) (error, error) {
	if code != 0 || kind != "" {
		return nil, errors.New("非 Windows 不支持 Windows 权限字段")
	}
	return nil, nil
}
