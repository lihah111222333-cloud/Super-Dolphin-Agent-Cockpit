//go:build !linux

package main

import (
	"os"
	"os/exec"
)

func configureInterruptProcessGroup(cmd *exec.Cmd) {
	cmd.Cancel = nil
}

func interruptProcessGroup(cmd *exec.Cmd) error {
	return cmd.Process.Signal(os.Interrupt)
}
