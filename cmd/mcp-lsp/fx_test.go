package main

import (
	"strings"
	"testing"
)

// TestNewStdioServerFailsFastWhenMcpStdoutNil 锁定 mcpStdout 未初始化时必须返回 error，
// 不得 fallback 到 os.Stdout，否则会把普通 stdout 当作 MCP JSON-RPC 通道继续使用。
func TestNewStdioServerFailsFastWhenMcpStdoutNil(t *testing.T) {
	prev := mcpStdout.Swap(nil)
	t.Cleanup(func() {
		if prev != nil {
			mcpStdout.Store(prev)
		}
	})

	_, err := newServer(ToolHandlers{})
	if err == nil {
		t.Fatal("newServer() error = nil, want error when mcpStdout is nil")
	}
	if !strings.Contains(err.Error(), "mcpStdout") {
		t.Fatalf("newServer() error = %q, want mention of mcpStdout", err.Error())
	}
}
