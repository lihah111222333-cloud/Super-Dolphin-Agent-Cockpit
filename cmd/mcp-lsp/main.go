// Package main 是 mcp-lsp sidecar 进程的入口，通过 MCP stdio 协议暴露 LSP 工具能力。
package main

import (
	"os"
	"runtime"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// binaryName 和 binaryVersion 标识本 sidecar 进程名称和版本。
const (
	binaryName    = "mcp-lsp"
	binaryVersion = "dev"
)

// mcpStdout holds the original stdout exclusively for the MCP JSON-RPC
// protocol. All other output is redirected to stderr.
var mcpStdout atomic.Pointer[os.File]

// main 初始化 sidecar 运行环境，保护 MCP stdout 通道后启动服务，异常时以非零码退出。
func main() {
	rlimit.Init()
	if err := os.Setenv("SUPER_DOLPHIN_PROCESS_ROLE", "sidecar"); err != nil {
		_, _ = os.Stderr.WriteString("mcp-lsp startup env failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := runtimeenv.ConfigureSidecarRuntime(); err != nil {
		_, _ = os.Stderr.WriteString("mcp-lsp sidecar runtime env failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	// Cap GOMAXPROCS for this lightweight sidecar (see cmd/mcp-orch/main.go).
	if runtime.GOMAXPROCS(0) > 2 {
		runtime.GOMAXPROCS(2)
	}
	mcpStdout.Store(os.Stdout)
	os.Stdout = os.Stderr
	os.Exit(runMain())
}

// runMain 运行 LSP 主逻辑，失败时返回非零退出码。
func runMain() int {
	if err := run(); err != nil {
		pkglogger.Get().Error("mcp-lsp failed", "error", err)
		return 1
	}
	return 0
}
