//go:build windows

package gateprivate

import (
	"os/exec"
	"time"
)

// ConfigureCommandCancellation 保留 CommandContext 的进程取消并限制管道回收等待。
func ConfigureCommandCancellation(command *exec.Cmd, waitDelay time.Duration) {
	command.WaitDelay = waitDelay
}
