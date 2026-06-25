// Package main 是 mcp-ida sidecar 进程的入口，通过 MCP stdio 协议暴露 IDA 能力。
package main

import (
	"os"
	"runtime"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// mcpStdout holds the original stdout exclusively for the MCP JSON-RPC
// protocol. All other output (log, fmt, panic) goes to stderr so it
// can never pollute the protocol channel.
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
	// Cap GOMAXPROCS for this lightweight sidecar (see cmd/mcp-orch/main.go).
	if runtime.GOMAXPROCS(0) > 2 {
		runtime.GOMAXPROCS(2)
	}
	// Protect the MCP stdio channel: save the real stdout for the MCP
	// server, then redirect os.Stdout to stderr so any accidental writes
	// (log.Printf, fmt.Println, library init, panics) can never break
	// the JSON-RPC framing.
	protectMCPStdout()

	if err := run(); err != nil {
		pkglogger.Get().Error("mcp-ida failed", "error", err)
		os.Exit(1)
	}
}
