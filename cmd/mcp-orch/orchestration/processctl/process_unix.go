//go:build !windows

package processctl

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
)

type Guard struct{}

func Configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func Attach(_ *exec.Cmd, _ *slog.Logger) *Guard {
	return &Guard{}
}

func (g *Guard) Close() {
	_ = g
}

func RequestStop(cmd *exec.Cmd, guard *Guard) error {
	return signalProcess(cmd, guard, syscall.SIGTERM)
}

func ForceStop(cmd *exec.Cmd, guard *Guard) error {
	return signalProcess(cmd, guard, syscall.SIGKILL)
}

func signalProcess(cmd *exec.Cmd, _ *Guard, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("invalid agent pid")
	}
	if err := syscall.Kill(-pid, sig); err == nil {
		return nil
	}
	err := cmd.Process.Signal(sig)
	if isProcessGoneErr(err) {
		return nil
	}
	return err
}

func isProcessGoneErr(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}
