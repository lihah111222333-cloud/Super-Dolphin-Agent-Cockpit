//go:build !windows

package hiddenexec

import "os/exec"

func configureCommand(_ *exec.Cmd) {}
