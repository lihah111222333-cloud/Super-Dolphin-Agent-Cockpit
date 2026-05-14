package main

import (
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"os"

	"github.com/anthropic-ai/super-agent-v3/internal/app"
	_ "github.com/anthropic-ai/super-agent-v3/internal/platform/rlimit"
)

func main() {
	if err := app.RunDesktop(frontendDistFS()); err != nil {
		pkglogger.Get().Error("agent-terminal failed", "error", err)
		os.Exit(1)
	}
}
