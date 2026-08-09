//go:build windows

// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"golang.org/x/sys/windows"
)

// windowsStillActive 代表 Windows 进程退出状态码中的 STILL_ACTIVE (259)。
const windowsStillActive = 259

// isParentOrphaned 在 Windows 系统上通过 OpenProcess 检查父进程句柄与退出码。
// 当父进程 PID 无效或其退出状态非 STILL_ACTIVE 时判定为父进程已退出（孤儿状态）。
func isParentOrphaned(ppid int) bool {
	if ppid <= 0 {
		return true
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(ppid))
	if err != nil {
		// 无法打开父进程句柄，说明父进程已终止
		return true
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	var exitCode uint32
	if err := windows.GetExitCodeProcess(handle, &exitCode); err != nil {
		return true
	}
	return exitCode != windowsStillActive
}
