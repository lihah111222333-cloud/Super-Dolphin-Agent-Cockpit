//go:build !windows

package heterocinode

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type batchProcessGuard struct{}

func startBatchProcess(command *exec.Cmd) (batchProcessGuard, error) {
	if command == nil {
		return batchProcessGuard{}, errors.New("batch command is nil")
	}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		return batchProcessGuard{}, err
	}
	return batchProcessGuard{}, nil
}

// stop 通过独立 Unix 进程组终止 command 及其后代。
func (batchProcessGuard) stop(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	pid := command.Process.Pid
	if pid <= 0 {
		return errors.New("batch process pid is invalid")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil || errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return command.Process.Kill()
}

func (batchProcessGuard) close() error {
	return nil
}

func isBatchProcessGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}
