//go:build !windows

package installer

import "os"

// nativeArtifactExecutableModeValid 在非 Windows 平台要求受管文件具有至少一个执行位。
func nativeArtifactExecutableModeValid(mode os.FileMode) bool {
	return mode&0o111 != 0
}
