//go:build !windows

// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"errors"
	"syscall"
)

// isParentOrphaned 在 POSIX 系统 (Linux/macOS) 上校验父进程是否已终止或被 init/launchd/subreaper 接管。
// 满足以下任一条件即判定父进程已失活（孤儿状态）：
// 1. ppid <= 1 (已被 PID 1 init/launchd 接管)
// 2. syscall.Kill(ppid, 0) 返回 ESRCH (父进程已不存在)
func isParentOrphaned(ppid int) bool {
	if ppid <= 1 {
		return true
	}
	err := syscall.Kill(ppid, 0)
	if err == nil || errors.Is(err, syscall.EPERM) {
		return false
	}
	if errors.Is(err, syscall.ESRCH) {
		return true
	}
	return false
}
