package config

import (
	"os"
	"runtime"
)

// resolveCurrentPackagedSQLiteHome 解析当前操作系统的打包应用用户数据目录。
func resolveCurrentPackagedSQLiteHome() (string, error) {
	var platform string
	switch runtime.GOOS {
	case "darwin":
		platform = "darwin"
	case "windows":
		platform = "windows"
	default:
		platform = "nonwindows"
	}
	return resolvePackagedSQLiteHome(platform, os.Getenv, os.UserHomeDir)
}
