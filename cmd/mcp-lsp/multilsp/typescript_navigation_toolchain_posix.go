//go:build !windows

package multilsp

// platformTypeScriptNavigationModuleSearchPaths 保持 POSIX 原有的
// project/bundle TypeScript 模块解析路径，不注入 PATH 工具链目录。
func platformTypeScriptNavigationModuleSearchPaths() ([]string, error) {
	return nil, nil
}
