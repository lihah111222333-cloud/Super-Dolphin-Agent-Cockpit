// Package main 是桌面终端应用的入口，负责初始化运行环境并启动 Wails 桌面 UI。
package main

import (
	"embed"
	"fmt"
	"io/fs"
)

// frontendDist 嵌入桌面宿主实际服务的前端构建产物。
// 构建脚本会先把当前 React/Vite frontend-app 复制到 web-dist，all: 前缀保证嵌套资源和点文件不丢失。
//
//go:embed all:web-dist
var frontendDist embed.FS

// frontendDistFS 返回嵌入的前端静态资源子文件系统，供 Wails HTTP 服务使用。
// fs.Sub 只在构建产物结构损坏时失败，错误交给 CLI 入口 fail-fast 退出。
func frontendDistFS() (fs.FS, error) {
	sub, err := fs.Sub(frontendDist, "web-dist")
	if err != nil {
		return nil, fmt.Errorf("frontendDistFS: embedded web-dist is missing or corrupt: %w", err)
	}
	return sub, nil
}
