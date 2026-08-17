//go:build windows

package sqlcconfig

import (
	"os/exec"
	"time"
)

func configureGitCommandCancellation(command *exec.Cmd) {
	command.Cancel = func() error { return command.Process.Kill() }
	command.WaitDelay = 500 * time.Millisecond
}

func cleanupGitCommand(*exec.Cmd) error { return nil }
