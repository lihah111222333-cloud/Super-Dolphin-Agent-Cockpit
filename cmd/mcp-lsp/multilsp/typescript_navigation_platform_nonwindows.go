//go:build !windows

package multilsp

// platformTypeScriptNavigationModuleSearchPaths 保持非 Windows 的 Node 模块解析语义。
func platformTypeScriptNavigationModuleSearchPaths() ([]string, error) {
	return nil, nil
}
