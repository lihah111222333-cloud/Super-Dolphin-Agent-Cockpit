//go:build windows

package main

import (
	"os"
	"os/exec"
)

func configureInterruptProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}
}

func interruptProcessGroup(cmd *exec.Cmd) error {
	return cmd.Cancel()
}
