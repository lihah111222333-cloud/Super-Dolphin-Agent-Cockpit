//go:build !darwin

package toolbridge

import (
	"path/filepath"
	"runtime"
)

// schemaHelperPlatformGOOS 在非 Darwin 编译目标上只暴露当前目标的 helper 文件名事实；
// 文件已由 !darwin 约束选源，不在公共生产代码中执行平台分支。
func schemaHelperPlatformGOOS() string {
	return runtime.GOOS
}

// packagedSchemaHelperDirectoryForExecutable 保持 Windows、Linux、FreeBSD 等非 Darwin
// 发布布局为“helper 与宿主可执行文件同目录”，不解释 macOS app bundle 路径。
func packagedSchemaHelperDirectoryForExecutable(executable string) string {
	return filepath.Dir(executable)
}
