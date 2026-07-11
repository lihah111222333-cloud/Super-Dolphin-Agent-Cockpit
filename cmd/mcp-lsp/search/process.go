package search

import (
	"context"
	"os/exec"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
)

func hiddenCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return hiddenexec.CommandContext(ctx, name, args...)
}
