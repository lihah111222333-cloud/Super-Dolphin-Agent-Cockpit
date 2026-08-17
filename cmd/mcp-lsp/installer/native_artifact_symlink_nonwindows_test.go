//go:build !windows

package installer

// nativeArtifactSymlinkAuthorizationRequired 在非 Windows 构建中不存在 Win32 ACL
// 授权错误，任何安装失败都由公共测试按真实错误处理。
func nativeArtifactSymlinkAuthorizationRequired(_ error) (uint32, bool) {
	return 0, false
}
