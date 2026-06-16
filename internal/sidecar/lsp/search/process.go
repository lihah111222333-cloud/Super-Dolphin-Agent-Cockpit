package search

import (
	"context"
	"os/exec"

	"github.com/anthropic-ai/super-agent-v3/internal/sidecar/lsp/internal/hiddenexec"
)

func hiddenCommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	return hiddenexec.CommandContext(ctx, name, args...)
}
