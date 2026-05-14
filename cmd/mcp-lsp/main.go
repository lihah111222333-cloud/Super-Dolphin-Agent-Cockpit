package main

import (
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"os"
	"runtime"

	_ "github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
)

const (
	binaryName    = "mcp-lsp"
	binaryVersion = "dev"
)

// mcpStdout holds the original stdout exclusively for the MCP JSON-RPC
// protocol. All other output is redirected to stderr.
var mcpStdout *os.File

func main() {
	// Cap GOMAXPROCS for this lightweight sidecar (see cmd/mcp-orch/main.go).
	if runtime.GOMAXPROCS(0) > 2 {
		runtime.GOMAXPROCS(2)
	}
	mcpStdout = os.Stdout
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
