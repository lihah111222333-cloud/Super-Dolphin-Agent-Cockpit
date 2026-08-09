//go:build !windows

// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

// isParentOrphaned 在 POSIX 系统 (Linux/macOS) 上校验父进程 ID 是否已被 init/launchd 接管 (PPID <= 1)。
func isParentOrphaned(ppid int) bool {
	return ppid <= 1
}
