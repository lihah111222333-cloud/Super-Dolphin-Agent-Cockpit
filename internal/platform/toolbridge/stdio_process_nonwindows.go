//go:build !windows

package toolbridge

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

type stdioProcessGuard struct{}

// stdioConfigureCommand 让 Unix stdio MCP 子进程进入独立进程组，便于整组终止。
func stdioConfigureCommand(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// stdioAttachProcessGuard 在 Unix 上不需要额外句柄，返回占位 guard 保持跨平台接口一致。
func stdioAttachProcessGuard(_ *exec.Cmd, _ bool) *stdioProcessGuard {
	return &stdioProcessGuard{}
}

// stdioTerminateProcessTree 优先杀掉 Unix 进程组，失败时再回退到单进程 Kill。
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

// stdioExpectedCloseWaitError 把预期内的 stdio 关闭错误归一化。
func stdioExpectedCloseWaitError(err error) error {
	if err == nil {
		return nil
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			switch status.Signal() {
			case syscall.SIGPIPE, syscall.SIGKILL, syscall.SIGTERM:
				return nil
			}
		}
	}
	return err
}

// stdioCleanupProcessTree 在关闭客户端后补杀残留进程组。
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

// stdioProcessGone 判断进程或进程组是否已经退出。
func stdioProcessGone(err error) bool {
	return errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH)
}
