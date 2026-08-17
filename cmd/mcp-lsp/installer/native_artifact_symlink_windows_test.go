//go:build windows

package installer

import (
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/securefs"
)

// nativeArtifactSymlinkAuthorizationRequired 在 Windows 构建中识别需要宿主授权的
// Win32 ACL 5/1314；windows build tag 防止 Windows 错误类型泄漏到其他平台测试。
func nativeArtifactSymlinkAuthorizationRequired(err error) (uint32, bool) {
	var permissionErr *securefs.WindowsPermissionError
	if !errors.As(err, &permissionErr) {
		return 0, false
	}
	code := permissionErr.Win32Code()
	return code, code == 5 || code == 1314
}
