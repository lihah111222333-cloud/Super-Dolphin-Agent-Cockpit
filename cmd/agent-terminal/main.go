package main

import (
	"fmt"
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "agent-terminal: %v\n", err)
		os.Exit(1)
	}
}
