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

// resolveClaudeBinary is a passthrough on Unix: exec.Command handles PATH
// lookup correctly and there are no shell wrappers to unwrap.
func resolveClaudeBinary(binary string) string {
	return binary
}

// processGuard is the Unix no-op variant. Process-group semantics already
// give us the necessary subtree reach via negative-pid syscall.Kill.
type processGuard struct{}

func attachProcessGuard(cmd *exec.Cmd) *processGuard {
	_ = cmd
	return &processGuard{}
}

func (g *processGuard) close() {}

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
