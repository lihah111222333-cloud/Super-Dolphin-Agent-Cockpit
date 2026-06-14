package hiddenexec

import (
	"context"
	"os/exec"
)

// Command 处理命令。
func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	configureCommand(cmd)
	return cmd
}

// CommandContext 处理命令上下文。
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	configureCommand(cmd)
	return cmd
}
