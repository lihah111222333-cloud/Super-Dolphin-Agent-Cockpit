package main

import (
	"context"
	"io"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/app"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli"
)

var runDesktopApp = func() error {
	return app.RunDesktop(frontendDistFS())
}

var runSkillMCPMode = func(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	return claudecli.RunSkillMCPMode(ctx, stdin, stdout)
}

func runMain(args []string) error {
	if hasMCPSkillMode(args) {
		return runSkillMCPMode(context.Background(), os.Stdin, os.Stdout)
	}
	return runDesktopApp()
}

func hasMCPSkillMode(args []string) bool {
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg == "--mcp-skill-mode" || strings.HasPrefix(arg, "--mcp-skill-mode=") {
			return true
		}
	}
	return false
}
