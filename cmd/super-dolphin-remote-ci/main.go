// Package main 提供独立的远程 CI descriptor 校验组合根。
package main

import (
	"os"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/remotecicli"
)

// main 启动独立 CLI，并将退出码交给可导入组合根。
func main() {
	os.Exit(remotecicli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
