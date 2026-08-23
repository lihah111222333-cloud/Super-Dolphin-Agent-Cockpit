//go:build windows

package installer

import "os"

// nativeArtifactExecutableModeValid 在 Windows 平台按普通文件身份判断可执行性，不解释 POSIX 执行位。
func nativeArtifactExecutableModeValid(os.FileMode) bool {
	return true
}
