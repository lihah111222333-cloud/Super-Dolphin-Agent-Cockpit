//go:build windows

package main

import (
	"os/exec"
	"time"
)

func configurePeerCommandCancellation(command *exec.Cmd) {
	command.Cancel = func() error { return command.Process.Kill() }
	command.WaitDelay = 500 * time.Millisecond
}
