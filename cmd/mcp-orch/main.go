package main

import (
	"fmt"
	"os"
	"runtime"

	_ "github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
)

// mcpStdout holds the original stdout exclusively for the MCP JSON-RPC
// protocol. All other output (log, fmt, panic) goes to stderr so it
// can never pollute the protocol channel.
var mcpStdout *os.File

func main() {
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
	mcpStdout = os.Stdout
	os.Stdout = os.Stderr

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-orch: %v\n", err)
		os.Exit(1)
	}
}
