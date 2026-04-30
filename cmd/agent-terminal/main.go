package main

import (
	"fmt"
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/app"
	_ "github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
)

func main() {
	if err := app.RunDesktop(frontendDistFS()); err != nil {
		fmt.Fprintf(os.Stderr, "agent-terminal: %v\n", err)
		os.Exit(1)
	}
}
