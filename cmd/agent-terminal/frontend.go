// Package main 是桌面终端应用的入口，负责初始化运行环境并启动 Wails 桌面 UI。
package main

import (
	"embed"
	"io/fs"
)

// frontendDist 嵌入桌面宿主实际服务的前端构建产物。
// 构建脚本会先把当前 React/Vite frontend-app 复制到该目录，all: 前缀保证嵌套资源和点文件不丢失。
//
//go:embed all:frontend/dist
var frontendDist embed.FS

// frontendDistFS 返回嵌入的前端静态资源子文件系统，供 Wails HTTP 服务使用。
// fs.Sub 只在构建产物结构损坏时失败，属不可恢复错误，直接 panic 而非静默回退。
func frontendDistFS() fs.FS {
	sub, err := fs.Sub(frontendDist, "frontend/dist")
	if err != nil {
		panic("frontendDistFS: embedded frontend/dist is missing or corrupt: " + err.Error())
	}
	return sub
}
