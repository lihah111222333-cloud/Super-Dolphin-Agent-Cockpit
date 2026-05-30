//go:build !windows

package toolbridge

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type stdioProcessGuard struct{}

func stdioConfigureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func stdioAttachProcessGuard(_ *exec.Cmd) *stdioProcessGuard {
	return &stdioProcessGuard{}
}

func stdioTerminateProcessTree(cmd *exec.Cmd, _ *stdioProcessGuard) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("toolbridge: invalid stdio MCP pid")
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil || stdioProcessGone(err) {
		return nil
	}
	err := cmd.Process.Kill()
	if stdioProcessGone(err) {
		return nil
	}
	return err
}

func stdioExpectedCloseWaitError(err error) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			switch status.Signal() {
			case syscall.SIGPIPE, syscall.SIGKILL, syscall.SIGTERM:
				return nil
			}
		}
	}
	return err
}

func stdioCleanupProcessTree(cmd *exec.Cmd, _ *stdioProcessGuard) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !stdioProcessGone(err) {
		return err
	}
	return nil
}

func stdioProcessGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}
