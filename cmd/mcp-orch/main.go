// Package main 是 mcp-orch 的入口，负责初始化运行时环境并启动编排进程。
package main

import (
	"os"
	"runtime"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// mcpStdout 独占保存原始 stdout，专用于 MCP JSON-RPC 协议输出，其他输出均走 stderr。
var mcpStdout atomic.Pointer[os.File]

// main 初始化 rlimit、环境变量和 GOMAXPROCS，保护 MCP stdio 通道后启动编排进程。
func main() {
	rlimit.Init()
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "sidecar"); err != nil {
		_, _ = os.Stderr.WriteString("mcp-orch startup env failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := runtimeenv.ConfigureSidecarRuntime(); err != nil {
		_, _ = os.Stderr.WriteString("mcp-orch sidecar runtime env failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	// 限制轻量 sidecar 的 GOMAXPROCS，避免默认 NumCPU 让空闲调度线程长期自旋消耗 CPU。
	if runtime.GOMAXPROCS(0) > 2 {
		runtime.GOMAXPROCS(2)
	}
	// 保护 MCP stdio 通道：保存真实 stdout 给 MCP server 使用，再把普通 stdout 重定向到 stderr。
	// 这样 log.Printf、fmt.Println、库初始化或 panic 的意外输出不会破坏 JSON-RPC framing。
	mcpStdout.Store(os.Stdout)
	os.Stdout = os.Stderr

	if err := run(); err != nil {
		pkglogger.Get().Error("mcp-orch failed", "error", err)
		os.Exit(1)
	}
}
