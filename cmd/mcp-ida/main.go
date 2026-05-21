package main

import (
	"os"
	"runtime"

	_ "github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// mcpStdout holds the original stdout exclusively for the MCP JSON-RPC
// protocol. All other output (log, fmt, panic) goes to stderr so it
// can never pollute the protocol channel.
var mcpStdout *os.File

func protectMCPStdout() {
	mcpStdout = os.Stdout
	os.Stdout = os.Stderr
	pkglogger.InitWithConsoleWriter(os.Stderr)
}

func main() {
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
