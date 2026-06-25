// Package main 是桌面终端应用的入口，负责初始化运行环境并启动 Wails 桌面 UI。
package main

import (
	"embed"
	"io/fs"
)

// frontendDist embeds the Vite build output used by the desktop host.
// The current React/Vite frontend-app build is copied into this embed path by
// Makefile build/test/run targets before Go compiles this package.
// The all: prefix preserves nested assets and dot-files in the built output.
//
//go:embed all:frontend/dist
var frontendDist embed.FS

// frontendDistFS 返回嵌入的前端静态资源子文件系统，供 Wails HTTP 服务使用。
func frontendDistFS() fs.FS {
	sub, err := fs.Sub(frontendDist, "frontend/dist")
	if err != nil {
		return frontendDist
	}
	return sub
}
