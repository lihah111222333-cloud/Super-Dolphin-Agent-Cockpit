package main

import (
	"fmt"
	"os"

	_ "github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
)

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "agent-terminal: %v\n", err)
		os.Exit(1)
	}
}
