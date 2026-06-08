//go:build windows

package embeddedpg

import (
	"os/exec"
	"syscall"
)

const (
	postgresCreateNewProcessGroup = 0x00000200
	postgresCreateNoWindow        = 0x08000000
)

func configurePostgresCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: postgresCreateNewProcessGroup | postgresCreateNoWindow,
		HideWindow:    true,
	}
}
