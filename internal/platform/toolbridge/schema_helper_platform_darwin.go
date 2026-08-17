//go:build darwin

package toolbridge

import "path/filepath"

// schemaHelperPlatformGOOS 由 Darwin 编译选源固定 helper/manifest 平台名，
// 禁止公共生产代码再用 runtime.GOOS 分支选择平台行为。
func schemaHelperPlatformGOOS() string {
	return "darwin"
}

// packagedSchemaHelperDirectoryForExecutable 按 macOS app bundle 布局把
// Contents/MacOS 中的宿主映射到 Contents/Resources/bin；非 bundle 可执行文件仍就地查找。
func packagedSchemaHelperDirectoryForExecutable(executable string) string {
	dir := filepath.Dir(executable)
	if filepath.Base(dir) == "MacOS" && filepath.Base(filepath.Dir(dir)) == "Contents" {
		return filepath.Join(filepath.Dir(dir), "Resources", "bin")
	}
	return dir
}
