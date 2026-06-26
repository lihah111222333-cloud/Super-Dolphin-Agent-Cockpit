//go:build !windows

package claudecli

import (
	"errors"
	"os/exec"
	"syscall"
)

func setClaudeProcessAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// resolveClaudeBinary 在 Unix 上保持原值。
// exec.Command 会负责 PATH 查找，且没有 Windows npm wrapper 需要解包。
func resolveClaudeBinary(binary string) string {
	return binary
}

// processGuard 是 Unix 侧的空实现。
// 子进程已单独建进程组，负 PID kill 能覆盖 Claude CLI 拉起的子树。
type processGuard struct{}

func attachProcessGuard(cmd *exec.Cmd) *processGuard {
	_ = cmd
	return &processGuard{}
}

func (g *processGuard) close() {
	_ = g
}

func signalClaudeProcess(cmd *exec.Cmd, guard *processGuard, sig processSig) error {
	_ = guard
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return errors.New("invalid claude pid")
	}
	return syscall.Kill(-pid, toUnixSignal(sig))
}

func toUnixSignal(sig processSig) syscall.Signal {
	switch sig {
	case sigInterrupt:
		return syscall.SIGINT
	case sigTerminate:
		return syscall.SIGTERM
	case sigForceKill:
		return syscall.SIGKILL
	default:
		return syscall.SIGTERM
	}
}

func isProcessGoneErr(err error) bool {
	return errors.Is(err, syscall.ESRCH)
}
