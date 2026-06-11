package main

import (
	"os"
	"runtime"
	"sync/atomic"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/runtimeenv"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	binaryName    = "mcp-lsp"
	binaryVersion = "dev"
)

// mcpStdout holds the original stdout exclusively for the MCP JSON-RPC
// protocol. All other output is redirected to stderr.
var mcpStdout atomic.Pointer[os.File]

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

func runMain() int {
	if err := run(); err != nil {
		pkglogger.Get().Error("mcp-lsp failed", "error", err)
		return 1
	}
	return 0
}
