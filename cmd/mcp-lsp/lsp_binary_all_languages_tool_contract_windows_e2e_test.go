//go:build windows && e2e

package main

import "path/filepath"

// Windows fake executable 使用 .exe 后缀，避免把无后缀脚本/文件误当作可启动命令。
func allLanguageToolContractExecutableName(name string) string {
	if filepath.Ext(name) == "" {
		return name + ".exe"
	}
	return name
}

// Windows 写入时已由公共 owner-only mode 完成权限约束，不需要 POSIX chmod。
func allLanguageToolContractPrepareExecutable(string) error {
	return nil
}

// Windows file URI 的盘符路径可能以 /C:/ 开头，归一化后再交给 filepath。
func allLanguageToolContractNativePathFromURIPath(path string) string {
	if len(path) > 2 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path)
}
