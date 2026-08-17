//go:build !windows

package installer

import "fmt"

// detectWindowsHostPlatform 在非 Windows 平台显式拒绝宿主探测；不模拟 Windows 事实，也不执行网络或写盘。
func detectWindowsHostPlatform() (WindowsHostPlatform, error) {
	return WindowsHostPlatform{}, fmt.Errorf("%w: Windows is required", ErrUnsupportedWindowsHostPlatform)
}
