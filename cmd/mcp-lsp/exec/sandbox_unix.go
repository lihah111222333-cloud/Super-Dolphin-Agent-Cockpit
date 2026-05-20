//go:build !windows

package exec

import (
	"errors"
	"os"
	osexec "os/exec"
	"strings"
	"syscall"
)

func setSandboxProcessAttrs(cmd *osexec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// sandboxGuard is the Unix no-op variant. Killing the process group via
// negative pid already reaches every descendant spawned by the sandboxed
// command.
type sandboxGuard struct{}

func attachSandboxGuard(cmd *osexec.Cmd) (*sandboxGuard, error) {
	_ = cmd
	return &sandboxGuard{}, nil
}

func (g *sandboxGuard) close() {}

func killSandboxProcess(process *os.Process, guard *sandboxGuard) error {
	_ = guard
	if process == nil {
		return nil
	}
	// Setpgid put the child and its descendants in their own process group,
	// so the negative-pid SIGKILL reaches the whole subtree. If the group
	// lookup fails we still escalate with process.Kill() as a belt-and-braces
	// fallback.
	groupErr := syscall.Kill(-process.Pid, syscall.SIGKILL)
	killErr := process.Kill()
	return errors.Join(groupErr, killErr)
}

// shellRequestArgs returns the argv used by Sandbox.ShellRequest to run an
// arbitrary shell command. The Unix convention is "<shell> -lc <command>";
// we honour $SHELL when set and fall back to /bin/sh.
func shellRequestArgs(command string) []string {
	shell := os.Getenv("SHELL")
	if strings.TrimSpace(shell) == "" {
		shell = "/bin/sh"
	}
	return []string{shell, "-lc", command}
}
