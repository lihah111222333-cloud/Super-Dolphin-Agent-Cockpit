package config

import (
	"os"
)

// resolveCurrentPackagedSQLiteHome 解析当前操作系统的打包应用用户数据目录。
func resolveCurrentPackagedSQLiteHome() (string, error) {
	return resolvePackagedSQLiteHome(packagedSQLitePlatform, os.Getenv, os.UserHomeDir)
}
