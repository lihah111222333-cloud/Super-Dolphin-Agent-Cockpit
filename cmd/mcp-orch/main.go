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
	// Cap GOMAXPROCS for this lightweight sidecar. The default (NumCPU)
	// causes the Go scheduler to spin 10+ idle P threads in
	// findRunnable/stealWork, burning ~30% CPU per process for no gain.
	if runtime.GOMAXPROCS(0) > 2 {
		runtime.GOMAXPROCS(2)
	}
	// Protect the MCP stdio channel: save the real stdout for the MCP
	// server, then redirect os.Stdout to stderr so any accidental writes
	// (log.Printf, fmt.Println, library init, panics) can never break
	// the JSON-RPC framing.
	mcpStdout.Store(os.Stdout)
	os.Stdout = os.Stderr

	if err := run(); err != nil {
		pkglogger.Get().Error("mcp-orch failed", "error", err)
		os.Exit(1)
	}
}
