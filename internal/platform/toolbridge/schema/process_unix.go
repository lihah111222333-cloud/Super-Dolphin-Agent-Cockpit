//go:build !windows

package schema

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type processGuard struct{}

func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachProcessGuard(cmd *exec.Cmd) (*processGuard, error) {
	if cmd == nil || cmd.Process == nil {
		return nil, errors.New("process is not started")
	}
	groupID, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return nil, err
	}
	if groupID != cmd.Process.Pid {
		return nil, errors.New("child process group is not isolated")
	}
	return &processGuard{}, nil
}

// terminateProcessTree 终止 helper 的整个 Unix 进程组。
func terminateProcessTree(cmd *exec.Cmd, _ *processGuard) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("process is not started")
	}
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if err == nil || errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
		return nil
	}
	return err
}

func closeProcessGuard(_ *processGuard) error {
	return nil
}
