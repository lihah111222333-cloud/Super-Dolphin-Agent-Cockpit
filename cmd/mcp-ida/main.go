package main

import (
	"fmt"
	"os"

	_ "github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "mcp-ida: %v\n", err)
		os.Exit(1)
	}
}
