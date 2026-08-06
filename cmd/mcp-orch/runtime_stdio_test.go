package main

import (
	"strings"
	"testing"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// TestNewStdioServerFailsFastWhenMcpStdoutNil 锁定 Fix-B：mcpStdout 未初始化时必须返回 error，
// 不得 fallback 到 os.Stdout（已被重定向为 stderr），否则会破坏 JSON-RPC framing。
func TestNewStdioServerFailsFastWhenMcpStdoutNil(t *testing.T) {
	// 保存原值并在测试后恢复，避免污染同进程其他测试。
	prev := mcpStdout.Swap(nil)
	t.Cleanup(func() {
		if prev != nil {
			mcpStdout.Store(prev)
		}
	})

	_, err := newStdioServer(newRegistry(newRegistryParams{}), pkglogger.NewRuntime(pkglogger.RuntimeConfig{}))
	if err == nil {
		t.Fatal("newStdioServer() error = nil, want error when mcpStdout is nil")
	}
	if !strings.Contains(err.Error(), "mcpStdout") {
		t.Fatalf("newStdioServer() error = %q, want mention of mcpStdout", err.Error())
	}
}
