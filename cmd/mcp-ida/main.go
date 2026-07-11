// Package main 是 mcp-ida sidecar 进程的入口，通过 MCP stdio 协议暴露 IDA 能力。
package main

import (
	"os"
	"runtime"
	"sync/atomic"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rlimit"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimeenv"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

// mcpStdout 保存原始 stdout 供 MCP JSON-RPC 协议独占使用。
// 日志、fmt 输出和 panic 都必须走 stderr，避免污染 stdio 协议帧。
var mcpStdout atomic.Pointer[os.File]

// protectMCPStdout 保存原始 stdout 供 MCP JSON-RPC 协议独占使用，将 os.Stdout 重定向到 stderr，
// 防止日志或 fmt 输出污染协议通道。
func protectMCPStdout() {
	mcpStdout.Store(os.Stdout)
	os.Stdout = os.Stderr
	pkglogger.InitWithConsoleWriter(os.Stderr)
}

// main 初始化 sidecar 运行环境，限制 GOMAXPROCS，保护 MCP stdout 通道后启动服务。
func main() {
	rlimit.Init()
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "sidecar"); err != nil {
		_, _ = os.Stderr.WriteString("mcp-ida startup env failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := runtimeenv.ConfigureSidecarRuntime(); err != nil {
		_, _ = os.Stderr.WriteString("mcp-ida sidecar runtime env failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	// sidecar 只处理轻量协议转发，限制调度线程避免和宿主/工具进程抢占 CPU。
	if runtime.GOMAXPROCS(0) > 2 {
		runtime.GOMAXPROCS(2)
	}
	// 保存真实 stdout 给 MCP server 后立即改写 os.Stdout，防止依赖初始化或 panic 写坏 JSON-RPC 帧。
	protectMCPStdout()

	if err := run(); err != nil {
		pkglogger.Get().Error("mcp-ida failed", "error", err)
		os.Exit(1)
	}
}
