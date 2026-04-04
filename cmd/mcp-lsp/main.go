package main

import (
	"fmt"
	"os"
)

const (
	binaryName    = "mcp-lsp"
	binaryVersion = "dev"
)

// mcpStdout holds the original stdout exclusively for the MCP JSON-RPC
// protocol. All other output is redirected to stderr.
var mcpStdout *os.File

func main() {
	mcpStdout = os.Stdout
	os.Stdout = os.Stderr
	os.Exit(runMain())
}

func runMain() int {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", binaryName, err)
		return 1
	}
	return 0
}
