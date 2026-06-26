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

// Configure 让子进程进入独立进程组，便于停止整个 agent 进程树。
func Configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Attach 在 Unix 上无需额外句柄，返回空 Guard 以保持跨平台接口一致。
func Attach(_ *exec.Cmd, _ *slog.Logger) *Guard {
	return &Guard{}
}

// Close 关闭编排资源。
func (g *Guard) Close() {
	_ = g
}

// RequestStop 向进程组发送 SIGTERM，请求 agent 温和退出。
func RequestStop(cmd *exec.Cmd, guard *Guard) error {
	return signalProcess(cmd, guard, syscall.SIGTERM)
}

// ForceStop 向进程组发送 SIGKILL，作为超时后的强制兜底。
func ForceStop(cmd *exec.Cmd, guard *Guard) error {
	return signalProcess(cmd, guard, syscall.SIGKILL)
}

// signalProcess 优先向进程组发信号，失败时退回单进程信号。
// 进程已退出视为成功，避免 stop 路径因竞态误报失败。
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
